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
	"github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/tidwall/gjson"
)

// ControlsHelp describes the shared terminal interaction in command help.
const ControlsHelp = "Terminal lists start with one screen of table rows or labeled fields. Complete results that fit return to the shell. Space shows unread rows first. After the last loaded row, Space requests one more API page if available. Use the arrow keys to scroll one row, b to go back, or q to quit. Use --pager to open the full-screen viewer immediately. In the viewer, press i to print loaded rows and continue in scrollback with Space or q."

// Page contains loaded records and whether another page can be requested.
type Page struct {
	Items      []gjson.Result
	More       bool
	Notice     string // optional catalog guidance, including a source-specific empty result
	FullLabels bool   // an ambiguous source page needs the complete original records
	Total      int    // known record count, when established by the source
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
	total    int
	loaded   bool
	loading  bool
	quitting bool
	pending  []string
	records  recordViewer
	inline   *inlineList
	err      error

	// Fetches start in the event loop. Shutdown cancels and joins the one worker,
	// including when quitting races with a completed request failure.
	work     sync.WaitGroup
	mu       sync.Mutex
	fetchErr error
}

// Run prints pages to scrollback or opens the interactive record viewer.
func Run(opts Options, fetch func() (Page, error), cancel context.CancelFunc) error {
	width, height, err := term.GetSize(opts.Stdout.(*os.File).Fd())
	if err != nil {
		return err
	}
	if opts.PrintOnly {
		defer cancel()
		return printPages(opts, width, fetch)
	}
	m := New(opts, fetch, cancel, width, height)
	for {
		outputErr, runErr, signaled := runPrompt(m, opts, width, height)
		if signaled {
			m.pending = nil
			m.quitting = true
		}
		if m.quitting || outputErr != nil || runErr != nil || opts.Context.Err() != nil {
			return m.Finish(outputErr, runErr)
		}
		width, height, err = term.GetSize(opts.Stdout.(*os.File).Fd())
		if err != nil {
			return m.Finish(nil, err)
		}
		m.window = tea.WindowSizeMsg{Width: width, Height: height}
		switch {
		case m.records.mode == recordOpening:
			continue
		case m.records.mode == recordBrowser && m.inline != nil && !m.loading:
			err = m.printLoaded()
		case m.records.mode == recordInline && len(m.pending) > 0:
			err = m.printPending()
		default:
			return m.Finish(nil, nil)
		}
		if err != nil || m.err != nil || !m.more && len(m.inline.lines) == 0 {
			return m.Finish(err, nil)
		}
	}
}

func runPrompt(m *Model, opts Options, width, height int) (error, error, bool) {
	programContext, stop := context.WithCancel(opts.Context)
	defer stop()
	out := &listNavigationOutput{TerminalWriter: clioutput.TerminalWriter{
		// Tea must still restore terminal modes after request/program cancellation.
		Writer: clioutput.Writer{Context: context.Background(), Sink: opts.Stdout}, File: opts.Stdout.(*os.File),
	}, stop: stop}
	var model tea.Model = m
	var output io.Writer = out
	var preview *recordPreviewModel
	if m.records.mode == recordPreview || m.records.mode == recordInline {
		preview = &recordPreviewModel{model: m, out: out, width: width, height: height}
		model = preview
		// Tea owns native input modes, resize notifications and key decoding.
		// Only the inline renderer writes visible output in this phase.
		output = terminal.BorrowedWriter{Writer: io.Discard, File: opts.Stdout.(*os.File)}
	}
	program := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(output),
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
	if preview != nil {
		preview.close()
	}
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
	return errors.Join(m.err, fetchErr, outputErr, runErr, m.printPending())
}

// Print short complete lists or received records after a later page fails.
// Remove pending ownership before writing, so partial output cannot be retried.
func (m *Model) printPending() error {
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
	if m.records.mode == recordOpening {
		m.records.mode = recordBrowser
		return m.advanceRecords(true, 1)
	}
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
		previous := m.window
		m.window = msg
		if previous == msg || m.records.mode == recordInline {
			return m, nil
		}
		return m, m.resizeRecords(previous.Width != msg.Width)
	case listPageMessage:
		m.loading, m.loaded, m.more = false, true, msg.page.More
		if msg.page.Total > 0 {
			m.total = msg.page.Total
		}
		m.err = errors.Join(m.err, msg.err)
		if m.records.mode == recordInline {
			return m, m.receiveInline(msg.page)
		}
		return m, m.receiveRecords(msg.page)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.pending = nil
			if msg.String() == "ctrl+c" && m.opts.Interrupt != nil {
				m.err = errors.Join(m.err, m.opts.Interrupt(context.Canceled))
			}
			return m, m.quit()
		}
		if m.records.mode == recordInline {
			if msg.String() == "space" && !m.loading && len(m.pending) == 0 {
				return m, m.advanceInline()
			}
			return m, nil
		}
		return m, m.recordKey(msg)
	}
	return m, nil
}

func (m *Model) View() tea.View {
	view := tea.NewView("")
	// Keep the alternate screen stable until Tea restores the terminal.
	view.AltScreen = m.records.mode == recordBrowser
	if m.records.mode == recordInline {
		if !m.quitting && len(m.pending) == 0 {
			view.Content = m.inlinePrompt()
		}
	} else if !m.quitting && m.records.mode != recordOpening {
		view.Content = m.recordView()
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
// Every viewer requires Context, RenderTable, RenderRecordLabels and RenderPage.
// RenderPage formats static output and received pages after a failure.
// Run requires a terminal file for Stdout. New permits buffered output for an embedded event loop.
type Options struct {
	Pager      bool // start directly in the full-screen viewer
	PrintOnly  bool // write all pages to scrollback without reading keys
	Context    context.Context
	Stdout     io.Writer
	Style      Style
	RenderPage func([]gjson.Result, int) (string, error)
	Interrupt  func(error) error
	// Record navigation renders a compact table with a pinned header. If the
	// data or terminal width needs labels, it renders one complete item.
	// ProjectPrintTable supplies raw columns to the multi-page scrollback layout.
	ProjectPrintTable  func([]gjson.Result) (transform.ListTableProjection, error)
	RenderTable        func([]gjson.Result, int) (string, bool, error)
	Singular, Plural   string
	Hint               string
	RenderRecordLabels func(item gjson.Result, full bool) (string, error)
	// The caller cancels Context on process signals and retains their exit status.
	CallerHandlesSignals bool
}

// New constructs the loaded-page event model without taking terminal ownership.
// The caller must call Finish after its event loop restores the terminal.
func New(opts Options, fetch func() (Page, error), cancel context.CancelFunc, width, height int) *Model {
	if opts.Singular == "" || opts.Plural == "" {
		opts.Singular, opts.Plural = "Record", "Records"
	}
	mode := recordPreview
	if opts.Pager {
		mode = recordBrowser
	}
	return &Model{opts: opts, fetch: fetch, cancel: cancel, records: recordViewer{mode: mode},
		window: tea.WindowSizeMsg{Width: width, Height: height}}
}
