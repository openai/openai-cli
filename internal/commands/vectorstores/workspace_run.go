package vectorstores

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

func runWorkspace(parent context.Context, service Service, errorMessage func(error) string, input, output *os.File) (resultErr error) {
	ctx, stopSignals := workspaceSignalContext(parent)
	signalContext := ctx
	defer func() {
		stopSignals()
		var interrupted *workspaceSignalError
		if errors.As(context.Cause(signalContext), &interrupted) {
			resultErr = errors.Join(cli.Exit("", interrupted.code), resultErr)
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return workspaceFailure("Could not read the terminal size.", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return workspaceFailure("Could not prepare terminal output.", err)
	}
	terminal, err := cliterminal.NewOutput(ctx, executable, output)
	if err != nil {
		return workspaceFailure("Could not prepare terminal output.", err)
	}
	defer func() {
		if err := terminal.Close(); err != nil {
			resultErr = errors.Join(resultErr, workspaceFailure("Could not close terminal output.", err))
		}
	}()
	inputState, err := term.GetState(input.Fd())
	if err != nil {
		return workspaceFailure("Could not read terminal input state.", err)
	}
	outputState, err := term.GetState(output.Fd())
	if err != nil {
		return workspaceFailure("Could not read terminal output state.", err)
	}
	// Restore both states even when native setup changes one side before failing.
	defer func() {
		if err := errors.Join(term.Restore(input.Fd(), inputState), term.Restore(output.Fd(), outputState)); err != nil {
			resultErr = errors.Join(resultErr, workspaceFailure("Could not restore terminal state.", err))
		}
	}()
	console := uv.NewConsole(input, output, os.Environ())
	if _, err := console.MakeRaw(); err != nil {
		return workspaceFailure("Could not enable terminal input.", err)
	}
	painter := cliterminal.NewPainter(terminal, cancel)
	defer painter.Stop()
	model := newWorkspace(ctx, service, errorMessage)
	model.width, model.height = width, height
	model.dark = cliterminal.DarkBackground(os.Getenv("COLORFGBG"))
	profile := cliterminal.ColorProfile(output, os.Environ(), term.IsTerminal(output.Fd()))
	model.color = profile >= colorprofile.ANSI
	inline := &workspaceInline{model: model, painter: painter, output: output, profile: profile, cancel: cancel}
	program := tea.NewProgram(inline, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(io.Discard),
		tea.WithoutSignalHandler(), tea.WithoutRenderer(), tea.WithWindowSize(width, height), tea.WithColorProfile(profile))
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
					program.Send(workspaceSizeError{})
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
	// Capture cancellation before normal shutdown cancels the worker context.
	contextErr := ctx.Err()
	model.shutdown(cancel)
	<-listenerDone
	writeErr := painter.Stop()
	cleanupErr := cliterminal.CloseFrame(terminal, inline.close(), model.exitCode != 0 || contextErr != nil)
	resultErr = errors.Join(model.lastErr, runErr, contextErr)
	if err := errors.Join(writeErr, inline.err, cleanupErr); err != nil {
		resultErr = errors.Join(resultErr, workspaceFailure("Could not finish the vector store display.", err))
	}
	if model.exitCode != 0 {
		resultErr = errors.Join(cli.Exit("", model.exitCode), resultErr)
	}
	return resultErr
}

type workspaceSignalError struct{ code int }

func (e *workspaceSignalError) Error() string        { return "vector store workspace interrupted" }
func (e *workspaceSignalError) Is(target error) bool { return target == context.Canceled }

func workspaceSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case received := <-signals:
			code := 130
			switch received {
			case syscall.SIGTERM:
				code = 143
			case syscall.SIGHUP:
				code = 129
			}
			cancel(&workspaceSignalError{code: code})
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		signal.Stop(signals)
		cancel(nil)
		<-done
	}
}

type workspaceWrapTimeout struct{}
type workspaceSizeError struct{}

// The workspace owns only its inline frame, paste mode, cursor, and wrapping.
type workspaceInline struct {
	model                             *workspace
	painter                           cliterminal.FramePainter
	output                            *os.File
	profile                           colorprofile.Profile
	cancel                            context.CancelFunc
	started, modesActive, restoreWrap bool
	content                           string
	width, height                     int
	err                               error
}

func (p *workspaceInline) Init() tea.Cmd {
	p.modesActive = true
	query := ansi.ResetModeTextCursorEnable + ansi.SetModeBracketedPaste + ansi.RequestModeAutoWrap
	if p.model.color {
		query += ansi.RequestBackgroundColor
	}
	p.painter.Control(query)
	return tea.Batch(p.model.Init(), tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return workspaceWrapTimeout{} }))
}

func (p *workspaceInline) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case workspaceSizeError:
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
	case workspaceWrapTimeout:
		p.start()
	}
	if key, ok := message.(tea.KeyPressMsg); !p.started && ok && key.String() != "ctrl+c" {
		return p, nil
	}
	// A queued resize must not submit a control that is no longer visible.
	if _, key := message.(tea.KeyPressMsg); key {
		if !p.resize() {
			return p, tea.Quit
		}
	}
	_, command := p.model.Update(message)
	if p.started {
		p.draw()
	}
	return p, command
}

func (p *workspaceInline) View() tea.View { return tea.NewView("") }

func (p *workspaceInline) start() {
	if !p.started && p.err == nil {
		p.started = true
		p.painter.Control(ansi.SetModeAutoWrap)
	}
}

func (p *workspaceInline) resize() bool {
	w, h, err := term.GetSize(p.output.Fd())
	if err != nil {
		p.err = errors.New("could not read terminal size")
		return false
	}
	if w != p.model.width || h != p.model.height {
		p.model.Update(tea.WindowSizeMsg{Width: w, Height: h})
	}
	return true
}

func (p *workspaceInline) draw() {
	if !p.resize() {
		p.cancel()
		return
	}
	var converted strings.Builder
	_, _ = io.WriteString(&colorprofile.Writer{Forward: &converted, Profile: max(p.profile, colorprofile.ASCII)}, p.model.View().Content)
	content := converted.String()
	w, h := p.model.width, p.model.height
	if content == p.content && w == p.width && h == p.height {
		return
	}
	p.painter.Frame(cliterminal.InlineFrame(content, w))
	p.content, p.width, p.height = content, w, h
}

func (p *workspaceInline) close() string {
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
