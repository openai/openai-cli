// Package listview provides lazy terminal list navigation and loaded-page rendering.
package listview

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	tea "charm.land/bubbletea/v2"
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
	opts     Options
	fetch    func() (Page, error)
	cancel   context.CancelFunc
	window   tea.WindowSizeMsg
	more     bool
	loaded   bool
	loading  bool
	quitting bool
	pending  []string
	viewer   *listViewer
	err      error

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
	outputErr, runErr, signaled := runPrompt(m, opts, width, height)
	if signaled {
		m.pending = nil
	}
	return m.Finish(outputErr, runErr)
}

func runPrompt(m *Model, opts Options, width, height int) (error, error, bool) {
	programContext, stop := context.WithCancel(opts.Context)
	defer stop()
	out := &listNavigationOutput{TerminalWriter: clioutput.TerminalWriter{
		// Tea must still restore terminal modes after request/program cancellation.
		Writer: clioutput.Writer{Context: context.Background(), Sink: opts.Stdout}, File: opts.Stdout.(*os.File),
	}, stop: stop}
	program := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(out),
		tea.WithContext(programContext), tea.WithWindowSize(width, height),
		tea.WithColorProfile(opts.Style.profile), tea.WithoutSignalHandler())
	stopSignals := func() {}
	var receivedSignal os.Signal
	if !opts.CallerHandlesSignals {
		// Forward through Send, which stops when the program exits. Tea's built-in
		// signal handler can block sending after its event loop has already stopped.
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		listenerDone := make(chan struct{})
		stopSignals = func() {
			signal.Stop(signals)
			stop()
			<-listenerDone
			// Retain a signal that arrives during terminal restoration.
			if receivedSignal == nil {
				select {
				case receivedSignal = <-signals:
				default:
				}
			}
		}
		go func() {
			defer close(listenerDone)
			defer signal.Stop(signals)
			select {
			case <-programContext.Done():
			case received := <-signals:
				receivedSignal = received
				if received == os.Interrupt {
					program.Send(tea.InterruptMsg{})
				} else {
					program.Send(tea.QuitMsg{})
				}
			}
		}()
	}
	defer stopSignals()
	_, runErr := program.Run()
	stopSignals()
	// Retain an interrupt even when terminal shutdown wins the message race.
	if receivedSignal == os.Interrupt {
		runErr = errors.Join(runErr, tea.ErrInterrupted)
	}
	return out.Err(), runErr, receivedSignal != nil
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
		runErr = listWithoutCleanupCancellation(runErr)
	}
	if m.opts.Interrupt != nil && errors.Is(runErr, tea.ErrInterrupted) {
		// An OS interrupt can reach the viewer before the loading context.
		// Add this explicit cancellation after filtering cleanup-only causes.
		runErr = errors.Join(runErr, m.opts.Interrupt(context.Canceled))
	}
	if outputErr != nil || runErr != nil || m.opts.Context.Err() != nil {
		m.pending = nil
	}
	return errors.Join(m.err, fetchErr, outputErr, runErr, m.printErrorPages())
}

// Print received records after restoration when a later page fails.
// Remove pending ownership before writing, so partial output cannot be retried.
func (m *Model) printErrorPages() error {
	pages := m.pending
	m.pending = nil
	writer := clioutput.Writer{Context: m.opts.Context, Sink: m.opts.Stdout}
	for i, page := range pages {
		pages[i] = ""
		if _, err := writer.WriteString(page); err != nil {
			return err
		}
	}
	return nil
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

func (m *Model) Init() tea.Cmd {
	if m.loaded {
		return nil
	}
	return m.load()
}

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

func (m *Model) quit() tea.Cmd {
	m.quitting = true
	m.cancel()
	return tea.Quit
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
		// Reflow only the loaded page. Resizing never requests data.
		m.window = msg
		if err := m.viewer.resize(m.opts.Context, m.opts.RenderPage, msg.Width, msg.Height); err != nil {
			m.err = errors.Join(m.err, err)
			m.pending = m.viewer.takePages()
			return m, m.quit()
		}
	case listPageMessage:
		m.loading = false
		var content string
		if len(msg.page.Items) != 0 || !m.loaded && msg.err == nil {
			content, m.err = m.opts.RenderPage(msg.page.Items, max(1, m.window.Width))
		}
		m.loaded = true
		m.more = msg.page.More
		m.err = errors.Join(m.err, msg.err)
		return m, m.receiveViewerPage(msg.page.Items, content)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.pending = nil
			if msg.String() == "ctrl+c" && m.opts.Interrupt != nil {
				m.err = errors.Join(m.err, m.opts.Interrupt(context.Canceled))
			}
			return m, m.quit()
		}
		return m, m.viewerKey(msg.String())
	}
	return m, nil
}

func (m *Model) View() tea.View {
	view := tea.NewView("")
	// Keep screen ownership stable through the final frame. Tea restores the
	// original terminal before Finish prints any received records after errors.
	view.AltScreen = true
	if !m.quitting {
		view.Content = m.viewerView()
	}
	return view
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
	Context    context.Context
	Stdout     io.Writer
	Style      Style
	RenderPage func([]gjson.Result, int) (string, error)
	Interrupt  func(error) error
	// The caller cancels Context on process signals and retains their exit status.
	CallerHandlesSignals bool
}

// New constructs the loaded-page event model without taking terminal ownership.
// The caller must call Finish after its event loop restores the terminal.
func New(opts Options, fetch func() (Page, error), cancel context.CancelFunc, width, height int) *Model {
	m := &Model{opts: opts, fetch: fetch, cancel: cancel,
		window: tea.WindowSizeMsg{Width: width, Height: height}}
	m.viewer = newListViewer()
	return m
}
