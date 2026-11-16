package batches

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
)

// Guided screens reuse the browser's terminal session, frame, editor, and palette.
type batchBrowserFlowState struct {
	csv        *batchBrowserCSV // CSV settings remain local until the user opens Review.
	localFiles bool
	mode       string
	lines      []string
	offset     int
	work       func() tea.Msg
	submit     bool               // Only the final batch review accepts Enter to submit.
	draft      *batchBrowserDraft // The create form applies inline settings to this local draft.
	setting    string
}

func newBatchBrowserFlow(mode, title, subtitle string, choices []batchBrowserChoice) *batchBrowserScreen {
	m := newBatchBrowserScreen("flow", title, subtitle, choices)
	m.flow = &batchBrowserFlowState{mode: mode}
	if mode == "create" || mode == "entry" || mode == "picker" {
		m.input.Focus()
		m.input.SetVirtualCursor(false)
		m.input.Placeholder = ""
	}
	return m
}

func (m *batchBrowserScreen) initFlow() tea.Cmd {
	if m.flow.work != nil {
		return m.flow.work
	}
	return nil
}

type batchBrowserWorkDone struct{ screen *batchBrowserScreen }

// The worker closes done before the controller reads its result, including on interruption.
func batchBrowserWork[T any](ctx context.Context, title string, show func(context.Context, *batchBrowserScreen) error, run func(context.Context) (T, error)) (T, error) {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newBatchBrowserFlow("busy", title, "Please wait. Ctrl+C interrupts this operation.", nil)
	var result T
	var workErr error
	done := make(chan struct{})
	go func() {
		result, workErr = run(workCtx)
		close(done)
	}()
	m.flow.work = func() tea.Msg {
		<-done
		return batchBrowserWorkDone{m}
	}
	err := show(ctx, m)
	if err != nil {
		cancel()
	}
	<-done
	if err != nil {
		return result, errors.Join(err, workErr)
	}
	return result, workErr
}

func (m *batchBrowserScreen) updateFlow(message tea.Msg) (tea.Model, tea.Cmd) {
	s := m.flow
	if done, ok := message.(batchBrowserWorkDone); ok {
		if done.screen == m {
			m.closed, m.chosen = true, "done"
			return m, tea.Quit
		}
		return m, nil
	}
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		return m, nil
	}
	if s.csv != nil {
		return m.updateCSVForm(message)
	}
	if s.draft != nil {
		if command, handled := m.updateCreateSetting(message); handled {
			return m, command
		}
	}
	inputRow := m.flowInputRow()
	editingPath := inputRow && m.input.Value() != ""
	if key, ok := message.(tea.KeyPressMsg); ok {
		if s.mode == "busy" {
			return m, nil
		}
		name := key.String()
		if s.localFiles {
			action := ""
			switch name {
			case "ctrl+l":
				action = "path"
			case "ctrl+r":
				action = "refresh"
			case "backspace":
				if m.input.Value() == "" {
					action = "parent"
				}
			}
			if action != "" {
				m.closed, m.chosen = true, action
				return m, tea.Quit
			}
		}
		if s.mode == "confirm" && (name == "enter" || name == "ctrl+s") {
			if !key.IsRepeat && (name == "ctrl+s" || s.submit) && m.width >= 40 && m.height >= 12 {
				m.closed, m.chosen = true, "confirm"
				return m, tea.Quit
			}
			return m, nil
		}
		switch name {
		case "esc":
			if editingPath {
				m.input.Reset()
				m.notice = ""
				return m, nil
			}
			fallthrough
		case "ctrl+c":
			m.closed, m.chosen = true, "back"
			return m, tea.Quit
		case "up", "shift+tab", "pgup":
			if editingPath {
				return m, nil
			}
			if s.mode == "confirm" {
				m.flow.offset = max(0, m.flow.offset-1)
				return m, nil
			}
			if s.mode != "entry" {
				m.index = max(0, m.index-1)
				return m, nil
			}
		case "down", "tab", "pgdown":
			if editingPath {
				return m, nil
			}
			if s.mode == "confirm" {
				m.flow.offset++
				return m, nil
			}
			if s.mode != "entry" {
				m.index = min(max(0, len(m.visible)-1), m.index+1)
				return m, nil
			}
		case "enter":
			if m.width < 30 || m.height < 7 {
				return m, nil
			}
			if s.localFiles && len(m.visible) == 0 && m.input.Value() != "" {
				// A pasted location can point outside the currently listed folder.
				m.closed, m.chosen = true, "typed-path"
				return m, tea.Quit
			}
			if s.mode == "entry" {
				m.closed, m.chosen = true, "value"
				return m, tea.Quit
			}
			if len(m.visible) > 0 {
				m.closed, m.chosen = true, m.choices[m.visible[m.index]].value
				return m, tea.Quit
			}
		}
	}
	if s.mode == "entry" || s.mode == "picker" || inputRow {
		previous := m.input.Value()
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		if inputRow && m.input.Value() != previous {
			m.notice = ""
		}
		if s.mode == "picker" && m.input.Value() != previous {
			m.visible = nil
			query := strings.ToLower(m.input.Value())
			for i, choice := range m.choices {
				if choice.search == "" || strings.Contains(strings.ToLower(choice.search), query) {
					m.visible = append(m.visible, i)
				}
			}
			m.index = 0
			if query == "" && s.localFiles && len(m.visible) > 1 && m.choices[0].value == "uploaded" {
				m.index = 1
			}
		}
		return m, command
	}
	return m, nil
}

func (m *batchBrowserScreen) flowInputRow() bool {
	return m.flow.mode == "create" && len(m.visible) != 0 && m.choices[m.visible[m.index]].value == "input"
}

func (m *batchBrowserScreen) flowKeys(shortcut func(string, string) string) []string {
	if m.flow.csv != nil {
		action := "next"
		switch m.choices[m.index].value {
		case "column":
			action = "choose"
		case "model", "review":
			action = "review"
		}
		return []string{shortcut("↑↓/Tab", "move"), shortcut("Enter", action), shortcut("Esc", "back")}
	}
	if m.flow.localFiles {
		if m.width < 50 || m.height < 12 {
			return []string{shortcut("Enter", "open"), shortcut("Ctrl+L", "path"), shortcut("Esc", "back")}
		}
		return []string{shortcut("↑↓", "move"), shortcut("Enter", "open"), shortcut("Backspace", "parent"), shortcut("Ctrl+L", "path"), shortcut("Esc", "back")}
	}
	if m.flow.setting == "metadata" || m.flow.setting == "custom" {
		return []string{shortcut("Enter", "apply"), shortcut("Esc", "cancel")}
	}
	if m.flow.setting == "expires" {
		return []string{shortcut("↑↓", "move"), shortcut("Enter", "choose"), shortcut("Esc", "collapse")}
	}
	if m.flowInputRow() && m.input.Value() != "" {
		return []string{shortcut("Enter", "use file"), shortcut("Esc", "discard path")}
	}
	if m.flow.draft != nil && len(m.visible) > 0 {
		action := "choose"
		switch m.choices[m.visible[m.index]].value {
		case "input":
			action = "browse files"
		case "metadata":
			action = "edit"
		case "expires":
			action = "expand"
		case "review":
			action = "review"
		}
		return []string{shortcut("↑↓/Tab", "move"), shortcut("Enter", action), shortcut("Esc", "back")}
	}
	switch m.flow.mode {
	case "entry":
		return []string{shortcut("Enter", "apply"), shortcut("Esc", "back")}
	case "confirm":
		if m.flow.submit {
			return []string{shortcut("↑↓", "scroll"), shortcut("Enter", "submit"), shortcut("Esc", "edit")}
		}
		return []string{shortcut("↑↓", "scroll"), shortcut("Ctrl+S", "confirm"), shortcut("Esc", "edit")}
	case "busy":
		return []string{shortcut("Ctrl+C", "interrupt")}
	default:
		return []string{shortcut("↑↓/Tab", "move"), shortcut("Enter", "choose"), shortcut("Esc", "back")}
	}
}

// A setting belongs to the row label. Keep both parts on the selection background.
func (m *batchBrowserScreen) flowRow(i, width, column, y int, muted, accent, highlight lipgloss.Style, colors bool) (string, *tea.Cursor) {
	choice := m.choices[m.visible[i]]
	selected := i == m.index
	csvHint := ""
	if m.flow.csv != nil && choice.setting == "" {
		switch choice.value {
		case "task":
			csvHint = "Describe the task"
		case "model":
			csvHint = "Enter a model name"
		}
	}
	labelStyle, valueStyle := lipgloss.NewStyle(), lipgloss.NewStyle()
	base := lipgloss.NewStyle()
	prefix := "  "
	if selected {
		base = highlight
		prefix = "> "
		labelStyle, valueStyle = accent.Inherit(base).Bold(colors), base
	} else if choice.setting != "" || csvHint != "" {
		labelStyle = muted
	} else if m.flow.localFiles && strings.HasSuffix(choice.title, string(filepath.Separator)) {
		labelStyle = accent
	} else if choice.value == "review" && m.flow.draft != nil {
		labelStyle = muted
		if m.flow.draft.path != "" || m.flow.draft.inputID != "" {
			labelStyle = accent
		}
	}

	label := batchInputLabel(choice.title)
	var cursor *tea.Cursor
	var value string
	editing := selected && (m.flowEditingRow() || m.flow.csv != nil && (choice.value == "task" || choice.value == "model"))
	if editing || choice.setting != "" || csvHint != "" {
		label = ansi.Truncate(label, column-1, "…")
		label += strings.Repeat(" ", max(0, column-ansi.StringWidth(label)))
		if editing {
			var x int
			value, x = m.searchView(width - column)
			cursor = tea.NewCursor(2+column+x, y)
			cursor.Shape, cursor.Blink = tea.CursorBar, true
			if m.input.Value() == "" && (choice.value != "input" || m.flow.draft == nil ||
				m.flow.draft.path == "" && m.flow.draft.inputID == "") {
				valueStyle = muted.Inherit(base)
			}
		} else if csvHint != "" {
			value = ansi.Truncate(csvHint, width-column, "…")
			valueStyle = muted
		} else {
			value = ansi.Truncate(batchInputLabel(choice.setting), width-column, "…")
			if choice.value == "metadata" && choice.setting == "None" || choice.value == "expires" && choice.setting == "Server default" {
				valueStyle = muted.Inherit(base)
			}
			if choice.value == "expires" {
				marker := " ▸"
				if m.flow.setting == "expires" || m.flow.setting == "custom" {
					marker = " ▾"
				}
				value = ansi.Truncate(value, width-column-2, "…") + marker
			}
		}
	} else {
		label = ansi.Truncate(label, width, "…")
	}
	line := base.Render(prefix) + labelStyle.Render(label) + valueStyle.Render(value)
	if selected {
		line += base.Render(strings.Repeat(" ", max(0, width-ansi.StringWidth(label)-ansi.StringWidth(value))))
	}
	return line, cursor
}

func (m *batchBrowserScreen) flowLines(height, width int, footer []string, rule func() string, muted, warning, accent, highlight lipgloss.Style, colors bool) ([]string, *tea.Cursor) {
	if height <= 10 && len(footer) > 1 && footer[0] == "" {
		footer = footer[1:]
	}
	title := ansi.Truncate(batchInputLabel(m.title), width, "…")
	heading := lipgloss.NewStyle().Bold(colors).Render(title)
	subtitle := m.subtitle
	if m.flow.csv != nil && width >= 50 {
		heading = muted.Render("Create batch / ") + heading
	}
	if m.flow.mode == "create" {
		window, folder, _ := strings.Cut(subtitle, "\nFolder: ")
		if window == "Completion window: 24 hours" {
			window = "24h window"
			if width >= 50 {
				heading = muted.Render("Batches / ") + heading
			}
			gap := width - ansi.StringWidth(heading) - ansi.StringWidth(window)
			if gap >= 2 {
				heading += strings.Repeat(" ", gap) + muted.Render(window)
			}
			subtitle = ""
			if folder != "" {
				if ansi.StringWidth(batchInputLabel(folder)) > min(40, width-8) {
					folder = "…" + string(filepath.Separator) + filepath.Base(folder)
				}
				subtitle = "Folder: " + folder
			}
		}
	}
	lines := []string{"  " + heading}
	wrap := func(text string) []string { return strings.Split(ansi.Wrap(text, width, ""), "\n") }
	headerBudget := max(0, height-len(footer)-4)
	var notices []string
	if m.notice != "" {
		notices = wrap(batchInputLabel(m.notice))
		notices = notices[:min(len(notices), headerBudget)]
	}
	if available := headerBudget - len(notices); available > 0 && subtitle != "" {
		if folder, ok := strings.CutPrefix(subtitle, "Folder: "); ok {
			prefix := "Folder: "
			location := batchInputLabel(folder)
			if excess := ansi.StringWidth(location) + ansi.StringWidth(prefix) - width; excess > 0 {
				for excess >= 0 {
					_, cells, size := terminal.Sequence(location)
					location = location[size:]
					excess -= cells
				}
				location = "…" + location
			}
			lines = append(lines, "  "+muted.Render(prefix)+location)
		} else {
			text := wrap(batchInputLabel(subtitle))
			if len(text) > available {
				text = text[:available]
				text[available-1] = ansi.Truncate(text[available-1]+"…", width, "…")
			}
			for _, line := range text {
				lines = append(lines, "  "+muted.Render(line))
			}
		}
	}
	for _, line := range notices {
		lines = append(lines, "  "+warning.Render(line))
	}
	lines = append(lines, rule())
	var cursor *tea.Cursor
	if m.flow.mode == "entry" || m.flow.mode == "picker" {
		prompt := m.input.Prompt
		value, x := m.searchView(max(1, width-ansi.StringWidth(prompt)))
		cursor = tea.NewCursor(2+ansi.StringWidth(prompt)+x, len(lines))
		cursor.Shape, cursor.Blink = tea.CursorBar, true
		if m.input.Value() == "" {
			value = muted.Render(value)
		}
		if m.flow.mode == "entry" {
			prompt = accent.Render(prompt)
		}
		lines = append(lines, "  "+prompt+value)
	}
	budget := max(0, height-len(lines)-len(footer))
	if m.flow.mode == "confirm" {
		var body []string
		for _, line := range m.flow.lines {
			text := batchInputLabel(line)
			if key, value, ok := strings.Cut(text, ": "); ok {
				if m.flow.submit && key == "Requests" {
					value = accent.Bold(colors).Render(value)
				}
				text = muted.Render(key+": ") + value
			} else if m.flow.submit && text == "Creating a batch can incur API charges." {
				text = warning.Render(text)
			}
			body = append(body, wrap(text)...)
		}
		if m.width < 40 || m.height < 12 {
			body = wrap(warning.Render("Resize to 40×12 to review and confirm."))
		}
		m.flow.offset = min(m.flow.offset, max(0, len(body)-budget))
		for _, line := range body[m.flow.offset:min(len(body), m.flow.offset+budget)] {
			lines = append(lines, "  "+line)
		}
	} else {
		column := 0
		if m.flow.mode == "create" || m.flow.csv != nil {
			for _, index := range m.visible {
				row := m.choices[index]
				if row.setting != "" || row.value == "input" {
					column = max(column, ansi.StringWidth(row.title)+2)
				}
			}
			column = min(column, width-6)
		}
		detail := ""
		if len(m.visible) > 0 {
			detail = m.choices[m.visible[m.index]].detail
		} else if m.flow.localFiles && budget > 0 {
			lines = append(lines, "  "+muted.Render(ansi.Truncate("No matching files or folders.", width, "…")))
		}
		var details []string
		if detail != "" && budget >= 4 {
			details = wrap(batchInputLabel(detail))
			details = details[:min(len(details), budget/2)]
		}
		rows := max(0, budget-len(details))
		start := max(0, m.index-rows+1)
		create := m.flow.draft != nil && m.flow.mode == "create"
		extra := rows - len(m.visible)
		formats := create && m.flow.draft.inputID == "" && m.flow.draft.path == "" && extra >= 3 && width >= 30
		options := create && extra >= 1
		for i := start; i < len(m.visible) && i < start+rows; i++ {
			choice := m.choices[m.visible[i]]
			if options && (choice.value == "endpoint" || choice.value == "csv-settings") {
				if extra >= 4 || !formats && extra >= 2 {
					lines = append(lines, "")
				}
				lines = append(lines, "  "+accent.Bold(colors).Render("Options"))
			}
			line, rowCursor := m.flowRow(i, width, column, len(lines), muted, accent, highlight, colors)
			lines = append(lines, line)
			if rowCursor != nil {
				cursor = rowCursor
			}
			if formats && choice.value == "input" {
				if extra >= 5 {
					lines = append(lines, "")
				}
				for _, format := range []struct{ name, meaning string }{{"JSONL", "One prepared API request per line."}, {"CSV", "Turn each spreadsheet row into a request."}} {
					label := "  " + format.name
					padding := strings.Repeat(" ", max(1, column-ansi.StringWidth(label)))
					description := ansi.Truncate(format.meaning, width-column, "…")
					lines = append(lines, "  "+accent.Render(label)+padding+muted.Render(description))
				}
			}
		}
		if len(details) > 0 && len(lines)+len(details)+len(footer) < height {
			lines = append(lines, "")
		}
		for _, line := range details {
			lines = append(lines, "  "+muted.Render(line))
		}
	}
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	lines = append(lines, footer...)
	return lines[:min(height, len(lines))], cursor
}
