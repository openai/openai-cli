package webhooks

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminal"
)

func runWebhookCreatePicker(ctx context.Context, input, output *os.File, events []string) (webhookCreateSettings, bool, error) {
	if err := ctx.Err(); err != nil {
		return webhookCreateSettings{}, false, err
	}
	if input == nil || output == nil || !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		return webhookCreateSettings{}, false, &WorkflowError{"Webhook setup needs terminal input and output. " +
			"No endpoint was created. Use openai webhooks create --help for explicit flags.", nil}
	}
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return webhookCreateSettings{}, false, &WorkflowError{"Could not read the terminal size. No endpoint was created. " +
			"Check your terminal, then retry webhooks create with your original authentication, project, organization, and API settings. " +
			"Use openai webhooks create --help for explicit flags.", err}
	}
	model := newWebhookCreatePicker(events)
	model.width, model.height = width, height
	model.color = os.Getenv("NO_COLOR") == ""
	model.dark = terminal.DarkBackground(os.Getenv("COLORFGBG"))
	// Parent cancellation must reach the model so Tea paints its final frame.
	// Only a failed output write immediately stops the private program context.
	programContext, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	// Use the same guarded writer as list navigation. Terminal restoration must
	// remain possible after the request context or a failed write cancels Tea.
	out := &webhookPickerOutput{TerminalWriter: clioutput.TerminalWriter{
		Writer: clioutput.Writer{Context: context.Background(), Sink: output}, File: output,
	}, stop: stop}
	options := []tea.ProgramOption{tea.WithInput(input), tea.WithOutput(out), tea.WithContext(programContext),
		tea.WithWindowSize(width, height), tea.WithoutSignalHandler()}
	if !model.color {
		options = append(options, tea.WithColorProfile(colorprofile.NoTTY))
	}
	program := tea.NewProgram(model, options...)
	finishCancellation := watchWebhookCreateCancellation(ctx, program, stop)
	defer finishCancellation()
	_, runErr := program.Run()
	if out.Err() != nil && ctx.Err() == nil {
		runErr = webhookWithoutCleanupCancellation(runErr)
	}
	err = errors.Join(runErr, out.Err(), ctx.Err())
	if err != nil && !errors.Is(err, context.Canceled) {
		err = &WorkflowError{"Webhook setup could not finish. No endpoint was created. " +
			"Check your terminal, then retry webhooks create with your original authentication, project, organization, and API settings. " +
			"Use openai webhooks create --help for explicit flags.", err}
	}
	return model.settings(), model.confirmed && !model.canceled && err == nil, err
}

type webhookCreateCancelMsg struct{}

// Join the watcher on every exit, including failed terminal initialization.
// Stopping the private context also releases Send if the event loop has ended.
func watchWebhookCreateCancellation(ctx context.Context, program *tea.Program, stop context.CancelFunc) func() {
	runDone, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			program.Send(webhookCreateCancelMsg{})
		case <-runDone:
		}
	}()
	return func() {
		close(runDone)
		stop()
		<-watcherDone
	}
}

type webhookCreateInput struct {
	value  []rune
	cursor int
}

func (i *webhookCreateInput) insert(text string) bool {
	if !utf8.ValidString(text) || strings.IndexFunc(text, unicode.IsControl) >= 0 {
		return false
	}
	insert := []rune(text)
	tail := slices.Clone(i.value[i.cursor:])
	i.value = append(i.value[:i.cursor], insert...)
	i.value = append(i.value, tail...)
	i.cursor += len(insert)
	return true
}

func (i *webhookCreateInput) edit(key tea.KeyPressMsg) bool {
	switch key.String() {
	case "left":
		i.cursor = max(0, i.cursor-1)
	case "right":
		i.cursor = min(len(i.value), i.cursor+1)
	case "home", "ctrl+a":
		i.cursor = 0
	case "end", "ctrl+e":
		i.cursor = len(i.value)
	case "ctrl+u":
		i.value, i.cursor = i.value[i.cursor:], 0
	case "ctrl+k":
		i.value = i.value[:i.cursor]
	case "backspace":
		if i.cursor > 0 {
			i.value = append(i.value[:i.cursor-1], i.value[i.cursor:]...)
			i.cursor--
		}
	case "delete":
		if i.cursor < len(i.value) {
			i.value = append(i.value[:i.cursor], i.value[i.cursor+1:]...)
		}
	default:
		if key.Text != "" {
			return i.insert(key.Text)
		}
	}
	return true
}

// Each field and the final confirmation has a single, visible action.
// Search only changes the visible rows; selected event names remain exact.
type webhookCreatePicker struct {
	stage, width, height int
	name, url, search    webhookCreateInput
	events               []string
	selected             map[string]bool
	current, offset      int
	reviewOffset         int
	confirmYes           bool
	confirmed, canceled  bool
	color                bool
	dark                 bool
	ownedHeight          int
	note                 string
}

func newWebhookCreatePicker(events []string) *webhookCreatePicker {
	m := &webhookCreatePicker{selected: map[string]bool{}, width: 80, height: 24, dark: true}
	seen := map[string]bool{}
	for _, event := range events {
		if !seen[event] {
			m.events = append(m.events, event)
			seen[event] = true
		}
	}
	slices.SortStableFunc(m.events, func(a, b string) int {
		ga, _ := webhookCreateEventGroup(a)
		gb, _ := webhookCreateEventGroup(b)
		if ga != gb {
			return ga - gb
		}
		return strings.Compare(a, b)
	})
	return m
}

func (*webhookCreatePicker) Init() tea.Cmd { return nil }

func (m *webhookCreatePicker) settings() webhookCreateSettings {
	name := string(m.name.value)
	if name == "" {
		name = "Webhook"
	}
	result := webhookCreateSettings{name: name, url: string(m.url.value)}
	for _, event := range m.events {
		if m.selected[event] {
			result.events = append(result.events, event)
		}
	}
	return result
}

func (m *webhookCreatePicker) filteredEvents() []string {
	query := strings.ToLower(string(m.search.value))
	var matches []string
	for _, event := range m.events {
		_, group := webhookCreateEventGroup(event)
		if strings.Contains(strings.ToLower(event), query) || strings.Contains(strings.ToLower(group), query) {
			matches = append(matches, event)
		}
	}
	return matches
}

func (m *webhookCreatePicker) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if _, canceled := message.(webhookCreateCancelMsg); canceled {
		m.canceled, m.confirmed = true, false
		return m, tea.Quit
	}
	if key, ok := message.(tea.KeyPressMsg); ok && (key.String() == "ctrl+c" || key.String() == "esc") {
		m.canceled, m.confirmed = true, false
		return m, tea.Quit
	}
	if m.confirmed || m.canceled {
		return m, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.PasteMsg:
		if m.width < 36 || m.height < 10 {
			return m, nil
		}
		if m.stage < 3 {
			if !m.input().insert(msg.Content) {
				m.note = "Paste one line without control characters."
			} else {
				m.note = ""
				m.current, m.offset = 0, 0
			}
		}
	case tea.KeyPressMsg:
		if m.width < 36 || m.height < 10 {
			return m, nil
		}
		key := msg.String()
		if key == "shift+tab" {
			m.stage = max(0, m.stage-1)
			m.confirmYes, m.note = false, ""
			return m, nil
		}
		if m.stage == 3 {
			switch key {
			case "left", "n":
				m.confirmYes = false
			case "right", "y":
				m.confirmYes = true
			case "tab":
				m.confirmYes = !m.confirmYes
			case "up", "pgup":
				m.reviewOffset = max(0, m.reviewOffset-1)
			case "down", "pgdown":
				m.reviewOffset++
			case "enter":
				m.confirmed, m.canceled = m.confirmYes, !m.confirmYes
				return m, tea.Quit
			}
			return m, nil
		}
		if key == "enter" || key == "tab" {
			m.advance()
			return m, nil
		}
		if m.stage == 2 {
			matches := m.filteredEvents()
			switch key {
			case "up":
				m.current = max(0, m.current-1)
				return m, nil
			case "down":
				m.current = min(max(0, len(matches)-1), m.current+1)
				return m, nil
			case "space", " ":
				if len(matches) != 0 {
					event := matches[m.current]
					m.selected[event] = !m.selected[event]
					m.note = ""
				}
				return m, nil
			}
		}
		previous := string(m.input().value)
		if !m.input().edit(msg) {
			m.note = "Use one line without control characters."
		} else if previous != string(m.input().value) {
			m.note = ""
			m.current, m.offset = 0, 0
		}
	}
	return m, nil
}

func (m *webhookCreatePicker) input() *webhookCreateInput {
	switch m.stage {
	case 0:
		return &m.name
	case 1:
		return &m.url
	default:
		return &m.search
	}
}

func (m *webhookCreatePicker) advance() {
	settings := m.settings()
	switch m.stage {
	case 0:
		if strings.TrimSpace(settings.name) == "" || utf8.RuneCountInString(settings.name) > 256 {
			m.note = "Use a name with 1 to 256 characters."
			return
		}
	case 1:
		if err := validateWebhookCreateURL(settings.url); err != nil {
			m.note = err.Error()
			return
		}
	case 2:
		if len(settings.events) == 0 {
			m.note = "Select at least one event with Space."
			return
		}
	}
	m.stage++
	m.note, m.confirmYes, m.reviewOffset = "", false, 0
}

func validateWebhookCreateSettings(settings webhookCreateSettings) error {
	if strings.TrimSpace(settings.name) == "" || utf8.RuneCountInString(settings.name) > 256 {
		return errors.New("Use a webhook name with 1 to 256 characters.")
	}
	if err := validateWebhookCreateURL(settings.url); err != nil {
		return err
	}
	if len(settings.events) == 0 {
		return errors.New("Select at least one webhook event.")
	}
	return nil
}

func validateWebhookCreateURL(value string) error {
	if !strings.HasPrefix(value, "https://") {
		return errors.New("Use lowercase https:// in the URL.")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" || strings.ContainsAny(value, " \t\r\n") {
		return errors.New("Enter a complete HTTPS URL.")
	}
	if utf8.RuneCountInString(value) > 2048 {
		return errors.New("Use at most 2048 URL characters.")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return errors.New("Remove URL credentials or #fragment.")
	}
	return nil
}

func webhookCreateEventGroup(event string) (int, string) {
	for i, group := range []struct{ prefix, label string }{
		{"agent.session.", "Agents"}, {"batch.", "Batches"}, {"response.", "Background responses"},
		{"eval.run.", "Eval runs"}, {"fine_tuning.job.", "Fine-tuning"}, {"realtime.", "Realtime API"},
		{"video.", "Videos"}, {"safety.", "Safety"},
	} {
		if strings.HasPrefix(event, group.prefix) {
			return i, group.label
		}
	}
	return 8, "Other"
}

func webhookCreateLine(text string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(readable.Text(text))
}

func (i *webhookCreateInput) display(width int, placeholder string) string {
	if width <= 0 {
		return ""
	}
	before := webhookCreateLine(string(i.value[:i.cursor]))
	after := webhookCreateLine(string(i.value[i.cursor:]))
	if len(i.value) == 0 {
		after = placeholder
	}
	// Reserve the cursor before truncating either side. Show nearby text on
	// both sides while editing, and the actual tail when the cursor is at end.
	remaining := width - 1
	afterRoom := min(ansi.StringWidth(after), remaining/3)
	beforeRoom := remaining - afterRoom
	if beforeWidth := ansi.StringWidth(before); beforeWidth > beforeRoom {
		if beforeRoom == 0 {
			before = ""
		} else {
			cut := beforeWidth - beforeRoom + 1 // leave room for the left ellipsis
			tail := ansi.TruncateLeft(before, cut, "")
			// A cut through a wide grapheme can retain that whole grapheme.
			for ansi.StringWidth(tail) > beforeRoom-1 {
				cut++
				tail = ansi.TruncateLeft(before, cut, "")
			}
			before = "…" + tail
		}
	}
	afterRoom = remaining - ansi.StringWidth(before)
	return before + "▏" + ansi.Truncate(after, afterRoom, "…")
}

func (m *webhookCreatePicker) View() tea.View {
	width, height := max(1, min(76, m.width-2)), max(1, min(18, m.height-1))
	if m.canceled {
		return m.frame([]string{"Webhook creation canceled."}, width, height)
	}
	if m.confirmed {
		return m.frame([]string{"Creating webhook endpoint..."}, width, height)
	}
	if m.width < 36 || m.height < 10 {
		return m.frame([]string{"Resize to 36 x 10.", "Esc or Ctrl+C cancels."}, width, height)
	}
	accent, fill, text, muted, border := "#3159BC", "#E9EFFE", "#20283B", "#657087", "#BAC4D8"
	if m.dark {
		accent, fill, text, muted, border = "#8AA8FF", "#343D58", "#F2F4FA", "#A4ACC2", "#51566B"
	}
	strong, quiet, selected := lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle()
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if m.color {
		strong = strong.Bold(true).Foreground(lipgloss.Color(accent))
		quiet = quiet.Foreground(lipgloss.Color(muted))
		selected = selected.Foreground(lipgloss.Color(text)).Background(lipgloss.Color(fill))
		panel = panel.BorderForeground(lipgloss.Color(border))
	}
	inner := width - 4 // Border and one padding cell on each side.
	clip := func(s string) string { return ansi.Truncate(s, inner, "…") }
	active := func(s string) string {
		s = clip(s)
		return selected.Render(s + strings.Repeat(" ", max(0, inner-ansi.StringWidth(s))))
	}
	body := []string{}
	footer := "Enter next · Shift+Tab back · Esc cancel"
	if width < 44 {
		footer = "Enter next · Esc cancel"
	}
	switch m.stage {
	case 0:
		body = append(body, active("> Name  "+m.name.display(inner-8, "Webhook")),
			quiet.Render(clip("Optional; default: Webhook")), "",
			clip("Choose a name to recognize this endpoint."))
	case 1:
		body = append(body, active("> URL   "+m.url.display(inner-8, "https://example.com/webhook")), "",
			clip("OpenAI sends events to this HTTPS receiver."))
	case 2:
		body = append(body, "Search: "+m.search.display(inner-8, "event or group"))
		matches := m.filteredEvents()
		body = append(body, quiet.Render(clip(fmt.Sprintf("%d selected · %d matching events", len(m.settings().events), len(matches)))))
		// Reserve title, border, hints, and a note. Every event may start a group.
		available := min(8, max(1, (height-8)/2))
		m.current = min(m.current, max(0, len(matches)-1))
		m.offset = min(m.offset, max(0, len(matches)-available))
		if m.current < m.offset {
			m.offset = m.current
		} else if m.current >= m.offset+available {
			m.offset = m.current - available + 1
		}
		lastGroup := ""
		for index := m.offset; index < min(len(matches), m.offset+available); index++ {
			event := matches[index]
			_, group := webhookCreateEventGroup(event)
			if group != lastGroup {
				body = append(body, quiet.Render(clip(group)))
				lastGroup = group
			}
			marker, tick := " ", " "
			if index == m.current {
				marker = ">"
			}
			if m.selected[event] {
				tick = "x"
			}
			row := clip(marker + " [" + tick + "] " + webhookCreateLine(event))
			if index == m.current {
				row = active(row)
			}
			body = append(body, row)
		}
		if len(matches) == 0 {
			body = append(body, clip("No matches. Edit the search."))
		}
		footer = "↑↓ move · Space select · Enter review"
		if width < 44 {
			footer = "↑↓ · Space select · Enter · Esc"
		}
	case 3:
		settings := m.settings()
		details := []string{"Name: " + webhookCreateLine(settings.name), "URL: " + webhookCreateLine(settings.url), "Events:"}
		for _, event := range settings.events {
			details = append(details, "  "+webhookCreateLine(event))
		}
		wrapped := strings.Split(ansi.Hardwrap(strings.Join(details, "\n"), inner, false), "\n")
		available := max(1, height-8)
		m.reviewOffset = min(m.reviewOffset, max(0, len(wrapped)-available))
		body = append(body, wrapped[m.reviewOffset:min(len(wrapped), m.reviewOffset+available)]...)
		if len(wrapped) > available {
			body = append(body, quiet.Render(clip(fmt.Sprintf("↑↓ review %d-%d/%d", m.reviewOffset+1, min(len(wrapped), m.reviewOffset+available), len(wrapped)))))
		}
		choice := "[No]   Yes, create"
		if m.confirmYes {
			choice = " No   [Yes, create]"
		}
		body = append(body, strong.Render("Create endpoint now?"), active(choice))
		footer = "←→ choose · Enter confirm · Esc cancel"
		if width < 44 {
			footer = "←→ choose · Enter · Esc cancel"
		}
	}
	if m.note != "" {
		body = append(body, clip(m.note))
	}
	lines := []string{strong.Render("Create webhook endpoint")}
	lines = append(lines, strings.Split(panel.Width(width).Render(strings.Join(body, "\n")), "\n")...)
	lines = append(lines, quiet.Render(footer))
	if m.stage == 2 && height >= 12 {
		lines = append(lines, quiet.Render("Type to search · Shift+Tab back · Esc cancel"))
	}
	return m.frame(lines, width, height)
}

// Retain only the compact region this form has drawn. Padding goes after the
// nearby hints, so filtering and final frames erase owned rows without putting
// controls at the bottom of a tall terminal or clearing its scrollback.
func (m *webhookCreatePicker) frame(lines []string, width, height int) tea.View {
	m.ownedHeight = min(height, max(m.ownedHeight, len(lines)))
	if len(lines) > m.ownedHeight {
		lines = lines[:m.ownedHeight]
	}
	for len(lines) < m.ownedHeight {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	return tea.NewView(strings.Join(lines, "\n"))
}

func webhookWithoutCleanupCancellation(err error) error {
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
			causes = append(causes, webhookWithoutCleanupCancellation(cause))
		}
		return errors.Join(causes...)
	case interface{ Unwrap() error }:
		return webhookWithoutCleanupCancellation(wrapped.Unwrap())
	}
	return err
}

type webhookPickerOutput struct {
	clioutput.TerminalWriter
	stop context.CancelFunc
	mu   sync.Mutex
	err  error
}

func (w *webhookPickerOutput) Write(data []byte) (int, error) {
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

func (w *webhookPickerOutput) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *webhookPickerOutput) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}
