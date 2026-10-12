package custom

import (
	"errors"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// imagePickerInline keeps one transient logical line below the shell transcript.
// Hard line breaks let terminal reflow move old UI rows above a saved cursor on
// resize, where clearing them would risk shell history. Soft-wrapping a single
// block keeps the cursor inside that same logical line while its width changes.
// Bubble Tea continues to own key decoding and event ordering; runImagePicker
// owns console setup, restoration and resize notifications.
type imagePickerInline struct {
	model         *imagePicker
	output        *imagePickerOutput
	profile       colorprofile.Profile
	started       bool
	modesActive   bool
	restoreWrap   bool
	content       string
	width, height int
	err           error
	resuming      bool
	submitAfter   time.Time
}

// Legacy terminal input has no key-release event. A quiet interval absorbs
// queued submit keys and ordinary auto-repeat when reopening after a request.
const imagePickerResumeQuiet = 750 * time.Millisecond

type imagePickerWrapTimeout struct{}
type imagePickerSizeErrorMsg struct{}

func (p *imagePickerInline) Init() tea.Cmd {
	p.openModes()
	query := ansi.RequestModeAutoWrap
	if p.model.color {
		query += ansi.RequestBackgroundColor
	}
	_, _ = io.WriteString(p.output, query)
	// Older terminals do not report modes. They retain the normal shell wrap
	// assumption; terminals that report disabled wrapping get it restored below.
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return imagePickerWrapTimeout{} })
}

func (p *imagePickerInline) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case imagePickerSizeErrorMsg:
		p.err = errors.New("could not read the terminal size")
		return p, tea.Quit
	case tea.ModeReportMsg:
		if msg.Mode == ansi.ModeAutoWrap {
			if msg.Value == ansi.ModePermanentlyReset {
				p.err = errors.New("the image picker needs terminal line wrapping")
				return p, tea.Quit
			}
			p.restoreWrap = msg.Value == ansi.ModeReset
			p.start()
		}
	case imagePickerWrapTimeout:
		p.start()
	case tea.ColorProfileMsg:
		p.profile = msg.Profile
	}
	if !p.started {
		// Ignore input queued during generation before the new draft is visible.
		if p.resuming {
			switch msg := msg.(type) {
			case tea.KeyPressMsg:
				if msg.String() != "ctrl+c" {
					return p, nil
				}
			case tea.PasteMsg:
				return p, nil
			}
		}
		if key, ok := msg.(tea.KeyPressMsg); ok {
			switch key.String() {
			case "enter", "ctrl+g", "ctrl+p":
				return p, nil
			}
		}
	}
	if key, ok := msg.(tea.KeyPressMsg); ok && !p.submitAfter.IsZero() {
		switch key.String() {
		case "enter", "ctrl+g", "ctrl+p":
			now := time.Now()
			if now.Before(p.submitAfter) {
				p.submitAfter = now.Add(imagePickerResumeQuiet)
				return p, nil
			}
			p.submitAfter = time.Time{}
		}
	}
	_, cmd := p.model.Update(msg)
	if p.started {
		p.draw()
	}
	return p, cmd
}

func (p *imagePickerInline) View() tea.View { return tea.NewView("") }

func (p *imagePickerInline) start() {
	if p.started || p.err != nil {
		return
	}
	p.openModes()
	p.started = true
	_, _ = io.WriteString(p.output, ansi.SetModeAutoWrap)
}

func (p *imagePickerInline) openModes() {
	if !p.modesActive {
		p.modesActive = true
		_, _ = io.WriteString(p.output, ansi.ResetModeTextCursorEnable+ansi.SetModeBracketedPaste)
	}
}

func (p *imagePickerInline) draw() {
	width, height, err := term.GetSize(p.output.Fd())
	if err != nil {
		p.err = errors.New("could not read the terminal size")
		p.output.cancel()
		return
	}
	p.model.width, p.model.height = width, height
	p.model.clampCommandOffset()
	content := p.model.View().Content
	var converted strings.Builder
	// The picker already requires cursor support. A colorless profile must
	// preserve layout controls instead of stripping them as nonterminal output.
	_, _ = io.WriteString(&colorprofile.Writer{Forward: &converted, Profile: max(p.profile, colorprofile.ASCII)}, content)
	content = converted.String()
	if content == p.content && width == p.width && height == p.height {
		return
	}
	_, _ = io.WriteString(p.output, imagePickerInlineFrame(content, width))
	p.content, p.width, p.height = content, width, height
	if p.resuming {
		p.submitAfter = time.Now().Add(imagePickerResumeQuiet)
		p.resuming = false
	}
}

func imagePickerInlineFrame(content string, width int) string {
	var frame strings.Builder
	frame.WriteString("\r" + ansi.EraseScreenBelow)
	if content == "" || width <= 0 {
		return frame.String()
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		// A printable space completes the preceding row's pending wrap. CR
		// then returns to column zero without breaking that logical line.
		frame.WriteString(" \r")
		used, clipped := 0, false
		for len(line) > 0 {
			sequence, cells, read := imagePickerSequence(line)
			line = line[read:]
			if cells == 0 {
				frame.WriteString(sequence)
			} else if !clipped && used+cells < width {
				frame.WriteString(sequence)
				used += cells
			} else {
				clipped = true
			}
		}
		// Place the wrap boundary explicitly. Emoji clusters can occupy fewer
		// cells than wcwidth reports; spaces alone would leave the cursor on
		// the wrong row. The final cell is reserved from content above.
		frame.WriteString(ansi.CursorHorizontalAbsolute(width))
		frame.WriteByte(' ')
	}
	// CR cancels the pending wrap after the final boundary cell. All preceding
	// rows were reached by autowrap, so this remains one logical terminal line.
	frame.WriteByte('\r')
	if len(lines) > 1 {
		frame.WriteString(ansi.CursorUp(len(lines) - 1))
	}
	return frame.String()
}

// Count complete printable clusters, including ASCII-led keycap emoji. ANSI's
// sequence decoder alone treats the leading digit as a separate byte. Taking
// the larger width covers both scalar and grapheme-aware terminal presentation.
func imagePickerSequence(text string) (sequence string, cells, read int) {
	if text[0] == '\x1b' {
		sequence, cells, read, _ = ansi.DecodeSequenceWc(text, 0, nil)
		return
	}
	sequence, cells = ansi.FirstGraphemeCluster(text, ansi.WcWidth)
	return sequence, max(cells, ansi.StringWidth(sequence)), len(sequence)
}

func (p *imagePickerInline) close() {
	if !p.modesActive {
		return
	}
	cleanup := ansi.ResetModeBracketedPaste + ansi.SetModeTextCursorEnable
	if p.started {
		cleanup = "\r" + ansi.EraseScreenBelow + cleanup
	}
	if p.restoreWrap {
		cleanup += ansi.ResetModeAutoWrap
	}
	_, _ = io.WriteString(p.output, cleanup)
	p.started, p.modesActive = false, false
}
