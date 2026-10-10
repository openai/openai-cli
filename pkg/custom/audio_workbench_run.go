package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/urfave/cli/v3"
)

func runAudioWorkbench(parent context.Context, command *cli.Command, next cli.ActionFunc, store audiohistory.Store, input, output *os.File) (resultErr error) {
	ctx, stopSignals := downloadSignalContext(parent)
	defer stopSignals()
	signalContext := ctx
	defer func() {
		var interrupted *downloadSignalError
		if errors.As(context.Cause(signalContext), &interrupted) {
			resultErr = errors.Join(cli.Exit("", interrupted.exitCode), resultErr)
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	terminal, err := newTokenizerTerminalOutput(ctx, executable, output)
	if err != nil {
		return audioFailure("Could not prepare terminal output.", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, audioFailureIf("Could not close terminal output.", terminal.Close()))
	}()
	inputState, err := term.GetState(input.Fd())
	if err != nil {
		return err
	}
	outputState, err := term.GetState(output.Fd())
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, term.Restore(input.Fd(), inputState), term.Restore(output.Fd(), outputState))
	}()
	console := uv.NewConsole(input, output, os.Environ())
	if _, err = console.MakeRaw(); err != nil {
		return err
	}
	painter := newTokenizerPainter(terminal, cancel)
	defer painter.Stop()
	m := newAudioWorkbench(ctx, command, next, store)
	m.width, m.height = width, height
	m.dark = codexGuideDarkBackground(os.Getenv("COLORFGBG"))
	profile := prettyColorProfile(output, os.Environ())
	m.color = profile >= colorprofile.ANSI
	inline := &audioWorkbenchInline{model: m, painter: painter, output: output, profile: profile, cancel: cancel}
	program := tea.NewProgram(inline, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(io.Discard), tea.WithoutSignalHandler(), tea.WithoutRenderer(), tea.WithWindowSize(width, height), tea.WithColorProfile(profile))
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w, h, err := term.GetSize(output.Fd())
				if err != nil {
					program.Send(audioWorkbenchSizeError{})
					return
				}
				if w != width || h != height {
					width, height = w, h
					program.Send(tea.WindowSizeMsg{Width: w, Height: h})
				}
			}
		}
	}()
	_, runErr := program.Run()
	contextErr := downloadContextError(ctx)
	m.workers.close(cancel)
	<-listenerDone
	writeErr := painter.Stop()
	cleanupErr := closeTokenizerEditorFrame(terminal, inline.close(), m.exitCode != 0 || contextErr != nil)
	resultErr = audioFailureIf("Could not finish the audio terminal display.", errors.Join(writeErr, inline.err, cleanupErr))
	if m.exitCode != 0 {
		return errors.Join(resultErr, cli.Exit("", m.exitCode))
	}
	if resultErr != nil {
		return resultErr
	}
	return errors.Join(m.lastErr, runErr, contextErr)
}

type audioWorkbenchWrapTimeout struct{}
type audioWorkbenchSizeError struct{}

// Reuse the existing inline frame and asynchronous terminal writer. This owns
// only bracketed paste, cursor visibility, and wrapping; keyboard modes stay put.
type audioWorkbenchInline struct {
	model                             *audioWorkbench
	painter                           tokenizerFramePainter
	output                            *os.File
	profile                           colorprofile.Profile
	cancel                            context.CancelFunc
	started, modesActive, restoreWrap bool
	content                           string
	width, height                     int
	err                               error
}

func (p *audioWorkbenchInline) Init() tea.Cmd {
	p.modesActive = true
	query := ansi.ResetModeTextCursorEnable + ansi.SetModeBracketedPaste + ansi.RequestModeAutoWrap
	if p.model.color {
		query += ansi.RequestBackgroundColor
	}
	p.painter.Control(query)
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return audioWorkbenchWrapTimeout{} })
}

func (p *audioWorkbenchInline) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case audioWorkbenchSizeError:
		p.err = errors.New("could not read terminal size")
		return p, tea.Quit
	case tea.ModeReportMsg:
		if msg.Mode == ansi.ModeAutoWrap {
			if msg.Value == ansi.ModePermanentlyReset {
				p.err = errors.New("terminal wrapping is unavailable")
				return p, tea.Quit
			}
			p.restoreWrap = msg.Value == ansi.ModeReset
			p.start()
		}
	case audioWorkbenchWrapTimeout:
		p.start()
	}
	if key, ok := message.(tea.KeyPressMsg); !p.started && ok && key.String() != "ctrl+c" {
		return p, nil
	}
	// Read current geometry before acting on keys. A queued resize notification
	// must not let a hidden Generate control submit from a smaller terminal.
	if _, key := message.(tea.KeyPressMsg); key {
		w, h, err := term.GetSize(p.output.Fd())
		if err != nil {
			p.err = errors.New("could not read terminal size")
			return p, tea.Quit
		}
		if w != p.model.width || h != p.model.height {
			p.model.Update(tea.WindowSizeMsg{Width: w, Height: h})
		}
	}
	_, cmd := p.model.Update(message)
	if p.started {
		p.draw()
	}
	return p, cmd
}

func (p *audioWorkbenchInline) View() tea.View { return tea.NewView("") }
func (p *audioWorkbenchInline) start() {
	if p.started || p.err != nil {
		return
	}
	p.started = true
	p.painter.Control(ansi.SetModeAutoWrap)
}
func (p *audioWorkbenchInline) draw() {
	w, h, err := term.GetSize(p.output.Fd())
	if err != nil {
		p.err = errors.New("could not read terminal size")
		p.cancel()
		return
	}
	var converted strings.Builder
	_, _ = io.WriteString(&colorprofile.Writer{Forward: &converted, Profile: max(p.profile, colorprofile.ASCII)}, p.model.View().Content)
	content := converted.String()
	if content == p.content && w == p.width && h == p.height {
		return
	}
	p.painter.Frame(imagePickerInlineFrame(content, w))
	p.content, p.width, p.height = content, w, h
}
func (p *audioWorkbenchInline) close() string {
	if !p.modesActive {
		return ""
	}
	cleanup := "\x18\x1b\\" + ansi.ResetStyle
	if p.started {
		cleanup += "\r" + ansi.EraseScreenBelow
	}
	cleanup += ansi.ResetModeBracketedPaste + ansi.SetModeTextCursorEnable
	if p.restoreWrap {
		cleanup += ansi.ResetModeAutoWrap
	}
	p.started, p.modesActive = false, false
	return cleanup
}
