package clihelp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// RunWithPager keeps bare interactive help at its first line. Explicit help,
// nonterminal streams, and CI retain the framework's ordinary output path.
func RunWithPager(ctx context.Context, root *cli.Command, argv []string, run func() error) error {
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
	m := newHelpPager(captured.String(), width, height)
	if m.viewport.TotalLineCount() <= height {
		_, err := (clioutput.Writer{Context: ctx, Sink: out}).Write(captured.Bytes())
		return err
	}
	return m.run(ctx, in, out, width, height, root)
}

func (m *helpPager) run(ctx context.Context, in, out *os.File, width, height int, root *cli.Command) (returnErr error) {
	programContext, cancel := context.WithCancel(ctx)
	defer cancel()
	// Console setup may change one borrowed descriptor before the other fails.
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
	if _, err := uv.NewConsole(in, out, os.Environ()).MakeRaw(); err != nil {
		return &PagerError{Cause: err}
	}
	m.output, m.terminal = clioutput.Writer{Context: context.Background(), Sink: out}, out
	// Tea decodes input. Writing complete rows ourselves keeps each one in
	// native scrollback instead of repainting a full screen on every Down.
	_, err = tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithContext(programContext),
		tea.WithWindowSize(width, height), tea.WithColorProfile(commandHelpStyle(root).profile), tea.WithoutSignalHandler()).Run()
	if writeErr := errors.Join(m.writeErr, m.close()); writeErr != nil {
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
	viewport viewport.Model
	lines    []string
	// One end offset per original line, measured in screen rows at the current width.
	lineEnds                        []int
	printed                         int
	trailing                        int
	output                          io.Writer
	terminal                        *os.File
	height                          int
	active, reviewing               bool
	quitting, finished, interrupted bool
	writeErr                        error
}

func newHelpPager(content string, width, height int) *helpPager {
	body := strings.TrimRight(content, "\n")
	m := &helpPager{
		viewport: viewport.New(viewport.WithWidth(width), viewport.WithHeight(height-1)),
		lines:    strings.Split(body, "\n"), trailing: len(content) - len(body),
		output: io.Discard, height: height,
	}
	m.wrap(width)
	return m
}

func (m *helpPager) wrap(width int) {
	m.viewport.SetWidth(width)
	m.lineEnds = m.lineEnds[:0]
	var screenRows, lines []string
	for i, line := range m.lines {
		wrapped := strings.Split(ansi.Hardwrap(line, width, true), "\n")
		if i < m.printed {
			// The terminal reflows these rows itself. Keep their published
			// identities when the number of physical rows changes.
			lines = append(lines, line)
			screenRows = append(screenRows, wrapped...)
			m.lineEnds = append(m.lineEnds, len(screenRows))
			continue
		}
		// An unread launcher path can wrap across several screens. Publish
		// each new physical row only when the reader reaches it.
		for _, row := range wrapped {
			lines = append(lines, row)
			screenRows = append(screenRows, row)
			m.lineEnds = append(m.lineEnds, len(screenRows))
		}
	}
	m.lines = lines
	m.viewport.SetContentLines(screenRows)
}

type pollHelpSize struct{}

func nextHelpSize() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return pollHelpSize{} })
}

func (m *helpPager) Init() tea.Cmd { return nextHelpSize() }

func (m *helpPager) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, tea.Quit
	}
	down := false
	switch msg := message.(type) {
	case pollHelpSize:
		width, height, err := term.GetSize(m.terminal.Fd())
		if err == nil && width > 0 && height > 0 && (width != m.viewport.Width() || height != m.height) {
			if _, cmd := m.Update(tea.WindowSizeMsg{Width: width, Height: height}); cmd != nil {
				return m, cmd
			}
		}
		return m, nextHelpSize()
	case tea.WindowSizeMsg:
		width, height := max(1, msg.Width), max(1, msg.Height)
		offset := m.viewport.YOffset()
		if width != m.viewport.Width() {
			m.wrap(width)
		}
		m.height = height
		m.viewport.SetHeight(max(1, height-1))
		m.viewport.SetYOffset(offset)
		if !m.reviewing && m.printed > 0 {
			// The terminal reflows previously printed rows in place. Keep our
			// forward position aligned with the end of that same printed prefix.
			m.viewport.SetYOffset(max(offset, m.lineEnds[m.printed-1]-m.viewport.Height()))
		}
		m.finished = m.viewport.TotalLineCount() <= height || offset > 0 && m.viewport.AtBottom()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "Q", "esc":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+c":
			m.quitting, m.interrupted = true, true
			return m, tea.Quit
		case "home", "g":
			m.viewport.GotoTop()
		case "end", "G":
			m.viewport.GotoBottom()
			m.finished = true
		default:
			keys := m.viewport.KeyMap
			down = key.Matches(msg, keys.Down, keys.PageDown, keys.HalfPageDown)
			m.viewport, _ = m.viewport.Update(msg)
		}
	case tea.MouseWheelMsg:
		down = msg.Button == tea.MouseWheelDown && !msg.Mod.Contains(tea.ModShift)
		m.viewport, _ = m.viewport.Update(msg)
	default:
		return m, nil
	}
	m.finished = m.finished || down && m.viewport.AtBottom()
	m.writeErr = m.draw()
	if m.finished || m.writeErr != nil {
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *helpPager) footer() string {
	footer := "Scroll or use arrows   Space: page   q: quit"
	if m.viewport.Width() < len(footer) {
		footer = "Scroll   q: quit"
	}
	return ansi.Truncate(footer, m.viewport.Width(), "")
}

func (m *helpPager) View() tea.View {
	if m.height == 1 {
		return tea.NewView(m.footer())
	}
	return tea.NewView(m.viewport.View() + "\n" + m.footer())
}

func (m *helpPager) draw() error {
	var frame strings.Builder
	hadFooter := m.active
	if !m.active {
		m.active = true
		frame.WriteString("\x1b[?25l\x1b[?1000h\x1b[?1006h")
	}
	end := m.viewport.YOffset() + m.viewport.Height()
	if m.printed > 0 && end < m.lineEnds[m.printed-1] && !m.finished {
		// Inspect older rows without overwriting or duplicating native history.
		if !m.reviewing {
			frame.WriteString("\x1b[?1049h")
			m.reviewing = true
		}
		frame.WriteString("\x1b[H\x1b[2J")
		frame.WriteString(strings.ReplaceAll(m.View().Content, "\n", "\r\n"))
	} else {
		if m.reviewing {
			m.reviewing = false
			frame.WriteString("\x1b[?1049l")
		}
		if hadFooter {
			frame.WriteString("\r\x1b[2K")
		}
		limit := min(len(m.lines), sort.SearchInts(m.lineEnds, end)+1)
		if m.finished {
			limit = len(m.lines)
		}
		for m.printed < limit {
			frame.WriteString(m.lines[m.printed])
			frame.WriteString("\r\n")
			m.printed++
		}
		if m.finished {
			frame.WriteString(strings.Repeat("\r\n", max(0, m.trailing-1)))
		} else {
			frame.WriteString(m.footer())
		}
	}
	_, err := io.WriteString(m.output, frame.String())
	return err
}

func (m *helpPager) close() error {
	if !m.active {
		return nil
	}
	var reset strings.Builder
	if m.reviewing {
		reset.WriteString("\x1b[?1049l")
	}
	if !m.finished {
		reset.WriteString("\r\x1b[2K")
	}
	reset.WriteString("\x1b[?1006l\x1b[?1000l\x1b[?25h")
	_, err := io.WriteString(m.output, reset.String())
	return err
}
