package custom

import (
	"context"
	"io"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// Only one bounded packet and one short key sequence are retained. Pasted
// content is discarded incrementally instead of accumulating an image-sized
// text buffer in the general terminal event reader.
func readInteractiveImageKeys(ctx context.Context, source interface {
	io.Reader
	Cancel() bool
}, cancel context.CancelCauseFunc) {
	type packet struct {
		data []byte
		err  error
	}
	packets := make(chan packet, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buffer := make([]byte, 4096)
		for {
			n, err := source.Read(buffer)
			if n == 0 && err == nil {
				err = io.ErrNoProgress
			}
			select {
			case <-ctx.Done():
				return
			case packets <- packet{data: append([]byte(nil), buffer[:n]...), err: err}:
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { source.Cancel(); <-readDone }()
	parser := interactiveImageKeys{}
	timer := time.NewTimer(uv.DefaultEscTimeout)
	timer.Stop()
	defer timer.Stop()
	var expired <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case packet := <-packets:
			for _, value := range packet.data {
				if stop, code := parser.feed(value); stop {
					cancel(interactiveImageExit{code})
					return
				}
			}
			if packet.err != nil {
				cancel(packet.err)
				return
			}
		case <-expired:
			if stop, code := parser.decode(true); stop {
				cancel(interactiveImageExit{code})
				return
			}
		}
		if len(parser.pending) > 0 {
			timer.Reset(uv.DefaultEscTimeout)
			expired = timer.C
		} else {
			timer.Stop()
			expired = nil
		}
	}
}

type interactiveImageKeys struct {
	pending []byte
	paste   bool
	end     int
}

func (p *interactiveImageKeys) feed(value byte) (bool, int) {
	if p.paste {
		const delimiter = "\x1b[201~"
		if value == delimiter[p.end] {
			p.end++
			if p.end == len(delimiter) {
				p.paste, p.end = false, 0
			}
		} else {
			p.end = 0
			if value == delimiter[0] {
				p.end = 1
			}
		}
		return false, 0
	}
	p.pending = append(p.pending, value)
	if len(p.pending) > 64 {
		p.pending = nil
		return false, 0
	}
	return p.decode(false)
}

func (p *interactiveImageKeys) decode(expired bool) (bool, int) {
	var decoder uv.EventDecoder
	for len(p.pending) > 0 {
		n, event := decoder.Decode(p.pending)
		if n <= 0 || n > len(p.pending) {
			p.pending = nil
			return false, 0
		}
		_, unknown := event.(uv.UnknownEvent)
		if !expired && (unknown || p.pending[0] == '\x1b' && n <= 2) {
			return false, 0
		}
		p.pending = p.pending[n:]
		if _, ok := event.(uv.PasteStartEvent); ok {
			p.paste = true
		}
		if key, ok := event.(uv.KeyPressEvent); ok {
			switch key.String() {
			case "q", "esc":
				return true, 0
			case "ctrl+c":
				return true, 130
			}
		}
	}
	return false, 0
}
