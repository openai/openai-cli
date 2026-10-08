package custom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// InteractiveListOutput leaves explicit machine formats and redirected input or
// output on their existing iterator path. It never opens a controlling terminal.
func InteractiveListOutput(opts ShowJSONOpts) bool {
	// Keep the initial rollout within the public-command coverage. Other
	// generated page callbacks retain their existing iterator path.
	switch opts.Operation {
	case "(resource) files > (method) list", "(resource) batches > (method) list",
		"(resource) admin.organization.projects > (method) list":
	default:
		return false
	}
	opts.setDefaults()
	format := strings.ToLower(opts.Format)
	return (format == "" || format == "auto") && opts.OutputKind == OutputPageItem &&
		opts.Transform == "" && !opts.RawOutput && os.Getenv("CI") == "" &&
		os.Getenv("TERM") != "dumb" && isTerminal(opts.Stdout) && term.IsTerminal(os.Stdin.Fd())
}

// JSONListPage is implemented by the SDK's explicit API page types.
type JSONListPage[P any] interface {
	comparable
	RawJSON() string
	GetNextPage() (P, error)
}

// ShowJSONPages requests pages only when the viewer needs them. The first
// request receives the same cancelable context as all subsequent SDK requests.
// cursorField is empty for item-ID cursors, or the SDK's envelope cursor field.
func ShowJSONPages[P JSONListPage[P]](first func(context.Context) (P, error), itemsToDisplay int64, opts ShowJSONOpts, cursorField string) error {
	opts.setDefaults()
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
	return runListNavigation(opts, newListPageFetcher(ctx, first, itemsToDisplay, cursorField), cancel)
}

func newListPageFetcher[P JSONListPage[P]](ctx context.Context, first func(context.Context) (P, error), itemsToDisplay int64, cursorField string) func() (listNavigationPage, error) {
	var page P
	started := false
	done := false
	seen := make(map[string]bool)
	remaining := itemsToDisplay
	return func() (listNavigationPage, error) {
		if done || remaining == 0 {
			return listNavigationPage{}, nil
		}
		if err := ctx.Err(); err != nil {
			return listNavigationPage{}, err
		}
		var err error
		if !started {
			started = true
			page, err = first(ctx)
		} else {
			page, err = page.GetNextPage()
		}
		if err != nil {
			return listNavigationPage{}, err
		}
		var zero P
		if page == zero {
			done = true
			return listNavigationPage{}, nil
		}
		value := gjson.Parse(page.RawJSON())
		items := value.Get("data").Array()
		if len(items) == 0 && value.Get("has_more").Bool() {
			done = true
			return listNavigationPage{}, &listNavigationError{}
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
		result := listNavigationPage{items: items, more: more}
		done = !more
		if more {
			if cursor == "" || seen[cursor] {
				done = true
				result.more = false
				return result, &listNavigationError{}
			}
			seen[cursor] = true
		}
		if remaining > 0 {
			remaining -= int64(len(items))
		}
		return result, ctx.Err()
	}
}

type listNavigationPage struct {
	items []gjson.Result
	more  bool
}

// This fixed diagnostic never includes API cursors or response data.
type listNavigationError struct{}

func (*listNavigationError) Error() string {
	return "List pagination stopped because the API returned a missing or repeated cursor. Output may be incomplete."
}

type listPageMessage struct {
	page listNavigationPage
	err  error
}

type listNavigation struct {
	opts          ShowJSONOpts
	fetch         func() (listNavigationPage, error)
	cancel        context.CancelFunc
	viewport      viewport.Model
	window        tea.WindowSizeMsg
	content       string
	contentFits   bool
	pages         []listNavigationPage
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

func runListNavigation(opts ShowJSONOpts, fetch func() (listNavigationPage, error), cancel context.CancelFunc) error {
	width, height, err := term.GetSize(opts.Stdout.(*os.File).Fd())
	if err != nil {
		return err
	}
	m := &listNavigation{opts: opts, fetch: fetch, cancel: cancel,
		window:   tea.WindowSizeMsg{Width: width, Height: height},
		viewport: viewport.New(viewport.WithWidth(max(1, width)), viewport.WithHeight(max(1, height-2)))}
	// Wrap once in linear time. Viewport soft wrapping repeatedly scans long
	// source lines, which can block keyboard handling on large API fields.
	m.viewport.SoftWrap = false
	m.viewport.FillHeight = false
	programContext, stop := context.WithCancel(opts.Context)
	defer stop()
	out := &listNavigationOutput{terminalOutputWriter: terminalOutputWriter{
		// Tea must still restore terminal modes after request/program cancellation.
		outputWriter: outputWriter{ctx: context.Background(), out: opts.Stdout}, file: opts.Stdout.(*os.File),
	}, stop: stop}
	program := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(out),
		tea.WithContext(programContext), tea.WithWindowSize(width, height))
	_, runErr := program.Run()
	return m.finish(out, runErr)
}

func (m *listNavigation) finish(out *listNavigationOutput, runErr error) error {
	m.cancel()
	m.work.Wait()
	m.mu.Lock()
	fetchErr := m.fetchErr
	m.mu.Unlock()
	if errors.Is(m.err, fetchErr) {
		fetchErr = nil
	}
	outputErr := out.Err()
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
	var printErr error
	if m.printPage || m.printComplete {
		// Print only after the worker stops and Tea restores the terminal.
		// Automatic completion retains the selected table or labeled format.
		content := m.content
		if m.printPage {
			// Plain labels preserve full values when the user requests p.
			content, printErr = renderListNavigationLabels(m.opts, m.pages[m.index].items)
		}
		if printErr == nil {
			_, printErr = (outputWriter{ctx: m.opts.Context, out: m.opts.Stdout}).WriteString(content)
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

func (m *listNavigation) Init() tea.Cmd { return m.load() }

func (m *listNavigation) load() tea.Cmd {
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

func (m *listNavigation) render() error {
	if len(m.pages) == 0 {
		return nil
	}
	content, err := renderListNavigationPage(m.opts, m.pages[m.index].items, m.viewport.Width())
	if err == nil {
		wrapped := ansi.Hardwrap(content, max(1, m.viewport.Width()), true)
		m.viewport.SetContent(strings.TrimSuffix(wrapped, "\n"))
		m.content = ""
		m.contentFits = listNavigationContentFits(content, m.window.Width, m.window.Height)
		// Long pages only need the viewport copy. Retain the original body
		// only when it can be printed on automatic completion.
		if m.contentFits {
			m.content = content
		}
	}
	return err
}

// listNavigationContentFits counts the original output's terminal rows, leaving
// one row for the shell prompt. Hardwrap treats tabs as zero-width controls;
// terminal output instead advances to the next eight-column tab stop.
func listNavigationContentFits(content string, width, height int) bool {
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

func (m *listNavigation) Update(message tea.Msg) (tea.Model, tea.Cmd) {
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
		if len(msg.page.items) != 0 || len(m.pages) == 0 {
			m.pages = append(m.pages, msg.page)
			m.index = len(m.pages) - 1
			m.err = m.render()
			m.viewport.GotoTop()
		} else {
			m.pages[len(m.pages)-1].more = false
		}
		m.err = errors.Join(m.err, msg.err)
		if m.err != nil {
			return m, tea.Quit
		}
		// Only a complete first response can bypass navigation. Keep later
		// pages open so users can revisit previously loaded results.
		// Reserve one terminal row for the returned shell prompt.
		if first && !msg.page.more && m.contentFits {
			m.printComplete = true
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyPressMsg:
		printPage := msg.String() == "p" && len(m.pages) != 0
		if printPage || msg.String() == "q" || msg.String() == "ctrl+c" || msg.String() == "esc" {
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
			} else if m.pages[m.index].more {
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

func (m *listNavigation) View() tea.View {
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
		if m.opts.Operation == "(resource) models > (method) list" && m.opts.OutputKind == OutputPageItem {
			printHint = modelsListPrintHint(len(m.pages[m.index].items), m.viewport.Width())
		}
	}
	if m.loading {
		footer = "Loading next page…   q: quit"
		if ansi.StringWidth(footer) > m.viewport.Width() {
			footer = "Loading… q: quit"
		}
	} else if len(m.pages) != 0 && m.index == len(m.pages)-1 && !m.pages[m.index].more && m.viewport.AtBottom() {
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

// Rendering only consumes loaded items. The table integration can replace this
// bounded presentation call without owning fetch timing or keyboard input.
func renderListNavigationPage(opts ShowJSONOpts, items []gjson.Result, width int) (string, error) {
	if err := opts.Context.Err(); err != nil {
		return "", err
	}
	if (opts.Format == "" || strings.EqualFold(opts.Format, "auto")) &&
		opts.Transform == "" && !opts.RawOutput {
		if opts.Operation == "(resource) models > (method) list" && opts.OutputKind == OutputPageItem {
			content, supported, err := renderModelsListNames(opts, items, width)
			if err != nil || supported {
				return content, err
			}
		}
		content, supported, err := renderListTablePage(opts.Context, opts.Operation, items, width)
		if err != nil {
			return "", err
		}
		if err := opts.Context.Err(); err != nil {
			return "", err
		}
		if supported {
			return content, nil
		}
	}
	return renderListNavigationLabels(opts, items)
}

// Printing a page always uses complete labeled values, independent of the
// viewport's width and any compact table projection used for navigation.
func renderListNavigationLabels(opts ShowJSONOpts, items []gjson.Result) (string, error) {
	var content strings.Builder
	omitted := false
	transform := selectOutputTransformer(opts, transformers.Select)
	for i, item := range items {
		if err := opts.Context.Err(); err != nil {
			return "", err
		}
		if i != 0 {
			content.WriteByte('\n')
		}
		item, err := transformOutput(opts.Context, item, transform)
		if err != nil {
			return "", err
		}
		hidden, err := writeReadableResource(&content, item, opts)
		if err != nil {
			return "", err
		}
		omitted = omitted || hidden
	}
	if len(items) == 0 {
		content.WriteString("No results.\n")
	} else if omitted {
		fmt.Fprintln(&content, resourceSummaryHint)
	}
	return content.String(), opts.Context.Err()
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
	terminalOutputWriter
	stop context.CancelFunc
	mu   sync.Mutex
	err  error
}

func (w *listNavigationOutput) Write(data []byte) (int, error) {
	n, err := w.outputWriter.Write(data)
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
