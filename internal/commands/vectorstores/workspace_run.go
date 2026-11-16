package vectorstores

import (
	"bytes"
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

func runWorkspace(parent context.Context, service Service, errorMessage func(error) string, input, output *os.File, fullScreen bool) (resultErr error) {
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
	screen := &workspaceScreenOutput{FrameOutput: terminal}
	painter := cliterminal.NewPainter(screen, cancel)
	defer painter.Stop()
	model := newWorkspace(ctx, service, errorMessage)
	model.fullScreen = fullScreen
	model.decLineDrawing = cliterminal.SupportsDECLineDrawing()
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
	cleanupErr := cliterminal.CloseFrame(terminal, inline.close(screen.mayBeAlternate), model.exitCode != 0 || contextErr != nil)
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

// Painter.Stop can discard queued frames. Only restore a buffer that the
// terminal might have entered, including a partially acknowledged write.
type workspaceScreenOutput struct {
	cliterminal.FrameOutput
	mayBeAlternate bool
}

func (o *workspaceScreenOutput) WriteContext(ctx context.Context, frame []byte) (int, error) {
	// Mode switches precede the frame. Never inspect its displayed text.
	controls, _, _ := bytes.Cut(frame, []byte{'\r'})
	enter := bytes.LastIndex(controls, []byte(ansi.SetModeAltScreenSaveCursor))
	leave := bytes.LastIndex(controls, []byte(ansi.ResetModeAltScreenSaveCursor))
	if enter >= 0 {
		o.mayBeAlternate = true
	}
	n, err := o.FrameOutput.WriteContext(ctx, frame)
	if leave > enter && err == nil && n == len(frame) {
		o.mayBeAlternate = false
	}
	return n, err
}

type workspaceInline struct {
	model                             *workspace
	painter                           cliterminal.FramePainter
	output                            *os.File
	profile                           colorprofile.Profile
	cancel                            context.CancelFunc
	started, modesActive, restoreWrap bool
	fullScreen, alternateUnavailable  bool
	receivedScreenReport              bool
	content                           string
	width, height                     int
	err                               error
}

func (p *workspaceInline) Init() tea.Cmd {
	p.modesActive = true
	query := ansi.ResetModeTextCursorEnable + ansi.SetModeBracketedPaste + ansi.RequestModeAltScreenSaveCursor + ansi.RequestModeAutoWrap
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
		if msg.Mode == ansi.ModeAltScreenSaveCursor && !p.receivedScreenReport {
			p.receivedScreenReport = true
			// Leave another application's alternate screen under its owner's control.
			p.alternateUnavailable = msg.Value.IsSet() || msg.Value == ansi.ModePermanentlyReset
			if p.alternateUnavailable {
				p.fullScreen = false
			}
		}
		if msg.Mode == ansi.ModeAutoWrap {
			if msg.Value == ansi.ModePermanentlyReset {
				p.err = errors.New("terminal wrapping is unavailable")
				return p, tea.Quit
			}
			p.restoreWrap = msg.Value == ansi.ModeReset
			// When starting expanded, give the alternate-screen query its startup window.
			if !p.model.fullScreen || p.receivedScreenReport {
				p.start()
			}
		}
	case workspaceWrapTimeout:
		p.start()
	}
	if key, ok := message.(tea.KeyPressMsg); !p.started && ok && key.String() != "ctrl+c" && key.Keystroke() != zoomKey {
		return p, nil
	}
	// A queued resize must not submit a control that is no longer visible.
	if _, key := message.(tea.KeyPressMsg); key {
		if !p.resize() {
			return p, tea.Quit
		}
	}
	_, command := p.model.Update(message)
	if p.model.fullScreen && p.alternateUnavailable {
		p.model.fullScreen = false
		p.model.note = "Full screen is unavailable here. Open vector stores in a new terminal tab."
		p.model.reflow()
	}
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
	if content == p.content && w == p.width && h == p.height && p.fullScreen == p.model.fullScreen {
		return
	}
	p.paintFrame(content, w)
	p.content, p.width, p.height = content, w, h
}

func (p *workspaceInline) paintFrame(content string, width int) {
	if p.model.fullScreen != p.fullScreen {
		// Drop frames queued for the previous buffer before queuing its switch.
		p.painter.Frame("")
		mode := ansi.ResetModeAltScreenSaveCursor
		if p.model.fullScreen {
			mode = ansi.SetModeAltScreenSaveCursor
		}
		p.painter.Control(mode)
		p.fullScreen = p.model.fullScreen
	}
	frame := cliterminal.InlineFrame(content, width)
	if p.fullScreen {
		frame = ansi.CursorHomePosition + frame
	}
	p.painter.Frame(frame)
}

func (p *workspaceInline) close(alternate bool) string {
	if !p.modesActive {
		return ""
	}
	cleanup := "\x18\x1b\\" + ansi.ResetStyle
	if p.model != nil && p.model.decLineDrawing {
		// A partial frame can stop between the character-set shifts.
		cleanup += ansi.SelectCharacterSet('(', 'B')
	}
	if alternate && !p.alternateUnavailable {
		cleanup += ansi.ResetModeAltScreenSaveCursor
	}
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
