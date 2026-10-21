// Package listview provides lazy terminal list navigation and loaded-page rendering.
package listview

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/tidwall/gjson"
)

// JSONListPage is implemented by the SDK's explicit API page types.
type JSONListPage[P any] interface {
	comparable
	RawJSON() string
	GetNextPage() (P, error)
}

// ShowPages requests pages only when the viewer needs them. The first
// request receives the same cancelable context as all subsequent SDK requests.
// cursorField is empty for item-ID cursors, or the SDK's envelope cursor field.
func ShowPages[P JSONListPage[P]](first func(context.Context) (P, error), itemsToDisplay int64, opts Options, cursorField string) error {
	if err := opts.Context.Err(); err != nil {
		return err
	}
	if itemsToDisplay == 0 {
		// Existing SDK auto-pagers make their first request before applying the
		// output limit. Preserve its errors even when no items will be printed.
		_, err := first(opts.Context)
		return err
	}
	ctx, cancel := context.WithCancel(opts.Context)
	defer cancel()
	return Run(opts, newListPageFetcher(ctx, first, itemsToDisplay, cursorField), cancel)
}

func newListPageFetcher[P JSONListPage[P]](ctx context.Context, first func(context.Context) (P, error), itemsToDisplay int64, cursorField string) func() (Page, error) {
	var page P
	started := false
	done := false
	seen := make(map[string]bool)
	remaining := itemsToDisplay
	return func() (Page, error) {
		if done || remaining == 0 {
			return Page{}, nil
		}
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		var err error
		if !started {
			started = true
			page, err = first(ctx)
		} else {
			page, err = page.GetNextPage()
		}
		if err != nil {
			return Page{}, err
		}
		var zero P
		if page == zero {
			done = true
			return Page{}, nil
		}
		value := gjson.Parse(page.RawJSON())
		items := value.Get("data").Array()
		if len(items) == 0 && value.Get("has_more").Bool() {
			done = true
			return Page{}, &PaginationError{}
		}
		more := len(items) != 0 && (!value.Get("has_more").Exists() || value.Get("has_more").Bool())
		cursor := value.Get(cursorField).String()
		if cursorField == "" && len(items) != 0 {
			cursor = items[len(items)-1].Get("id").String()
		}
		if remaining > 0 && int64(len(items)) >= remaining {
			items = items[:int(remaining)]
			more = false
		}
		result := Page{Items: items, More: more}
		done = !more
		if more {
			if cursor == "" || seen[cursor] {
				done = true
				result.More = false
				return result, &PaginationError{}
			}
			seen[cursor] = true
		}
		if remaining > 0 {
			remaining -= int64(len(items))
		}
		return result, ctx.Err()
	}
}

// Page contains loaded records and whether another page can be requested.
type Page struct {
	Items []gjson.Result
	More  bool
}

// PaginationError is a fixed diagnostic that never includes API cursors or response data.
type PaginationError struct{}

func (*PaginationError) Error() string {
	return "List pagination stopped because the API returned a missing or repeated cursor. Output may be incomplete."
}

type listPageMessage struct {
	page Page
	err  error
}

// Model owns navigation state and the lifetime of its single fetch worker.
type Model struct {
	opts          Options
	fetch         func() (Page, error)
	cancel        context.CancelFunc
	viewport      viewport.Model
	window        tea.WindowSizeMsg
	content       string
	contentFits   bool
	pages         []Page
	index         int
	loading       bool
	quitting      bool
	printPage     bool
	printComplete bool
	err           error

	// Fetches start in the event loop. Shutdown cancels and joins the one worker,
	// including when quitting races with a completed request failure.
	work     sync.WaitGroup
	mu       sync.Mutex
	fetchErr error
}

// Run owns the terminal event loop and restores it before final printing.
func Run(opts Options, fetch func() (Page, error), cancel context.CancelFunc) error {
	width, height, err := term.GetSize(opts.Stdout.(*os.File).Fd())
	if err != nil {
		return err
	}
	m := New(opts, fetch, cancel, width, height)
	programContext, stop := context.WithCancel(opts.Context)
	defer stop()
	out := &listNavigationOutput{TerminalWriter: clioutput.TerminalWriter{
		// Tea must still restore terminal modes after request/program cancellation.
		Writer: clioutput.Writer{Context: context.Background(), Sink: opts.Stdout}, File: opts.Stdout.(*os.File),
	}, stop: stop}
	program := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(out),
		tea.WithContext(programContext), tea.WithWindowSize(width, height), tea.WithoutSignalHandler())
	// Models can cancel the program context while the same signal reaches Tea.
	// Forward through Send, which stops when the program exits. Tea's built-in
	// signal handler can block sending after its event loop has already stopped.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	listenerDone := make(chan struct{})
	stopSignals := func() {
		signal.Stop(signals)
		stop()
		<-listenerDone
	}
	defer stopSignals()
	go func() {
		defer close(listenerDone)
		defer signal.Stop(signals)
		select {
		case <-programContext.Done():
		case received := <-signals:
			if received == os.Interrupt {
				program.Send(tea.InterruptMsg{})
			} else {
				program.Send(tea.QuitMsg{})
			}
		}
	}()
	_, runErr := program.Run()
	// Final printing has no Tea event loop. Restore signal ownership first.
	stopSignals()
	return m.Finish(out.Err(), runErr)
}

// Finish cancels and joins the worker, retains failures, and prints after restoration.
func (m *Model) Finish(outputErr, runErr error) error {
	m.cancel()
	m.work.Wait()
	m.mu.Lock()
	fetchErr := m.fetchErr
	m.mu.Unlock()
	if errors.Is(m.err, fetchErr) {
		fetchErr = nil
	}
	if outputErr != nil && m.opts.Context.Err() == nil {
		// An output failure stops Tea and cancels its pending fetch. Remove those
		// cleanup causes so they cannot hide the write failure in diagnostics.
		m.err = listWithoutCleanupCancellation(m.err)
		fetchErr = listWithoutCleanupCancellation(fetchErr)
		runErr = listWithoutCleanupCancellation(runErr)
	}
	if m.quitting && listCancellationOnly(fetchErr) {
		fetchErr = nil
	}
	if m.quitting && errors.Is(runErr, tea.ErrProgramKilled) {
		runErr = nil
	}
	if m.opts.Interrupt != nil && errors.Is(runErr, tea.ErrInterrupted) {
		// An OS interrupt can reach the viewer before the loading context.
		// Add this explicit cancellation after filtering cleanup-only causes.
		runErr = errors.Join(runErr, m.opts.Interrupt(context.Canceled))
	}
	var printErr error
	if m.printPage || m.printComplete {
		// Print only after the worker stops and Tea restores the terminal.
		// Automatic completion retains the selected table or labeled format.
		content := m.content
		if m.printPage {
			content, printErr = m.opts.RenderPrint(m.pages[m.index].Items)
		}
		if printErr == nil {
			_, printErr = (clioutput.Writer{Context: m.opts.Context, Sink: m.opts.Stdout}).WriteString(content)
		}
	}
	return errors.Join(m.err, fetchErr, outputErr, runErr, printErr)
}

// Retain real errors beside cleanup cancellation, including inside wrappers.
// Caller cancellation bypasses this filter, and timeouts remain unchanged.
func listWithoutCleanupCancellation(err error) error {
	if err == context.Canceled || err == tea.ErrProgramKilled {
		return nil
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		var causes []error
		for _, cause := range wrapped.Unwrap() {
			causes = append(causes, listWithoutCleanupCancellation(cause))
		}
		return errors.Join(causes...)
	case interface{ Unwrap() error }:
		return listWithoutCleanupCancellation(wrapped.Unwrap())
	}
	return err
}

func (m *Model) Init() tea.Cmd { return m.load() }

func (m *Model) load() tea.Cmd {
	m.loading = true
	result := make(chan listPageMessage, 1)
	m.work.Add(1)
	go func() {
		defer m.work.Done()
		page, err := m.fetch()
		m.mu.Lock()
		m.fetchErr = err
		m.mu.Unlock()
		result <- listPageMessage{page, err}
	}()
	return func() tea.Msg { return <-result }
}

func (m *Model) render() error {
	if len(m.pages) == 0 {
		return nil
	}
	content, err := m.opts.RenderPage(m.pages[m.index].Items, m.viewport.Width())
	if err == nil {
		wrapped := ansi.Hardwrap(content, max(1, m.viewport.Width()), true)
		m.viewport.SetContent(strings.TrimSuffix(wrapped, "\n"))
		m.content = ""
		m.contentFits = ContentFits(content, m.window.Width, m.window.Height)
		// Long pages only need the viewport copy. Retain the original body
		// only when it can be printed on automatic completion.
		if m.contentFits {
			m.content = content
		}
	}
	return err
}

// ContentFits counts the original output's terminal rows, leaving
// one row for the shell prompt. Hardwrap treats tabs as zero-width controls;
// terminal output instead advances to the next eight-column tab stop.
func ContentFits(content string, width, height int) bool {
	if width <= 0 || height <= 1 {
		return false
	}
	rows, column := 1, 0
	state := ansi.NormalState
	for content = strings.TrimSuffix(content, "\n"); len(content) > 0; {
		sequence, cells, n, next := ansi.DecodeSequence(content, state, nil)
		content, state = content[n:], next
		switch sequence {
		case "\n":
			rows++
			column = 0
		case "\t":
			column = min(width-1, (column/8+1)*8)
		default:
			if cells > width {
				return false
			}
			if cells > 0 && column+cells > width {
				rows++
				column = 0
			}
			column += cells
		}
		if rows >= height {
			return false
		}
	}
	return true
}

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.quitting {
		if msg, ok := message.(listPageMessage); ok && !listCancellationOnly(msg.err) {
			m.err = errors.Join(m.err, msg.err)
		}
		return m, tea.Quit
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.window = msg
		m.viewport.SetWidth(max(1, msg.Width))
		m.viewport.SetHeight(max(1, msg.Height-2))
		m.err = m.render()
		if m.err != nil {
			return m, tea.Quit
		}
		return m, nil
	case listPageMessage:
		m.loading = false
		first := len(m.pages) == 0
		if len(msg.page.Items) != 0 || len(m.pages) == 0 {
			m.pages = append(m.pages, msg.page)
			m.index = len(m.pages) - 1
			m.err = m.render()
			m.viewport.GotoTop()
		} else {
			m.pages[len(m.pages)-1].More = false
		}
		m.err = errors.Join(m.err, msg.err)
		if m.err != nil {
			return m, tea.Quit
		}
		// Only a complete first response can bypass navigation. Keep later
		// pages open so users can revisit previously loaded results.
		// Reserve one terminal row for the returned shell prompt.
		if first && !msg.page.More && m.contentFits {
			m.printComplete = true
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyPressMsg:
		printPage := msg.String() == "p" && len(m.pages) != 0
		if printPage || msg.String() == "q" || msg.String() == "ctrl+c" || msg.String() == "esc" {
			if msg.String() == "ctrl+c" && m.opts.Interrupt != nil {
				m.err = errors.Join(m.err, m.opts.Interrupt(context.Canceled))
			}
			m.printPage = printPage
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		}
		if m.loading {
			return m, nil
		}
		if key.Matches(msg, m.viewport.KeyMap.PageDown) && m.viewport.AtBottom() && len(m.pages) != 0 {
			if m.index+1 < len(m.pages) {
				m.index++
				m.err = m.render()
				m.viewport.GotoTop()
			} else if m.pages[m.index].More {
				return m, m.load()
			}
			return m, nil
		}
		if key.Matches(msg, m.viewport.KeyMap.PageUp) && m.viewport.AtTop() && m.index > 0 {
			m.index--
			m.err = m.render()
			m.viewport.GotoBottom()
			return m, nil
		}
	}
	var command tea.Cmd
	m.viewport, command = m.viewport.Update(message)
	return m, command
}

func (m *Model) View() tea.View {
	if m.printComplete {
		// The original rendered content is printed once after restoration.
		return tea.NewView("")
	}
	footer := "Space: more   q: quit"
	if m.viewport.Width() >= len("Space: more   b: back   q: quit") {
		footer = "Space: more   b: back   q: quit"
	}
	printHint := ""
	if len(m.pages) != 0 {
		printHint = "p: print page, quit"
		if m.opts.PrintHint != nil {
			printHint = m.opts.PrintHint(len(m.pages[m.index].Items), m.viewport.Width())
		}
	}
	if m.loading {
		footer = "Loading next page…   q: quit"
		if ansi.StringWidth(footer) > m.viewport.Width() {
			footer = "Loading… q: quit"
		}
	} else if len(m.pages) != 0 && m.index == len(m.pages)-1 && !m.pages[m.index].More && m.viewport.AtBottom() {
		footer = "End of results   q: quit"
		if m.viewport.Width() >= len("End of results   b: back   q: quit") {
			footer = "End of results   b: back   q: quit"
		}
		if ansi.StringWidth(footer) > m.viewport.Width() {
			footer = "End q: quit"
		}
	}
	if m.err != nil {
		footer = "List request failed."
	}
	if m.viewport.Width() < 21 {
		footer = strings.ReplaceAll(footer, "   ", " ")
	}
	return tea.NewView(m.viewport.View() + "\n" + ansi.Truncate(printHint, m.viewport.Width(), "") + "\n" + ansi.Truncate(footer, m.viewport.Width(), ""))
}

// A canceled fetch can wrap context.Canceled. Joined real failures must survive
// quitting even when one member is cancellation.
func listCancellationOnly(err error) bool {
	if err == context.Canceled {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children := wrapped.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !listCancellationOnly(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return listCancellationOnly(wrapped.Unwrap())
	}
	return false
}

type listNavigationOutput struct {
	clioutput.TerminalWriter
	stop context.CancelFunc
	mu   sync.Mutex
	err  error
}

func (w *listNavigationOutput) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if err != nil {
		w.mu.Lock()
		if w.err == nil {
			w.err = err
		}
		w.mu.Unlock()
		w.stop()
	}
	return n, err
}

func (w *listNavigationOutput) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *listNavigationOutput) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

// Options supplies presentation and feature policy for the shared viewer.
// Context and render functions must be non-nil. Stdout must be a terminal file
// when used by Run; New also permits buffered output for an embedded event loop.
type Options struct {
	Context     context.Context
	Stdout      io.Writer
	RenderPage  func([]gjson.Result, int) (string, error)
	RenderPrint func([]gjson.Result) (string, error)
	PrintHint   func(count, width int) string
	Interrupt   func(error) error
}

// New constructs the loaded-page event model without taking terminal ownership.
// The caller must call Finish after its event loop restores the terminal.
func New(opts Options, fetch func() (Page, error), cancel context.CancelFunc, width, height int) *Model {
	m := &Model{opts: opts, fetch: fetch, cancel: cancel,
		window:   tea.WindowSizeMsg{Width: width, Height: height},
		viewport: viewport.New(viewport.WithWidth(max(1, width)), viewport.WithHeight(max(1, height-2)))}
	// Wrap once in linear time. Viewport soft wrapping repeatedly scans long
	// source lines, which can block keyboard handling on large API fields.
	m.viewport.SoftWrap = false
	m.viewport.FillHeight = false
	return m
}
