package listview

import (
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
)

// Keep the preview on one soft-wrapped logical terminal line. On a width
// change, conventional hard lines can reflow above the renderer's cursor
// and leave old records in the shell's scrollback.
type recordPreviewModel struct {
	model         *Model
	out           io.Writer
	width, height int
	content       string
	started       bool
}

func (p *recordPreviewModel) Init() tea.Cmd {
	p.started = true
	_, _ = io.WriteString(p.out, ansi.SaveCursor+ansi.SetModeAutoWrap+ansi.ResetModeTextCursorEnable+ansi.SetModeBracketedPaste)
	cmd := p.model.Init()
	p.draw()
	return cmd
}

func (p *recordPreviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		if p.width != size.Width || p.height != size.Height {
			p.content = ""
		}
		p.width, p.height = size.Width, size.Height
	}
	_, cmd := p.model.Update(msg)
	p.draw()
	return p, cmd
}

func (p *recordPreviewModel) draw() {
	content := p.model.View().Content
	if content == "" || content == p.content {
		return
	}
	p.content = content
	frame := terminal.InlineFrame(content, p.width)
	if p.width == 1 {
		frame = "\r" + ansi.EraseScreenBelow + "q\r"
	}
	_, _ = io.WriteString(p.out, frame)
}

func (p *recordPreviewModel) View() tea.View { return tea.NewView("") }

func (p *recordPreviewModel) close() {
	if p.started {
		_, _ = io.WriteString(p.out, "\r"+ansi.EraseScreenBelow+ansi.RestoreCursor+
			"\r"+ansi.ResetModeBracketedPaste+ansi.SetModeTextCursorEnable)
	}
}
