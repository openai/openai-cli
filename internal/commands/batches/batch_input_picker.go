package batches

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/tidwall/gjson"
)

type batchInputItem struct{ value gjson.Result }

func batchInputLabel(value string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(readable.Text(value))
}

func (item batchInputItem) Title() string {
	name := item.value.Get("filename").Str
	if name == "" {
		name = item.value.Get("id").Str
	}
	return batchInputLabel(name)
}
func (item batchInputItem) Description() string { return batchInputLabel(item.value.Get("id").Str) }
func (item batchInputItem) FilterValue() string { return item.Title() + " " + item.Description() }

type batchInputPicker struct {
	input                textinput.Model
	items                []gjson.Result
	visible              []int
	index, width, height int
	ctx                  context.Context
	selected             gjson.Result
	err                  error
}

func newBatchInputPicker(ctx context.Context, items []gjson.Result, width, height int) *batchInputPicker {
	input := textinput.New()
	input.Prompt = "Search: "
	input.SetStyles(textinput.DefaultStyles(terminal.DarkBackground(os.Getenv("COLORFGBG"))))
	input.SetWidth(max(1, width-8))
	input.Focus()
	m := &batchInputPicker{input: input, items: items, width: width, height: height, ctx: ctx}
	m.filter()
	return m
}

func (m *batchInputPicker) filter() {
	m.visible = m.visible[:0]
	search := strings.ToLower(m.input.Value())
	for i, item := range m.items {
		if m.ctx.Err() != nil {
			m.err = m.ctx.Err()
			return
		}
		if strings.Contains(strings.ToLower(item.Get("filename").Str), search) || strings.Contains(strings.ToLower(item.Get("id").Str), search) {
			m.visible = append(m.visible, i)
		}
	}
	m.index = 0
}

func (m *batchInputPicker) setPickerContext(ctx context.Context) { m.ctx = ctx }

func (m *batchInputPicker) Init() tea.Cmd { return textinput.Blink }
func (m *batchInputPicker) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(1, msg.Width-8))
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.err = &WorkflowError{message: "No input file selected. No batch was created.", code: 130}
			return m, tea.Quit
		case "up":
			m.index = max(0, m.index-1)
			return m, nil
		case "down":
			m.index = min(max(0, len(m.visible)-1), m.index+1)
			return m, nil
		case "enter":
			if m.height >= 5 && m.width >= 12 && len(m.visible) > 0 {
				m.selected = m.items[m.visible[m.index]]
				return m, tea.Quit
			}
			return m, nil
		}
	}
	previous := m.input.Value()
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	if m.input.Value() != previous {
		m.filter()
	}
	if m.err != nil {
		return m, tea.Quit
	}
	return m, command
}
func (m *batchInputPicker) View() tea.View {
	if m.height < 5 || m.width < 12 {
		return tea.NewView(ansi.Truncate("Resize terminal. Esc: cancel", max(1, m.width), "…"))
	}
	var view strings.Builder
	view.WriteString(ansi.Truncate("Select a Batch input file", m.width, "…") + "\n")
	view.WriteString(m.input.View() + "\n")
	capacity := max(1, (m.height-3)/2)
	start := max(0, m.index-capacity+1)
	for i := start; i < len(m.visible) && i < start+capacity; i++ {
		item := batchInputItem{value: m.items[m.visible[i]]}
		prefix := "  "
		if i == m.index {
			prefix = "> "
		}
		view.WriteString(ansi.Truncate(prefix+item.Title(), m.width, "…") + "\n")
		view.WriteString(ansi.Truncate("  "+item.Description(), m.width, "…") + "\n")
	}
	if len(m.visible) == 0 {
		view.WriteString("No matching files.\n")
	}
	view.WriteString(ansi.Truncate("↑/↓: choose   Enter: select   Esc: cancel", m.width, "…"))
	return tea.NewView(view.String())
}

func runBatchInputPicker(ctx context.Context, items []gjson.Result) (gjson.Result, error) {
	m := newBatchInputPicker(ctx, items, 80, 24)
	err := runBatchPickerModel(ctx, m, batchFilePickerInputPolicy())
	if err = errors.Join(m.err, err); err != nil {
		return gjson.Result{}, err
	}
	if !m.selected.Exists() {
		return gjson.Result{}, batchError("No input file selected. No batch was created.")
	}
	return m.selected, nil
}

// runBatchPickerModel retains one terminal lifecycle for file and Batch selection.
func runBatchPickerModel(ctx context.Context, model tea.Model, policy batchPickerInputPolicy) (err error) {
	width, height, err := term.GetSize(os.Stderr.Fd())
	if err != nil {
		return batchError("Could not inspect the terminal. Use the noninteractive Batch commands instead.")
	}
	inputState, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		return err
	}
	outputState, err := term.GetState(os.Stderr.Fd())
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, term.Restore(os.Stdin.Fd(), inputState), term.Restore(os.Stderr.Fd(), outputState))
	}()
	outputContext, stopOutput := context.WithCancelCause(ctx)
	defer stopOutput(nil)
	if aware, ok := model.(interface{ setPickerContext(context.Context) }); ok {
		aware.setPickerContext(outputContext)
	}
	model, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	programContext, cancel := context.WithCancel(outputContext)
	defer cancel()
	err = writeBatchStdoutWithCleanup(outputContext, os.Stderr, batchOutputCleanupGrace, func(out io.Writer) error {
		if _, err := term.MakeRaw(os.Stdin.Fd()); err != nil {
			return err
		}
		writer := &batchPickerOutput{TerminalWriter: clioutput.TerminalWriter{
			Writer: clioutput.Writer{Context: context.Background(), Sink: out}, File: os.Stderr,
		}, cancel: cancel}
		program := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(writer), tea.WithContext(programContext),
			tea.WithoutSignalHandler(), tea.WithWindowSize(width, height),
			tea.WithColorProfile(terminal.ColorProfile(os.Stderr, os.Environ(), true)))
		closeInput, err := startBatchPickerInputWithPolicy(outputContext, os.Stdin, program, stopOutput, policy)
		if err != nil {
			return err
		}
		_, runErr := program.Run()
		inputErr := closeInput()
		writer.mu.Lock()
		outputErr := writer.err
		writer.mu.Unlock()
		if outputErr != nil && ctx.Err() == nil && errors.Is(runErr, tea.ErrProgramKilled) {
			runErr = nil
		}
		return errors.Join(inputErr, outputErr, runErr)
	})
	if ctx.Err() != nil {
		return batchContextError(errors.Join(ctx.Err(), context.Cause(ctx), err))
	}
	if cause := context.Cause(outputContext); cause != nil {
		var failure *WorkflowError
		if errors.As(cause, &failure) {
			return &WorkflowError{message: failure.message, code: failure.code, cause: errors.Join(cause, err)}
		}
	}
	return err
}

// Read and queue decoded keys separately from Tea's renderer. A blocked frame
// must not prevent Ctrl+C from cancelling the owned output worker.
type batchPickerInputPolicy struct {
	escapeCancels              bool
	cancelled, ended, overflow string
}

func batchFilePickerInputPolicy() batchPickerInputPolicy {
	return batchPickerInputPolicy{
		escapeCancels: true,
		cancelled:     "No input file selected. No batch was created.",
		ended:         "Terminal input ended. No file was selected. Retry with --select or list files without it.",
		overflow:      "Too much pending keyboard input. The picker accepts 64 queued events and 1 MiB of queued text. Retry with less input or omit --select.",
	}
}

func startBatchPickerInput(ctx context.Context, input *os.File, program *tea.Program, stop context.CancelCauseFunc) (func() error, error) {
	return startBatchPickerInputWithPolicy(ctx, input, program, stop, batchFilePickerInputPolicy())
}

func startBatchPickerInputWithPolicy(ctx context.Context, input *os.File, program *tea.Program, stop context.CancelCauseFunc, policy batchPickerInputPolicy) (func() error, error) {
	source, err := uv.NewCancelReader(input)
	if err != nil {
		return nil, err
	}
	private, cancel := context.WithCancel(ctx)
	sourceStopped := make(chan struct{})
	stopSource := context.AfterFunc(private, func() { source.Cancel(); close(sourceStopped) })
	events, deliveries := make(chan uv.Event), make(chan uv.Event)
	readDone, routeDone, sendDone := make(chan error, 1), make(chan struct{}), make(chan struct{})
	go func() {
		readDone <- uv.NewTerminalReader(source, os.Getenv("TERM")).StreamEvents(private, events)
		close(events)
	}()
	go func() {
		defer close(sendDone)
		for event := range deliveries {
			program.Send(event)
		}
	}()
	go func() {
		defer close(routeDone)
		defer close(deliveries)
		routeBatchPickerInputWithPolicy(private, events, deliveries, stop, policy)
	}()
	return func() error {
		cancel()
		if stopSource() {
			source.Cancel()
		} else {
			<-sourceStopped
		}
		<-routeDone
		<-sendDone
		return errors.Join(<-readDone, source.Close())
	}, nil
}

func routeBatchPickerInput(ctx context.Context, events <-chan uv.Event, deliveries chan<- uv.Event, stop context.CancelCauseFunc) {
	routeBatchPickerInputWithPolicy(ctx, events, deliveries, stop, batchFilePickerInputPolicy())
}

func routeBatchPickerInputWithPolicy(ctx context.Context, events <-chan uv.Event, deliveries chan<- uv.Event, stop context.CancelCauseFunc, policy batchPickerInputPolicy) {
	const pendingKeys = 64
	const pendingBytes = 1 << 20
	type queuedKey struct {
		event uv.Event
		bytes int
	}
	queue := make([]queuedKey, 0, pendingKeys)
	bytes := 0
	for {
		if ctx.Err() != nil {
			// StreamEvents flushes pending parser events during cancellation.
			for range events {
			}
			return
		}
		var destination chan<- uv.Event
		var next uv.Event
		if len(queue) > 0 {
			destination, next = deliveries, queue[0].event
		}
		select {
		case <-ctx.Done():
		case event, ok := <-events:
			if !ok {
				if ctx.Err() == nil {
					stop(&WorkflowError{message: policy.ended, code: 1})
				}
				return
			}
			size := 0
			switch key := event.(type) {
			case uv.KeyPressEvent:
				if key.String() == "ctrl+c" || policy.escapeCancels && key.String() == "esc" {
					stop(&WorkflowError{message: policy.cancelled, code: 130, cause: context.Canceled})
					continue
				}
				size = len(key.Text)
			case uv.KeyReleaseEvent:
				size = len(key.Text)
			case uv.PasteEvent:
				size = len(key.Content)
			}
			if len(queue) >= pendingKeys || size > pendingBytes-bytes {
				stop(&WorkflowError{message: policy.overflow, code: 1})
				continue
			}
			queue = append(queue, queuedKey{event, size})
			bytes += size
		case destination <- next:
			bytes -= queue[0].bytes
			copy(queue, queue[1:])
			queue[len(queue)-1] = queuedKey{}
			queue = queue[:len(queue)-1]
		}
	}
}

// Stop the event loop when terminal output fails; Tea must not hide sink errors.
type batchPickerOutput struct {
	clioutput.TerminalWriter
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error
}

func (w *batchPickerOutput) Write(data []byte) (int, error) {
	n, err := w.TerminalWriter.Write(data)
	if err != nil {
		w.mu.Lock()
		w.err = errors.Join(w.err, err)
		w.mu.Unlock()
		w.cancel()
	}
	return n, err
}
func (w *batchPickerOutput) WriteString(data string) (int, error) { return w.Write([]byte(data)) }
