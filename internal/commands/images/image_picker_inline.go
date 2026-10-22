package images

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// imagePickerInline keeps one transient logical line below the shell transcript.
// Hard line breaks let terminal reflow move old UI rows above a saved cursor on
// resize, where clearing them would risk shell history. Soft-wrapping a single
// block keeps the cursor inside that same logical line while its width changes.
// Bubble Tea continues to own key decoding and event ordering; runImagePicker
// owns console setup, restoration and resize notifications.
type imagePickerInline struct {
	model          *imagePicker
	output         *imagePickerOutput
	profile        colorprofile.Profile
	started        bool
	modesActive    bool
	mouseShell     bool
	restoreWrap    bool
	wrapKnown      bool
	wrapQueryless  bool
	wrapProbe      *imagePickerWrapProbe
	wrapRevision   uint64
	content        string
	width, height  int
	err            error
	resuming       bool
	submitAfter    time.Time
	cursor         *tea.Cursor
	anchored       bool
	cursorStyled   bool
	workspace      *imagePickerWorkspace
	generate       func(context.Context, *imagePickerResult) error
	requestContext context.Context
	requestCancel  context.CancelFunc
	requestDone    chan struct{}
	requestErr     error
	busy           bool
	savedSettings  imagePickerSettings
	selection      *imagePickerResult
}

// Legacy terminal input has no key-release event. A quiet interval absorbs
// queued submit keys and ordinary auto-repeat when reopening after a request.
const imagePickerResumeQuiet = 750 * time.Millisecond

type imagePickerWrapTimeout struct{ revision uint64 }
type imagePickerSizeErrorMsg struct{}

func (p *imagePickerInline) Init() tea.Cmd {
	p.openModes()
	query := ansi.RequestModeAutoWrap
	if os.Getenv("TERM_PROGRAM") == "Apple_Terminal" && os.Getenv("KITTY_WINDOW_ID") == "" {
		// Apple Terminal can expose the final p of unsupported DECRQM requests.
		query = ""
		p.wrapQueryless = true
		if p.workspace != nil {
			if p.mouseShell {
				query = p.workspace.mouse.openShell()
			} else {
				p.workspace.mouse.stopped = true
			}
		}
	} else if p.workspace != nil {
		query += imagePickerMouseQuery
	}
	if p.model.color {
		query += ansi.RequestBackgroundColor
	}
	_, _ = io.WriteString(p.output, query)
	if p.wrapQueryless {
		return p.probeWrapping()
	}
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return imagePickerWrapTimeout{} })
}

func (p *imagePickerInline) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer func() {
		if p.workspace != nil && (p.err != nil || p.model.result.Canceled || p.model.result.PrintOnly) {
			p.err = errors.Join(p.err, p.workspace.stopMouse())
		}
	}()
	if handled, cmd := p.updateWrapProbe(msg); handled {
		return p, cmd
	}
	if p.workspace != nil {
		if handled, cmd := p.updateWorkspace(msg); handled {
			return p, cmd
		}
	}
	switch msg := msg.(type) {
	case imagePickerSizeErrorMsg:
		p.err = errors.New("could not read the terminal size")
		return p, tea.Quit
	case tea.ModeReportMsg:
		if msg.Mode == ansi.ModeAutoWrap && !p.wrapQueryless {
			if msg.Value == ansi.ModePermanentlyReset {
				p.err = errors.New("the image picker needs terminal line wrapping")
				return p, tea.Quit
			}
			if !p.started {
				p.wrapKnown = msg.Value == ansi.ModeReset || msg.Value == ansi.ModeSet || msg.Value == ansi.ModePermanentlySet
				p.restoreWrap = msg.Value == ansi.ModeReset
			}
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
	if p.workspace != nil && p.model.result.Canceled {
		return p, cmd
	}
	if p.workspace != nil && len(p.model.result.Args) != 0 && !p.model.result.PrintOnly {
		return p, p.startWorkspaceRequest()
	}
	if p.started {
		p.draw()
	}
	return p, cmd
}

func (p *imagePickerInline) View() tea.View { return tea.NewView("") }

func (p *imagePickerInline) start() {
	if p.started || p.err != nil || p.wrapQueryless && !p.wrapKnown {
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
	if p.workspace != nil {
		if err := p.workspace.draw(p.model, p.profile, p.busy, true); err != nil {
			p.err = err
			p.output.cancel()
			return
		}
		p.prepareExpanded()
		if p.resuming {
			p.submitAfter = time.Now().Add(imagePickerResumeQuiet)
			p.resuming = false
		}
		return
	}
	view := p.model.View()
	content := view.Content
	var converted strings.Builder
	// The picker already requires cursor support. A colorless profile must
	// preserve layout controls instead of stripping them as nonterminal output.
	_, _ = io.WriteString(&colorprofile.Writer{Forward: &converted, Profile: max(p.profile, colorprofile.ASCII)}, content)
	content = converted.String()
	view.Content = content
	p.paint(view, width, height)
	if p.resuming {
		p.submitAfter = time.Now().Add(imagePickerResumeQuiet)
		p.resuming = false
	}
}

// Keep the frame origin separate from the visible terminal cursor. Moving the
// cursor must repaint its position even when every displayed byte stays equal.
func (p *imagePickerInline) paint(view tea.View, width, height int) {
	same := view.Content == p.content && width == p.width && height == p.height
	sameCursor := p.cursor == nil && view.Cursor == nil || p.cursor != nil && view.Cursor != nil && p.cursor.Position == view.Cursor.Position
	if same && sameCursor {
		return
	}
	var frame strings.Builder
	frame.WriteString(ansi.ResetModeTextCursorEnable)
	if p.anchored {
		frame.WriteString(ansi.RestoreCursor)
	}
	if !same {
		frame.WriteString(cliterminal.InlineFrame(view.Content, width))
		frame.WriteString(ansi.SaveCursor)
		p.anchored = true
	}
	if view.Cursor != nil {
		if view.Cursor.Y > 0 {
			frame.WriteString(ansi.CursorDown(view.Cursor.Y))
		}
		frame.WriteString(ansi.CursorHorizontalAbsolute(view.Cursor.X + 1))
		frame.WriteString(ansi.SetCursorStyle(5) + ansi.SetModeTextCursorEnable)
		p.cursorStyled = true
	}
	_, _ = io.WriteString(p.output, frame.String())
	p.content, p.width, p.height, p.cursor = view.Content, width, height, view.Cursor
}

func (p *imagePickerInline) close() {
	if !p.modesActive {
		return
	}
	cleanup := ansi.ResetModeBracketedPaste + ansi.SetModeTextCursorEnable
	if p.cursorStyled {
		cleanup = ansi.SetCursorStyle(0) + cleanup
	}
	if p.started && p.workspace == nil {
		cleanup = "\r" + ansi.EraseScreenBelow + cleanup
	}
	if p.anchored {
		cleanup = ansi.RestoreCursor + cleanup
	}
	if p.restoreWrap {
		cleanup += ansi.ResetModeAutoWrap
	}
	_, _ = io.WriteString(p.output, cleanup)
	p.started, p.modesActive = false, false
	p.anchored, p.cursorStyled, p.cursor = false, false, nil
}

// The worker can finish after Tea stops receiving events. Joining it also reads its final failure.
func (p *imagePickerInline) stopWorkspaceRequest() {
	if p.requestCancel == nil {
		return
	}
	p.requestCancel()
	<-p.requestDone
	if !imagePickerSafePreviewFailure(p.requestErr) {
		p.err = errors.Join(p.err, p.requestErr)
	}
}
