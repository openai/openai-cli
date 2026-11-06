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

const helpLinkPrefix = "\x1b]8;;"
const endHelpLink = helpLinkPrefix + "\x1b\\"

type helpPager struct {
	viewport viewport.Model
	content  string
	// Source byte offsets at each physical row, including only authored newlines.
	rowEnds                          []int
	printed                          int
	output                           io.Writer
	terminal                         *os.File
	height                           int
	active, reviewing, footerVisible bool
	quitting, finished, interrupted  bool
	writeErr                         error
}

func newHelpPager(content string, width, height int) *helpPager {
	m := &helpPager{
		viewport: viewport.New(viewport.WithWidth(width), viewport.WithHeight(height-1)),
		content:  content, output: io.Discard, height: height,
	}
	m.wrap(width)
	return m
}

func (m *helpPager) wrap(width int) {
	m.viewport.SetWidth(width)
	m.rowEnds = m.rowEnds[:0]
	var screenRows []string
	offset := 0
	for _, line := range strings.Split(strings.TrimRight(m.content, "\n"), "\n") {
		wrapped := strings.Split(ansi.Hardwrap(line, width, true), "\n")
		for i, row := range wrapped {
			offset += len(row)
			end := offset
			if i+1 < len(wrapped) {
				// Let the first grapheme of the next row trigger native soft wrap.
				// The cursor then rests inside that row, rather than at a pending
				// right-margin wrap that a switch of screen buffers can discard.
				next, _ := ansi.FirstGraphemeCluster(wrapped[i+1], ansi.GraphemeWidth)
				end += len(next)
			} else if offset < len(m.content) {
				offset++ // The source line ends here, even if it wraps on screen.
				end = offset
			}
			screenRows = append(screenRows, row)
			m.rowEnds = append(m.rowEnds, end)
		}
	}
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
			m.viewport.SetYOffset(max(offset, sort.SearchInts(m.rowEnds, m.printed)+1-m.viewport.Height()))
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
	if !m.active {
		m.active = true
		frame.WriteString("\x1b[?25l\x1b[?1000h\x1b[?1006h")
	}
	end := min(m.viewport.YOffset()+m.viewport.Height(), len(m.rowEnds))
	limit := m.rowEnds[end-1]
	if m.finished {
		limit = len(m.content)
	}
	if limit < m.printed {
		// Inspect older rows without overwriting or duplicating native history.
		if !m.reviewing {
			frame.WriteString("\x1b[?1049h")
			m.reviewing = true
		}
		frame.WriteString(endHelpLink + "\x1b[0m\x1b[H\x1b[2J")
		frame.WriteString(strings.ReplaceAll(m.View().Content, "\n", "\r\n"))
	} else {
		if m.reviewing {
			m.reviewing = false
			// The terminal associates insertion links with the active buffer.
			// Close any review link before returning to the primary screen.
			frame.WriteString(endHelpLink + "\x1b[?1049l")
			if !m.footerVisible {
				prefix := m.content[:m.printed]
				if i := strings.LastIndex(prefix, helpLinkPrefix); i >= 0 {
					if url, _, ok := strings.Cut(prefix[i+len(helpLinkPrefix):], "\x1b\\"); ok && url != "" {
						frame.WriteString(helpLinkPrefix + url + "\x1b\\")
					}
				}
			}
		}
		if m.footerVisible {
			frame.WriteString("\r\x1b[2K")
		}
		frame.WriteString(strings.ReplaceAll(m.content[m.printed:limit], "\n", "\r\n"))
		m.printed = limit
		// A footer needs its own physical row. Never insert it inside a
		// soft-wrapped shell command, where it would change what users copy.
		m.footerVisible = !m.finished && limit > 0 && m.content[limit-1] == '\n'
		if m.footerVisible {
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
		reset.WriteString(endHelpLink + "\x1b[?1049l")
	}
	if m.footerVisible {
		reset.WriteString("\r\x1b[2K")
	} else if !m.finished {
		// A partial styled command may still have an open insertion link.
		// Its closing sequences live in the unread suffix of the source.
		reset.WriteString(endHelpLink + "\x1b[0m\r\n")
	}
	reset.WriteString("\x1b[?1006l\x1b[?1000l\x1b[?25h")
	_, err := io.WriteString(m.output, reset.String())
	return err
}
