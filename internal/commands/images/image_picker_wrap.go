package images

import (
	"errors"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

type imagePickerWrapProbe struct {
	width, height int
	revision      uint64
	invalid       bool
}

// Apple Terminal does not implement DECRQM or private mode save/restore.
// Observe wrapping with spaces on the blank row the initial frame owns. DECRC
// immediately restores the original cursor and wrap mode before processing input.
func (p *imagePickerInline) probeWrapping() tea.Cmd {
	width, height := p.model.width, p.model.height
	if width < 3 || height < 2 {
		// No ambiguous one-column probe. Resize or cancellation remains usable.
		return nil
	}
	p.wrapRevision++
	revision := p.wrapRevision
	p.wrapProbe = &imagePickerWrapProbe{width: width, height: height, revision: revision}
	_, _ = io.WriteString(p.output, ansi.SaveCursor+"\r"+ansi.EraseScreenBelow+
		ansi.CursorHorizontalAbsolute(width)+"  "+ansi.RequestCursorPositionReport+
		ansi.RestoreCursor+"\r"+ansi.EraseScreenBelow)
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return imagePickerWrapTimeout{revision} })
}

func (p *imagePickerInline) updateWrapProbe(msg tea.Msg) (bool, tea.Cmd) {
	if p.err != nil {
		return true, tea.Quit
	}
	if !p.wrapQueryless || p.started {
		return false, nil
	}
	fail := func() (bool, tea.Cmd) {
		p.err = errors.New("could not read terminal wrapping; retry, or provide --prompt to generate without the picker")
		return true, tea.Quit
	}
	switch msg := msg.(type) {
	case tea.CursorPositionMsg:
		if p.wrapProbe == nil {
			return true, nil
		}
		probe := p.wrapProbe
		width, height, err := term.GetSize(p.output.Fd())
		if err != nil {
			return fail()
		}
		if probe.invalid || width != probe.width || height != probe.height {
			// Consume the old CPR before probing the replacement geometry.
			// There is only one outstanding request, despite untagged replies.
			p.wrapProbe = nil
			p.model.width, p.model.height = width, height
			return true, p.probeWrapping()
		}
		if msg.Y < 0 || msg.Y >= height || msg.X != 1 && msg.X != width-1 {
			return fail()
		}
		p.restoreWrap, p.wrapKnown, p.wrapProbe = msg.X == width-1, true, nil
		p.start()
		p.draw()
		return true, nil
	case tea.WindowSizeMsg:
		if p.wrapProbe != nil {
			if msg.Width != p.wrapProbe.width || msg.Height != p.wrapProbe.height {
				p.wrapProbe.invalid = true
			}
			p.model.width, p.model.height = msg.Width, msg.Height
			return true, nil
		}
		p.model.width, p.model.height = msg.Width, msg.Height
		return true, p.probeWrapping()
	case imagePickerWrapTimeout:
		if p.wrapProbe != nil && msg.revision == p.wrapProbe.revision {
			return fail()
		}
		return true, nil
	}
	return false, nil
}
