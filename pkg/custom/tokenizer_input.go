package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/openai/openai-cli/internal/tokenizer"
)

const (
	tokenizerPasteStart       = "\x1b[200~"
	tokenizerPasteEnd         = "\x1b[201~"
	tokenizerPastePlaceholder = "openai-tokenizer-input"
)

type tokenizerInputPasteErrorMsg struct{ message string }

type tokenizerCancelableInput interface {
	io.ReadCloser
	Cancel() bool
}

type tokenizerInputPacket struct {
	data []byte
	err  error
}

type tokenizerPendingInput struct {
	message tea.Msg
	ack     chan struct{}
}

// tokenizerInputBridge keeps raw pasted bytes outside the terminal decoder.
// A small ASCII paste preserves their position among ordinary decoded events.
type tokenizerInputBridge struct {
	ctx             context.Context
	cancel          context.CancelFunc
	source          tokenizerCancelableInput
	input, output   *os.File
	packets         chan tokenizerInputPacket
	readDone, done  chan struct{}
	stopOnce        sync.Once
	closeOnce       sync.Once
	stopCallback    func() bool
	mu              sync.Mutex
	pending         *tokenizerPendingInput
	err, closeError error
}

func newTokenizerInputBridge(ctx context.Context, original *os.File) (*tokenizerInputBridge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, err := uv.NewCancelReader(original)
	if err != nil {
		return nil, err
	}
	bridge, err := startTokenizerInputBridge(ctx, source)
	if err != nil {
		return nil, errors.Join(err, source.Close())
	}
	return bridge, nil
}

func startTokenizerInputBridge(parent context.Context, source tokenizerCancelableInput) (*tokenizerInputBridge, error) {
	input, output, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	b := &tokenizerInputBridge{
		ctx: ctx, cancel: cancel, source: source, input: input, output: output,
		packets: make(chan tokenizerInputPacket, 1), readDone: make(chan struct{}), done: make(chan struct{}),
	}
	b.stopCallback = context.AfterFunc(ctx, b.stop)
	go b.read()
	go b.pump()
	return b, nil
}

func (b *tokenizerInputBridge) Input() *os.File       { return b.input }
func (b *tokenizerInputBridge) Done() <-chan struct{} { return b.done }

func (b *tokenizerInputBridge) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// Resolve runs in the same event loop as ordinary keys. Acknowledging here
// bounds pending pastes and prevents their contents from overtaking prior keys.
func (b *tokenizerInputBridge) Resolve(message tea.Msg) tea.Msg {
	paste, ok := message.(tea.PasteMsg)
	if !ok || paste.Content != tokenizerPastePlaceholder {
		return message
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending == nil {
		return message
	}
	pending := b.pending
	b.pending = nil
	close(pending.ack)
	return pending.message
}

func (b *tokenizerInputBridge) stop() {
	b.stopOnce.Do(func() {
		b.source.Cancel()
		_ = b.input.Close()
		_ = b.output.Close()
	})
}

func (b *tokenizerInputBridge) Close() error {
	b.closeOnce.Do(func() {
		b.cancel()
		b.stop()
		<-b.done
		<-b.readDone
		b.stopCallback()
		b.closeError = errors.Join(b.Err(), b.source.Close())
	})
	return b.closeError
}

func (b *tokenizerInputBridge) read() {
	defer close(b.readDone)
	defer close(b.packets)
	buffer := make([]byte, 4096)
	for b.ctx.Err() == nil {
		n, err := b.source.Read(buffer)
		packet := tokenizerInputPacket{data: append([]byte(nil), buffer[:n]...), err: err}
		select {
		case <-b.ctx.Done():
			return
		case b.packets <- packet:
		}
		if err != nil {
			return
		}
	}
}

func (b *tokenizerInputBridge) pump() {
	defer close(b.done)
	defer b.output.Close()
	parser := tokenizerInputParser{bridge: b}
	timer := time.NewTimer(uv.DefaultEscTimeout)
	timer.Stop()
	defer timer.Stop()
	var expired <-chan time.Time
	var err error
	for err == nil {
		select {
		case <-b.ctx.Done():
			return
		case <-expired:
			err = parser.expireStart()
		case packet, ok := <-b.packets:
			if !ok {
				err = io.EOF
				break
			}
			err = parser.feed(packet.data)
			if err == nil && packet.err != nil {
				err = parser.finish()
				if err == nil {
					err = packet.err
				}
			}
		}
		// Only this bridge owns unfinished terminal sequences. Tea receives
		// complete bytes or decoded expiry events, never a competing ESC timer.
		if parser.ordinary.Len() > 0 {
			timer.Reset(uv.DefaultEscTimeout)
			expired = timer.C
		} else {
			timer.Stop()
			expired = nil
		}
	}
	if b.ctx.Err() == nil {
		b.mu.Lock()
		b.err = err
		b.mu.Unlock()
	}
}

func (b *tokenizerInputBridge) publish(message tea.Msg, opened bool) error {
	pending := &tokenizerPendingInput{message: message, ack: make(chan struct{})}
	b.mu.Lock()
	b.pending = pending
	b.mu.Unlock()
	marker := tokenizerPastePlaceholder + tokenizerPasteEnd
	if !opened {
		marker = tokenizerPasteStart + marker
	}
	if _, err := io.WriteString(b.output, marker); err != nil {
		return err
	}
	select {
	case <-b.ctx.Done():
		return b.ctx.Err()
	case <-pending.ack:
		return nil
	}
}

type tokenizerInputParser struct {
	bridge               *tokenizerInputBridge
	ordinary             bytes.Buffer
	startMatch, endMatch int
	paste, oversized     bool
	content, replacement []byte
}

func (p *tokenizerInputParser) feed(data []byte) error {
	for _, value := range data {
		if p.paste {
			if err := p.pasteByte(value); err != nil {
				return err
			}
			continue
		}
		// The decoder also drops a literal U+FFFD outside bracketed paste.
		// Keep only its three-byte prefix; all other key bytes pass unchanged.
		if len(p.replacement) > 0 || value == 0xef {
			p.replacement = append(p.replacement, value)
			for len(p.replacement) > 0 && !bytes.HasPrefix([]byte("\ufffd"), p.replacement) {
				if err := p.ordinaryByte(p.replacement[0]); err != nil {
					return err
				}
				p.replacement = p.replacement[1:]
			}
			if bytes.Equal(p.replacement, []byte("\ufffd")) {
				if err := p.expireStart(); err != nil {
					return err
				}
				p.replacement = nil
				if err := p.bridge.publish(tea.KeyPressMsg{Code: '\ufffd', Text: "\ufffd"}, false); err != nil {
					return err
				}
			}
			continue
		}
		if err := p.ordinaryByte(value); err != nil {
			return err
		}
	}
	if err := p.flush(); err != nil {
		return err
	}
	if p.ordinary.Len() > tokenizer.MaxInputBytes {
		return errors.New("incomplete terminal input sequence exceeds 1 MiB")
	}
	return nil
}

func (p *tokenizerInputParser) ordinaryByte(value byte) error {
	p.ordinary.WriteByte(value)
	if value == tokenizerPasteStart[p.startMatch] {
		p.startMatch++
	} else {
		p.startMatch = 0
		if value == tokenizerPasteStart[0] {
			p.startMatch = 1
		}
	}
	if p.startMatch == len(tokenizerPasteStart) {
		p.startMatch = 0
		// A delimiter inside another control sequence is not a paste opener.
		var decoder uv.EventDecoder
		data := p.ordinary.Bytes()
		opened := false
		for len(data) > 0 {
			n, event := decoder.Decode(data)
			if n <= 0 || n > len(data) {
				return errors.New("could not decode terminal input")
			}
			_, opened = event.(uv.PasteStartEvent)
			data = data[n:]
		}
		if !opened {
			return nil
		}
		p.ordinary.Truncate(p.ordinary.Len() - len(tokenizerPasteStart))
		if err := p.expireStart(); err != nil {
			return err
		}
		p.paste, p.oversized = true, false
		p.content, p.endMatch = nil, 0
		_, err := io.WriteString(p.bridge.output, tokenizerPasteStart)
		return err
	}
	return nil
}

func (p *tokenizerInputParser) expireStart() error {
	p.startMatch = 0
	return p.flushOrdinary(true)
}

func (p *tokenizerInputParser) pasteByte(value byte) error {
	if value == tokenizerPasteEnd[p.endMatch] {
		p.endMatch++
		if p.endMatch != len(tokenizerPasteEnd) {
			return nil
		}
		var message tea.Msg = tea.PasteMsg{Content: string(p.content)}
		if p.oversized {
			message = tokenizerInputPasteErrorMsg{message: "Paste rejected: the input limit is 1 MiB. Your text is unchanged."}
		}
		p.content, p.endMatch, p.paste = nil, 0, false
		return p.bridge.publish(message, true)
	}
	if p.endMatch > 0 {
		for i := 0; i < p.endMatch; i++ {
			p.appendContent(tokenizerPasteEnd[i])
		}
		p.endMatch = 0
		if value == tokenizerPasteEnd[0] {
			p.endMatch = 1
			return nil
		}
	}
	p.appendContent(value)
	return nil
}

func (p *tokenizerInputParser) appendContent(value byte) {
	if p.oversized {
		return
	}
	if len(p.content) == tokenizer.MaxInputBytes {
		p.oversized, p.content = true, nil
		return
	}
	p.content = append(p.content, value)
}

func (p *tokenizerInputParser) flush() error {
	return p.flushOrdinary(false)
}

func (p *tokenizerInputParser) writeOrdinary(size int) error {
	if size == 0 {
		return nil
	}
	n, err := p.bridge.output.Write(p.ordinary.Bytes()[:size])
	p.ordinary.Next(n)
	if err == nil && n != size {
		return io.ErrShortWrite
	}
	return err
}

func (p *tokenizerInputParser) flushOrdinary(expired bool) error {
	var decoder uv.EventDecoder
	ready := 0
	for ready < p.ordinary.Len() {
		data := p.ordinary.Bytes()[ready:]
		n, event := decoder.Decode(data)
		if n <= 0 || n > len(data) {
			return errors.New("could not decode terminal input")
		}
		_, unknown := event.(uv.UnknownEvent)
		// TerminalReader holds unknown sequences and short ESC-prefixed keys
		// until its escape timeout. Keep those bytes here, using the same decoder.
		ambiguous := unknown || data[0] == '\x1b' && n <= 2
		if ambiguous && !expired {
			break
		}
		if ambiguous {
			if err := p.writeOrdinary(ready); err != nil {
				return err
			}
			p.ordinary.Next(n)
			ready = 0
			var message tea.Msg = event
			if key, ok := event.(uv.KeyPressEvent); ok {
				message = tea.KeyPressMsg(key)
			}
			if err := p.bridge.publish(message, false); err != nil {
				return err
			}
		} else {
			ready += n
		}
	}
	if err := p.writeOrdinary(ready); err != nil {
		return err
	}
	if p.ordinary.Len() < p.startMatch {
		p.startMatch = 0
	}
	return nil
}

func (p *tokenizerInputParser) finish() error {
	if p.paste {
		p.content = nil
		return p.bridge.publish(tokenizerInputPasteErrorMsg{message: "Paste interrupted. Your text is unchanged; paste the complete text again."}, true)
	}
	p.ordinary.Write(p.replacement)
	p.replacement = nil
	return p.expireStart()
}
