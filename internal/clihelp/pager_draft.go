package clihelp

import (
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// A cooperating zsh function owns the destination. Only the shell can safely
// put literal text into its next editing buffer.
type helpDraft struct {
	recovery                                      string
	probeDone                                     bool
	probeStart, probeInput                        time.Time
	early                                         []tea.Msg
	input                                         *helpDraftInput
	output                                        *os.File
	text                                          []byte
	enabled, typed, invalid, pasteActive, settled bool
	started, lastInput                            time.Time
}

// Once draft capture starts, leave everything after the terminal's reply in
// the original tty. In particular the shell can read the rest of a fast burst
// even if a character and the reply arrived in the same write.
type helpDraftInput struct {
	spill          []byte
	pasteInterrupt bool
	lastRead       time.Time
	cursorQueries  int
	cursorMatched  int
	utf8Tail       []byte
	paste          []byte
	completed      []string
	*os.File
	output                                      io.Writer
	mu                                          sync.Mutex
	enabled, probing, waiting, replied, inPaste bool
	pending                                     []byte
}

const (
	// Saving and restoring the cursor lets zsh recognize a delayed report
	// without interpreting ordinary keystrokes or moving the visible prompt.
	draftCursorQuery = "\x1b7\x1b[H\x1b[6n\x1b8"
	draftCursorReply = "\x1b[1;1R"
	// Ultraviolet also decodes the wire reply as F3. Forward the unambiguous
	// cursor report only after confirming the reply from the terminal.
	decodedCursorReply = "\x1b[?1;1R"
	encodedEscape      = "\x1b[27u"
	pasteOpener        = "\x1b[200~"
	pasteCloser        = "\x1b[201~"
)

func (r *helpDraftInput) beginProbe() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probing = true
	r.cursorQueries = 1
}

// A delayed first ESC can be the start of the position report, or the user's
// Escape key on a terminal that never answers. Bound that ambiguity at startup.
func (r *helpDraftInput) expireProbe(deadline bool) (wait, escape bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !deadline && (r.cursorMatched > 0 || r.probing && r.cursorQueries == 0) {
		return true, false
	}
	escape = r.cursorMatched == 1
	r.cursorMatched, r.cursorQueries = 0, 0
	r.probing = false
	return false, escape
}

func (r *helpDraftInput) enableFraming() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = true
	r.probing = false
}

func (r *helpDraftInput) requestReply() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waiting = true
	r.cursorQueries++
	_, err := io.WriteString(r.output, draftCursorQuery)
	return err
}

// Withhold partial paste delimiters from Tea's escape-key timeout. A slow
// clipboard paste must arrive as a paste, even if its opener arrives in pieces.
func (r *helpDraftInput) frame(data []byte) []byte {
	out := make([]byte, 0, len(r.pending)+len(data))
	for _, b := range data {
		delimiter := pasteOpener
		if r.inPaste {
			delimiter = pasteCloser
		}
		if b == delimiter[len(r.pending)] {
			r.pending = append(r.pending, b)
			if len(r.pending) == len(delimiter) {
				out = append(out, draftCursorReply[:r.cursorMatched]...)
				out = append(out, r.pending...)
				r.pending = r.pending[:0]
				if r.inPaste {
					r.completed = append(r.completed, string(r.paste))
					r.paste = nil
				}
				r.inPaste = !r.inPaste
				r.pasteInterrupt = false
				r.cursorMatched = 0
			}
			continue
		}
		if r.inPaste {
			if b == 3 {
				r.pasteInterrupt = true
			}
			r.paste = append(r.paste, r.pending...)
			if b != 0x1b {
				r.paste = append(r.paste, b)
			}
		}
		prefix := r.pending
		if !r.inPaste && len(prefix) == 1 && (b == 0x1b || b == 3) {
			// A second escape sequence proves the preceding ESC was a key.
			prefix = []byte(encodedEscape)
		}
		for _, v := range prefix {
			out = r.forwardByte(out, v)
		}
		r.pending = r.pending[:0]
		if b == 0x1b {
			r.pending = append(r.pending, b)
			continue
		}
		out = r.forwardByte(out, b)
	}
	return out
}

// A cursor report can arrive in pieces. Give Tea the complete report in one
// read so its Escape timeout cannot turn the last bytes into command text.
func (r *helpDraftInput) forwardByte(out []byte, b byte) []byte {
	if r.cursorQueries == 0 || r.inPaste {
		return append(out, b)
	}
	if b == draftCursorReply[r.cursorMatched] {
		r.cursorMatched++
		if r.cursorMatched < len(draftCursorReply) {
			return out
		}
		r.cursorMatched = 0
		r.cursorQueries--
		r.replied = r.waiting && r.cursorQueries == 0
		return append(out, decodedCursorReply...)
	}
	if r.cursorMatched == 1 && b == draftCursorReply[0] {
		out = append(out, encodedEscape...)
	} else {
		out = append(out, draftCursorReply[:r.cursorMatched]...)
	}
	r.cursorMatched = 0
	if b == draftCursorReply[0] {
		r.cursorMatched = 1
		return out
	}
	return append(out, b)
}

func (r *helpDraftInput) completeUTF8(data []byte) []byte {
	if len(r.utf8Tail) != 0 {
		data = append(r.utf8Tail, data...)
		r.utf8Tail = nil
	}
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				r.utf8Tail = append([]byte(nil), data[i:]...)
				return data[:i]
			}
			break
		}
	}
	return data
}

func (r *helpDraftInput) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	// Tea's Unix cancelable reader waits for readability before this call.
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.spill) > 0 {
		n := copy(buf, r.spill)
		r.spill = r.spill[n:]
		return n, nil
	}
	if !r.enabled && !r.probing {
		return r.File.Read(buf)
	}
	if r.replied {
		return 0, io.EOF
	}
	nread := max(1, (len(buf)-len(pasteOpener))/len(encodedEscape)) // reserve space for explicit Escape keys
	if r.waiting {
		// A cursor report is variable length. Stop on its final byte so the
		// caller can read the rest of that very same packet.
		nread = 1
	}
	n, err := r.File.Read(buf[:nread])
	if n == 0 {
		return n, err
	}
	r.lastRead = time.Now()
	var framed []byte
	if r.probing {
		// Bracketed paste is still disabled. Frame only the admission reply.
		for _, b := range buf[:n] {
			framed = r.forwardByte(framed, b)
		}
	} else {
		framed = r.frame(buf[:n])
	}
	framed = r.completeUTF8(framed)
	// Promptly disambiguate an actual Escape key. A terminal queues this
	// cursor report behind the rest of any paste it is already transmitting.
	if len(r.pending) == 1 && !r.inPaste && !r.waiting {
		r.cursorQueries++
		if _, e := io.WriteString(r.output, draftCursorQuery); e != nil {
			return 0, e
		}
	}
	written := copy(buf, framed)
	r.spill = framed[written:]
	return written, err
}

func (r *helpDraftInput) pasteInProgress() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inPaste || len(r.pending) > 0 || len(r.utf8Tail) > 0 || r.cursorMatched > 0
}

// The shell can replay fixed terminal framing consumed before the handoff.
func (r *helpDraftInput) recoveryTag() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var tag string
	switch {
	case r.inPaste:
		tag = "p" + strconv.Itoa(len(r.pending))
	case len(r.pending) > 0:
		tag = "o" + strconv.Itoa(len(r.pending))
	}
	if r.cursorQueries > 0 {
		tag += "r"
		if r.cursorMatched > 0 {
			tag += strconv.Itoa(r.cursorMatched)
		}
	}
	return tag
}

func (r *helpDraftInput) canceledPaste() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inPaste && r.pasteInterrupt && time.Since(r.lastRead) >= 200*time.Millisecond
}

func (r *helpDraftInput) receivedReply() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.replied
}

func (r *helpDraftInput) takePaste(fallback string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.completed) == 0 {
		return fallback
	}
	text := r.completed[0]
	r.completed[0] = ""
	r.completed = r.completed[1:]
	return text
}

func (d *helpDraft) textFor(msg tea.Msg) string {
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		return v.Text
	case tea.PasteMsg:
		// The input decoder can normalize or discard embedded controls.
		// Validate the actual paste before staging any part of the command.
		raw := d.input.takePaste(v.Content)
		return strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	}
	return ""
}

func (d *helpDraft) appendText(text string) {
	if !utf8.ValidString(text) {
		d.invalid = true
		return
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' {
			d.invalid = true
			return
		}
	}
	if d.typed {
		d.text = append(d.text, text...)
	}
}

type draftProbeExpired struct{}
type checkDraftInput struct{}

// Queue early text until the first cursor report selects the interactive mode.
// A terminal without cursor reports keeps the ordinary navigation key bindings.
func (m *helpPager) negotiateDraft(msg tea.Msg) (bool, tea.Cmd) {
	d := m.draft
	switch v := msg.(type) {
	case tea.CursorPositionMsg:
		if v.X != 0 || v.Y != 0 {
			return true, nil
		}
		d.input.enableFraming()
		d.enabled = true
		if _, m.writeErr = io.WriteString(m.output, "\x1b[?2004h"); m.writeErr == nil {
			m.writeErr = m.draw()
		}
		if m.writeErr != nil {
			return true, m.leave()
		}
	case draftProbeExpired:
		expired := time.Since(d.probeStart) >= 2*time.Second
		if !expired && len(d.early) > 0 && time.Since(d.probeInput) < 200*time.Millisecond {
			return true, tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return draftProbeExpired{} })
		}
		wait, escape := d.input.expireProbe(expired)
		if wait {
			return true, tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return draftProbeExpired{} })
		}
		if escape {
			d.early = append(d.early, tea.KeyPressMsg{Code: tea.KeyEscape})
		}
	case tea.PasteMsg:
		d.probeInput = time.Now()
		d.early = append(d.early, v)
		return true, nil
	case tea.KeyPressMsg:
		if len(d.early) == 0 && !startsHelpDraft(v) {
			return false, nil
		}
		d.probeInput = time.Now()
		d.early = append(d.early, v)
		return true, nil
	default:
		return false, nil
	}
	d.probeDone = true
	var cmds []tea.Cmd
	for _, early := range d.early {
		if _, cmd := m.Update(early); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	d.early = nil
	return true, tea.Batch(cmds...)
}

func startsHelpDraft(key tea.KeyPressMsg) bool {
	return literalDraftKey(key) && key.Text != "q" && key.Text != "Q" && key.Text != " "
}

const draftLockModifiers = tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock

func literalDraftKey(key tea.KeyPressMsg) bool {
	return key.Text != "" && key.Mod&^(tea.ModShift|draftLockModifiers) == 0
}

func waitForDraftInput() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return checkDraftInput{} })
}

func (m *helpPager) startDraft(text string) tea.Cmd {
	m.draft.typed = true
	m.draft.appendText(text)
	m.viewport.GotoBottom()
	m.finished = true
	m.writeErr = m.stopMouse()
	if m.writeErr == nil {
		m.writeErr = m.draw()
	}
	return m.leave()
}

func (m *helpPager) finishDraftInput() tea.Cmd {
	d := m.draft
	d.started = time.Now()
	d.lastInput = d.started
	m.writeErr = d.input.requestReply()
	if m.writeErr != nil {
		return tea.Quit
	}
	return waitForDraftInput()
}

func (m *helpPager) collectDraft(msg tea.Msg) tea.Cmd {
	d := m.draft
	switch v := msg.(type) {
	case tea.CursorPositionMsg:
		if d.input.receivedReply() && !d.pasteActive {
			d.settled = true
			return tea.Quit
		}
	case checkDraftInput:
		if !d.pasteActive && !d.input.pasteInProgress() && time.Since(d.lastInput) >= 200*time.Millisecond {
			d.recovery = d.input.recoveryTag()
			d.settled = true
			return tea.Quit
		}
		return waitForDraftInput()
	case tea.KeyPressMsg:
		d.lastInput = time.Now()
		mod := v.Mod &^ draftLockModifiers
		switch {
		case v.Code == 'c' && mod == tea.ModCtrl:
			m.interrupted = true
		case v.Code == tea.KeyEnter && mod == 0:
			d.appendText("\n")
		case literalDraftKey(v):
			d.typed = true
			d.appendText(v.Text)
		default:
			// Only zsh knows what its editing keys do. Omitting one
			// could stage a different command from the one the user typed.
			d.invalid = true
		}
	case uv.UnknownEvent, uv.UnknownCsiEvent, uv.UnknownSs3Event:
		d.invalid = true
	case tea.PasteStartMsg:
		d.pasteActive = true
	case tea.PasteEndMsg:
		d.pasteActive = false
		d.lastInput = time.Now()
	case tea.PasteMsg:
		d.lastInput = time.Now()
		d.typed = true
		d.appendText(d.textFor(v))
	case tea.MouseWheelMsg:
		d.lastInput = time.Now()
	}
	return nil
}
