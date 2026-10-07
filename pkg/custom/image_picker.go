package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-go/v3"
)

// imagePickerOptions configures the interactive image settings.
type imagePickerOptions struct {
	Prompt string
	// Shell selects quoting for the displayed command, never execution.
	Shell       string
	initial     *imagePickerSettings
	resuming    bool
	initialNote string
}

// imagePickerResult contains arguments for the existing images generate command.
// The picker itself never makes API requests or writes image files.
type imagePickerResult struct {
	Args      []string
	Canceled  bool
	PrintOnly bool
	// ExitCode preserves external SIGINT/SIGTERM status after terminal cleanup.
	ExitCode int
	settings imagePickerSettings
	shell    string
}

func runImagePicker(parent context.Context, input, output *os.File, options imagePickerOptions) (result imagePickerResult, err error) {
	if err := parent.Err(); err != nil {
		return imagePickerResult{}, err
	}
	model, err := newImagePicker(options)
	if err != nil {
		return imagePickerResult{}, err
	}
	defer model.cancelFolderWork()
	if input == nil || output == nil || !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		return imagePickerResult{}, errors.New("the image picker needs terminal input and output")
	}
	if os.Getenv("TERM") == "dumb" {
		return imagePickerResult{}, errors.New("the image picker needs a terminal with cursor support")
	}
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return imagePickerResult{}, errors.New("could not read the terminal size")
	}
	model.width, model.height = width, height
	// WithoutRenderer also disables Tea's terminal initialization. Own the input
	// state explicitly and restore it after the painter has closed its modes.
	inputState, err := term.GetState(input.Fd())
	if err != nil {
		return imagePickerResult{}, errors.New("could not read terminal input state")
	}
	outputState, err := term.GetState(output.Fd())
	if err != nil {
		return imagePickerResult{}, errors.New("could not read terminal output state")
	}
	// Capture both states before setup: a platform setup can change input and
	// then fail while enabling output. Restore both even after partial failure.
	defer func() {
		err = errors.Join(err, term.Restore(input.Fd(), inputState), term.Restore(output.Fd(), outputState))
	}()
	console := uv.NewConsole(input, output, os.Environ())
	_, err = console.MakeRaw()
	if err != nil {
		return imagePickerResult{}, fmt.Errorf("could not enable terminal input: %w", err)
	}
	// Parent cancellation goes through the model so the inline painter clears
	// its final frame before closing. Write failures stop the program at once.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracked := &imagePickerOutput{File: output, cancel: cancel}
	inline := &imagePickerInline{model: model, output: tracked, profile: colorprofile.Detect(output, os.Environ()), resuming: options.resuming}
	programOptions := []tea.ProgramOption{tea.WithInput(input), tea.WithOutput(tracked), tea.WithContext(ctx), tea.WithWindowSize(width, height), tea.WithoutSignalHandler(), tea.WithoutRenderer()}
	if os.Getenv("NO_COLOR") != "" {
		model.color = false
		programOptions = append(programOptions, tea.WithColorProfile(colorprofile.NoTTY))
	}
	program := tea.NewProgram(inline, programOptions...)
	// Route OS signals through the model so cleanup and exit status match
	// keyboard cancellation, without treating a write failure as cancellation.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	listenerDone := make(chan struct{})
	defer func() {
		signal.Stop(signals)
		cancel()
		<-listenerDone
	}()
	go func() {
		defer close(listenerDone)
		// Tea's resize handler is inactive without its renderer. Polling also
		// catches resizes on terminals that do not deliver SIGWINCH.
		resize := time.NewTicker(100 * time.Millisecond)
		defer resize.Stop()
		for {
			select {
			case received := <-signals:
				code := 130
				if received == syscall.SIGTERM {
					code = 143
				}
				program.Send(imagePickerStopMsg{code: code})
				return
			case <-ctx.Done():
				return
			case <-parent.Done():
				program.Send(imagePickerStopMsg{code: 130})
				return
			case <-resize.C:
				w, h, err := term.GetSize(output.Fd())
				if err != nil {
					program.Send(imagePickerSizeErrorMsg{})
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
	inline.close()
	writeErr := tracked.Err()
	if writeErr != nil {
		// Stopping Tea after a failed write cancels its private context. That
		// cancellation is not a user action; retain real parent cancellation below.
		if errors.Is(runErr, context.Canceled) {
			runErr = nil
		}
		writeErr = imageSavingFailure("Could not display the image picker. No new image request was started.", writeErr)
	}
	return model.result, errors.Join(runErr, writeErr, inline.err, parent.Err())
}

type imagePickerStopMsg struct{ code int }

// Stop on the first failed terminal write and retain that failure while the
// input loop exits and the painter attempts terminal cleanup.
type imagePickerOutput struct {
	*os.File
	mu     sync.Mutex
	err    error
	cancel context.CancelFunc
}

// io.WriteString must use the same error tracking as Write, rather than the
// embedded file's method, so failed repaints stop the input loop too.
func (w *imagePickerOutput) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *imagePickerOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.File.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if w.err == nil && err != nil {
		w.err = err
		w.cancel()
	}
	return n, err
}

func (w *imagePickerOutput) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

type imagePickerSettings struct {
	prompt, model, size, quality, background, format, count, outputDir string
}

type imagePicker struct {
	settings       imagePickerSettings
	page, field    string
	shell          string
	focus          string
	returnPage     string
	returnSelected int
	draft          []rune
	cursor         int
	selected       int
	commandOffset  int
	width, height  int
	color          bool
	dark           bool
	note           string
	result         imagePickerResult
	folder         imagePickerFolder
}

type imagePickerRow struct {
	id, label, value, detail string
}

func newImagePicker(options imagePickerOptions) (*imagePicker, error) {
	m := &imagePicker{
		settings: imagePickerSettings{prompt: options.Prompt, model: defaultSavedImageModel, size: "1024x1024", quality: "auto", background: "auto", format: "png", count: "1"},
		page:     "settings", focus: "prompt", color: true, dark: true,
		draft: []rune(options.Prompt), cursor: len([]rune(options.Prompt)),
	}
	if options.initial != nil {
		m.settings = *options.initial
		// Carry image settings forward, but require a fresh description for
		// each image. Only an explicitly supplied prompt may prefill it.
		m.settings.prompt = options.Prompt
	}
	m.shell = options.Shell
	m.note = options.initialNote
	return m, nil
}

func (m *imagePicker) Init() tea.Cmd {
	// The inline wrapper owns terminal initialization.
	return nil
}

func (m *imagePicker) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.result.Canceled {
		return m, nil
	}
	// Cancellation can still stop a submitted action before the picker exits.
	switch msg := message.(type) {
	case imagePickerStopMsg:
		m.cancelFolderWork()
		m.result = imagePickerResult{Canceled: true, ExitCode: msg.code}
		return m, tea.Quit
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.cancelFolderWork()
			m.result = imagePickerResult{Canceled: true}
			return m, tea.Quit
		}
	}
	// Ignore queued keys and paste after submission; run the captured command once.
	if len(m.result.Args) != 0 {
		return m, nil
	}
	switch msg := message.(type) {
	case imagePickerFolderMsg:
		return m, m.finishFolderWork(msg)
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampCommandOffset()
	case tea.PasteMsg:
		if m.folder.busy == "submit" || m.folder.busy == "select" {
			return m, nil
		}
		if m.focus == "path" {
			m.insertFolderPath(msg.Content)
		} else if m.focus == "prompt" {
			m.insertPrompt(msg.Content)
		} else {
			m.note = "Tab to Prompt to paste text."
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "esc" {
			if m.page == "folder" || m.page == "path" || m.folder.busy == "submit" {
				return m, m.backFolder()
			}
			m.focusPrompt()
			return m, nil
		}
		if m.folder.busy == "submit" || m.folder.busy == "select" {
			return m, nil
		}
		if m.focus == "path" {
			if m.width >= 40 && m.height >= 12 {
				return m, m.editFolderPath(msg)
			}
			return m, nil
		}
		// Esc immediately followed by another key can arrive as an Alt key.
		// Leave menus first; no Alt shortcuts are assigned
		// there. In the prompt itself, Alt+Enter still inserts a newline.
		if msg.Mod&tea.ModAlt != 0 {
			printable := (msg.Mod == tea.ModAlt || msg.Mod == tea.ModAlt|tea.ModShift) && unicode.IsPrint(msg.Code)
			if m.focus != "prompt" || printable {
				m.focusPrompt()
				if printable {
					letter := msg.Code
					if msg.Mod&tea.ModShift != 0 {
						letter = unicode.ToUpper(letter)
					}
					m.insertPrompt(string(letter))
				}
				return m, nil
			}
		}
		if key == "q" && m.focus != "prompt" {
			m.cancelFolderWork()
			m.result = imagePickerResult{Canceled: true}
			return m, tea.Quit
		}
		// At very small sizes, only returning to the prompt or quitting is safe.
		if m.width < 40 || m.height < 12 {
			return m, nil
		}
		switch key {
		case "ctrl+g":
			return m, m.activate(imagePickerRow{id: "generate"})
		case "ctrl+p":
			return m, m.activate(imagePickerRow{id: "print"})
		case "tab", "shift+tab":
			m.cycleFocus(key == "shift+tab")
			return m, nil
		}
		if m.focus == "prompt" {
			switch key {
			case "enter":
				return m, m.activate(imagePickerRow{id: "generate"})
			case "down":
				m.focus, m.selected = "options", 0
			default:
				m.editPrompt(msg)
			}
			return m, nil
		}
		if m.focus == "command" {
			if key == "up" && m.commandOffset == 0 {
				m.focus, m.selected = "options", len(m.rows())-1
				return m, nil
			}
			if m.scrollCommand(key) {
				return m, nil
			}
			switch key {
			case "enter":
				return m, m.activate(imagePickerRow{id: "generate"})
			}
			return m, nil
		}
		rows := m.rows()
		switch key {
		case "up":
			if m.selected == 0 {
				m.focus = "prompt"
			} else {
				m.selected--
			}
		case "down":
			if m.selected == len(rows)-1 {
				m.focus, m.commandOffset = "command", 0
			} else {
				m.selected++
			}
		case "home":
			m.selected = 0
		case "end":
			m.selected = len(rows) - 1
		case "left":
			return m, m.back()
		case "enter":
			return m, m.activate(rows[m.selected])
		}
	}
	return m, nil
}

// Esc closes any menu without applying its highlighted choice. Committed
// settings and prompt text remain intact.
func (m *imagePicker) focusPrompt() {
	m.cancelFolderWork()
	m.page, m.focus, m.selected, m.note = "settings", "prompt", 0, ""
}

func (m *imagePicker) rows() []imagePickerRow {
	s := m.settings
	switch m.page {
	case "folder":
		return m.folderRows()
	case "path":
		return m.folderCompletionRows()
	case "choose":
		return m.choices(m.field)
	case "more":
		return []imagePickerRow{{id: "format", label: "File type", value: strings.ToUpper(s.format)}, {id: "back", label: "Back to settings"}}
	}
	rows := []imagePickerRow{
		{id: "model", label: "Model", value: s.model},
		{id: "size", label: "Size / aspect", value: imagePickerSizeLabel(s.size)},
		{id: "quality", label: "Quality", value: imagePickerTitle(s.quality)},
		{id: "count", label: "Images", value: s.count},
		{id: "background", label: "Background", value: imagePickerTitle(s.background)},
	}
	return append(rows, m.folderRow(), imagePickerRow{id: "more", label: "More options", value: strings.ToUpper(s.format)})
}

func (m *imagePicker) cycleFocus(backwards bool) {
	focuses := []string{"prompt", "options", "command"}
	for i, focus := range focuses {
		if focus == m.focus {
			delta := 1
			if backwards {
				delta = -1
			}
			m.focus = focuses[(i+len(focuses)+delta)%len(focuses)]
			return
		}
	}
}

func (m *imagePicker) choices(field string) []imagePickerRow {
	var values []string
	switch field {
	case "model":
		values = []string{openai.ImageModelGPTImage2_5Sunburst, openai.ImageModelGPTImage2_5Flare, openai.ImageModelGPTImage2, openai.ImageModelGPTImage1_5, openai.ImageModelGPTImage1Mini}
	case "size":
		values = []string{"auto", "1024x1024", "1536x1024", "1024x1536"}
	case "quality":
		values = []string{"auto", "low", "medium", "high"}
		if m.settings.model == openai.ImageModelGPTImage2_5Sunburst || m.settings.model == openai.ImageModelGPTImage2_5Flare {
			values = append(values, "xhigh", "max")
		}
	case "background":
		values = []string{"auto", "opaque", "transparent"}
	case "format":
		values = []string{"png", "jpeg", "webp"}
	case "count":
		for n := 1; n <= 10; n++ {
			values = append(values, fmt.Sprint(n))
		}
	}
	rows := make([]imagePickerRow, 0, len(values))
	for _, value := range values {
		row := imagePickerRow{id: "choice", value: value, label: imagePickerTitle(value)}
		switch field {
		case "model":
			row.label = value
			if value == openai.ImageModelGPTImage2_5Sunburst {
				row.detail = "CLI default"
			}
		case "size":
			row.label = imagePickerSizeLabel(value)
		case "format":
			row.label = strings.ToUpper(value)
			if value == "jpeg" {
				row.detail = "No transparency"
			}
		}
		if value == "auto" {
			row.detail = "Model chooses"
		}
		rows = append(rows, row)
	}
	return rows
}

func (m *imagePicker) value(field string) string {
	s := m.settings
	switch field {
	case "model":
		return s.model
	case "size":
		return s.size
	case "quality":
		return s.quality
	case "background":
		return s.background
	case "format":
		return s.format
	case "count":
		return s.count
	}
	return ""
}

func (m *imagePicker) choose(field string) {
	m.page, m.field = "choose", field
	m.selectCurrent(field)
}

func (m *imagePicker) apply(value string) {
	if m.value(m.field) != value {
		m.commandOffset = 0
	}
	m.note = ""
	s := &m.settings
	switch m.field {
	case "model":
		s.model = value
		if (s.quality == "xhigh" || s.quality == "max") && value != openai.ImageModelGPTImage2_5Sunburst && value != openai.ImageModelGPTImage2_5Flare {
			s.quality = "auto"
			m.note = "Quality changed to Auto for this model."
		}
	case "size":
		s.size = value
	case "quality":
		s.quality = value
	case "background":
		s.background = value
		if value == "transparent" && s.format == "jpeg" {
			s.format = "png"
			m.note = "File type changed to PNG to keep transparency."
		}
	case "format":
		s.format = value
		if value == "jpeg" && s.background == "transparent" {
			s.background = "opaque"
			m.note = "Background changed to Opaque for JPEG."
		}
	case "count":
		s.count = value
	}
}

func (m *imagePicker) activate(row imagePickerRow) tea.Cmd {
	switch row.id {
	case "folder", "folder-default", "folder-current", "folder-path", "folder-back":
		return m.activateFolder(row.id)
	case "choice":
		m.apply(row.value)
		m.page, m.selected = m.returnPage, m.returnSelected
	case "model", "size", "quality", "background", "format", "count":
		m.returnPage, m.returnSelected = m.page, m.selected
		m.choose(row.id)

	case "more":
		m.page, m.selected = "more", 0
	case "generate", "print":
		if row.id == "print" && imagePickerShellQuoter(m.shell) == nil {
			m.note = imagePickerUnsupportedShell
			return nil
		}
		if m.validPrompt() {
			return m.submitWithFolder(row.id == "print")
		}
	case "back":
		return m.back()
	}
	return nil
}

func (m *imagePicker) selectCurrent(field string) {
	m.selected = 0
	for i, row := range m.choices(field) {
		if row.value == m.value(field) {
			m.selected = i
		}
	}
}

func (m *imagePicker) validPrompt() bool {
	switch {
	case strings.TrimSpace(m.settings.prompt) == "":
		m.note = "Add a prompt first."
	case strings.ContainsRune(m.settings.prompt, 0):
		m.note = "Remove the NUL character; shell arguments cannot contain it."
	case strings.HasPrefix(m.settings.prompt, `\@`):
		m.note = `Prompts beginning with \@ cannot be represented by the request parser.`
	default:
		return true
	}
	return false
}

func (m *imagePicker) back() tea.Cmd {
	m.note = ""
	switch m.page {
	case "folder", "path":
		return m.backFolder()
	case "more":
		m.page = "settings"
		m.selected = len(m.rows()) - 1
	case "choose":
		m.page, m.selected = m.returnPage, m.returnSelected
	default:
		m.focus = "prompt"
	}
	return nil
}

func (m *imagePicker) insertPrompt(text string) {
	insert := []rune(text)
	tail := append([]rune(nil), m.draft[m.cursor:]...)
	m.draft = append(m.draft[:m.cursor], insert...)
	m.draft = append(m.draft, tail...)
	m.cursor += len(insert)
	m.settings.prompt = string(m.draft)
	if text != "" {
		m.commandOffset = 0
	}
	m.note = ""
}

func (m *imagePicker) editPrompt(key tea.KeyPressMsg) {
	previous := m.settings.prompt
	switch key.String() {
	case "left":
		m.cursor = max(0, m.cursor-1)
	case "right":
		m.cursor = min(len(m.draft), m.cursor+1)
	case "home", "ctrl+a":
		m.cursor = 0
	case "end", "ctrl+e":
		m.cursor = len(m.draft)
	case "ctrl+u":
		m.draft, m.cursor = m.draft[m.cursor:], 0
	case "backspace":
		if m.cursor > 0 {
			m.draft = append(m.draft[:m.cursor-1], m.draft[m.cursor:]...)
			m.cursor--
		}
	case "delete":
		if m.cursor < len(m.draft) {
			m.draft = append(m.draft[:m.cursor], m.draft[m.cursor+1:]...)
		}
	case "alt+enter":
		m.insertPrompt("\n")
	default:
		if key.Text != "" {
			m.insertPrompt(key.Text)
		}
	}
	m.settings.prompt = string(m.draft)
	if m.settings.prompt != previous {
		m.commandOffset = 0
	}
}
