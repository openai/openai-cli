package clihelp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// RunWithPager keeps bare interactive help at its first line. Explicit help,
// nonterminal streams, and CI retain the framework's ordinary output path.
func RunWithPager(ctx context.Context, root *cli.Command, argv []string, run func() error) (returnErr error) {
	input, output := root.Reader, root.Writer
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stdout
	}
	in, inputFile := input.(*os.File)
	out, outputFile := output.(*os.File)
	if len(argv) != 1 || argv[0] == "" || !inputFile || !outputFile ||
		!term.IsTerminal(in.Fd()) || !term.IsTerminal(out.Fd()) || !term.IsTerminal(os.Stderr.Fd()) ||
		!welcomeEnvironment(os.Getenv, runtime.GOOS) {
		return run()
	}
	if runtime.GOOS == "windows" && os.Getenv("TERM") == "" && os.Getenv("WT_SESSION") == "" {
		// Preserve escape-free, noninteractive help in classic Windows consoles.
		return run()
	}
	width, height, err := term.GetSize(out.Fd())
	if err != nil || width < 2 || height < 3 {
		return run()
	}
	// Keep the real terminal identity while the framework parses flags and renders
	// its complete help. This preserves the banner, colors, and insertion links.
	var captured bytes.Buffer
	previous := root.Writer
	root.Writer = terminal.BorrowedWriter{Writer: &captured, File: out}
	defer func() { root.Writer = previous }()
	err = run()
	root.Writer = previous
	format := root.String("format")
	if err != nil || format != "" && !strings.EqualFold(format, "auto") && !strings.EqualFold(format, "text") {
		_, writeErr := (clioutput.Writer{Context: ctx, Sink: out}).Write(captured.Bytes())
		return errors.Join(err, writeErr)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	invocation, _ := root.Metadata["help-invocation"].(string)
	m := newHelpPager(captured.String(), width, height, func(width int) string {
		return commandHelpAtWidths(root, invocation, "", min(width, 79), min(width, 100))
	})
	programContext, cancel := context.WithCancel(ctx)
	defer cancel()
	tracked := &helpPagerOutput{BorrowedWriter: terminal.BorrowedWriter{
		Writer: clioutput.Writer{Context: context.Background(), Sink: out}, File: out,
	}, cancel: cancel}
	// Tea can return during startup after changing terminal modes but before
	// installing its cleanup. Preserve both borrowed terminals independently.
	inputState, err := term.GetState(in.Fd())
	if err != nil {
		return &PagerError{Cause: err}
	}
	outputState, err := term.GetState(out.Fd())
	if err != nil {
		return &PagerError{Cause: err}
	}
	// Tea treats SIGTERM as a successful quit. Keep signal exit statuses while
	// allowing cancellation to finish terminal cleanup before releasing signals.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	var signalCode atomic.Int32
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		select {
		case received := <-signals:
			code := int32(130)
			if received == syscall.SIGTERM {
				code = 143
			}
			signalCode.Store(code)
			cancel()
		case <-programContext.Done():
		}
	}()
	defer func() {
		signal.Stop(signals)
		cancel()
		<-listenerDone
	}()
	defer func() {
		if restoreErr := errors.Join(term.Restore(in.Fd(), inputState), term.Restore(out.Fd(), outputState)); restoreErr != nil {
			returnErr = errors.Join(returnErr, &PagerError{Cause: restoreErr})
		}
	}()
	_, err = tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(tracked), tea.WithContext(programContext),
		tea.WithWindowSize(width, height), tea.WithColorProfile(commandHelpStyle(root).profile), tea.WithoutSignalHandler()).Run()
	if writeErr := tracked.failure(); writeErr != nil {
		return writeErr
	}
	if code := signalCode.Load(); code != 0 {
		return cli.Exit(errors.Join(context.Canceled, err), int(code))
	}
	if m.interrupted || errors.Is(err, tea.ErrInterrupted) {
		return cli.Exit(errors.Join(context.Canceled, err), 130)
	}
	if err != nil {
		return &PagerError{Cause: err}
	}
	return nil
}

// PagerError preserves the cause while exposing only safe recovery guidance.
type PagerError struct{ Cause error }

func (e *PagerError) Error() string {
	return "Could not display help. Run openai --help for plain output."
}

func (e *PagerError) Unwrap() error { return e.Cause }

type helpPager struct {
	viewport    viewport.Model
	render      func(int) string
	height      int
	interrupted bool
}

func newHelpPager(content string, width, height int, render func(int) string) *helpPager {
	m := &helpPager{viewport: viewport.New(viewport.WithWidth(width), viewport.WithHeight(height-1)), render: render, height: height}
	m.viewport.SoftWrap = false
	m.viewport.SetContent(strings.TrimRight(ansi.Hardwrap(content, width, true), "\n"))
	m.viewport.GotoTop()
	return m
}

func (m *helpPager) Init() tea.Cmd { return nil }

func (m *helpPager) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		width, height := max(1, msg.Width), max(1, msg.Height-1)
		m.height = max(1, msg.Height)
		offset := m.viewport.YOffset()
		m.viewport.SetWidth(width)
		m.viewport.SetHeight(height)
		m.viewport.SetContent(strings.TrimRight(ansi.Hardwrap(m.render(width), width, true), "\n"))
		m.viewport.SetYOffset(offset)
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "enter":
			return m, tea.Quit
		case "ctrl+c":
			m.interrupted = true
			return m, tea.Quit
		case "home", "g":
			m.viewport.GotoTop()
			return m, nil
		case "end", "G":
			m.viewport.GotoBottom()
			return m, nil
		}
	}
	var command tea.Cmd
	m.viewport, command = m.viewport.Update(message)
	return m, command
}

func (m *helpPager) View() tea.View {
	footer := "Up/Down: scroll   Space: page   q: quit"
	if m.viewport.Width() < len(footer) {
		footer = "Space: page   q: quit"
	}
	content := ansi.Truncate(footer, m.viewport.Width(), "")
	if m.height > 1 {
		content = m.viewport.View() + "\n" + content
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

// Tea does not return renderer failures. Stop input immediately on a write
// failure, but let restoration writes use the still-open borrowed terminal.
type helpPagerOutput struct {
	terminal.BorrowedWriter
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error
}

func (w *helpPagerOutput) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.mu.Lock()
		if w.err == nil {
			w.err = err
		}
		w.mu.Unlock()
		w.cancel()
	}
	return n, err
}

func (w *helpPagerOutput) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}
