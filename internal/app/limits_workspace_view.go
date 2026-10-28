package app

import (
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

func (m *limitsWorkspace) View() tea.View {
	view := tea.NewView("")
	if m.quitting {
		return view
	}
	if m.small() {
		view.Content = "Resize to at least 40 x 12.\nCtrl+C: quit"
		return view
	}
	width, height := m.panelWidth()-2, m.panelHeight()
	accent := lipgloss.NewStyle()
	if m.color {
		color := "#3159BC"
		if m.dark {
			color = "#8AA8FF"
		}
		accent = accent.Foreground(lipgloss.Color(color))
	}
	line := func(s string) string { return ansi.Truncate(limitsSafeLine(s), width, "…") }
	mark := func(s string, selected bool) string {
		if selected {
			return accent.Render("> " + line(s))
		}
		return "  " + line(s)
	}
	header := accent.Render(line(m.heading() + " · " + m.scope()))
	var body []string
	footer := "Esc: back  Ctrl+C: quit"
	switch m.page {
	case "home":
		sections := []string{"Spend", "Alerts"}
		if m.snapshot.ProjectID != "" {
			sections = append(sections, "Rates")
		}
		for i := range sections {
			prefix := "  "
			if i == m.section {
				prefix = "> "
				sections[i] = accent.Render(prefix + sections[i])
			} else {
				sections[i] = prefix + sections[i]
			}
		}
		body = append(body, strings.Join(sections, "   "), line(m.sourceSummary()), line(m.costsSummary()))
		enforcement := ""
		if m.section == 0 && m.snapshot.Limit.Exists() {
			state := m.snapshot.Limit.Get("enforcement.status")
			status := "not reported"
			if limitsUniqueJSON(m.snapshot.Limit) != nil {
				status = "ambiguous; inspect original"
			} else if state.Type == gjson.String {
				status = state.String()
			}
			enforcement = line("Returned enforcement: " + status)
		}
		body = append(body, enforcement)
		rows := m.menu()
		// Reserve the same menu rows across tabs, without filling the terminal.
		room := max(2, len(m.snapshot.Alerts)+1)
		if m.snapshot.ProjectID != "" {
			room = max(room, len(m.snapshot.Rates))
		}
		budget := height - 10
		if m.locked {
			budget--
		}
		room = max(1, min(room, budget))
		start := max(0, m.selection-room+1)
		for i := start; i < min(len(rows), start+room); i++ {
			body = append(body, mark(rows[i].label, i == m.selection))
		}
		if len(rows) == 0 {
			body = append(body, "No editable records loaded.")
		}
		for i := max(1, min(len(rows), room)); i < room; i++ {
			body = append(body, "")
		}
		body = append(body, line(m.note))
		if m.locked {
			body = append(body, "Writes locked. Inspect before retrying.")
		}
		footer = "←/→ section  ↑/↓ select  Enter open\nr refresh  i inspect  d delete alert\nq quit  ? help  Ctrl+C interrupt"
	case "form":
		body = append(body, "Tab: next field · Shift+Tab: previous")
		count := len(m.draft.inputs)
		var notice []string
		if m.formError != "" {
			first := limitsSafeLine(strings.SplitN(m.formError, "\n", 2)[0])
			notice = strings.Split(ansi.Wrap(first, width, ""), "\n")
			if len(notice) > 2 {
				notice = []string{notice[0], ansi.Truncate(strings.Join(notice[1:], " "), width, "…")}
			}
		}
		// Reserve navigation, Review, diagnostics, and the optional prefix action before fields.
		fieldRows := height - 4 - 2 - len(notice)
		if count == 3 {
			fieldRows--
		}
		unavailable := m.draft.hasUnavailableEdits()
		if unavailable {
			fieldRows--
		}
		var fields []int
		position := 0
		for i := range m.draft.inputs {
			if m.draft.inputAvailable(i) {
				if i <= m.focus {
					position = len(fields)
				}
				fields = append(fields, i)
			}
		}
		if len(fields) < count {
			fieldRows--
		}
		visible := max(1, fieldRows/2)
		start := max(0, position-visible+1)
		end := min(len(fields), start+visible)
		for _, i := range fields[start:end] {
			m.draft.inputs[i].SetWidth(max(1, width-4))
			text := m.draft.labels[i]
			if m.draft.change.Kind == "rate_update" {
				text = limitsRateInputs[i].label + " (blank: unchanged)"
			}
			marker, label := "  ", line(text)
			if i == m.focus {
				marker = "> "
				label = accent.Render(label)
			}
			body = append(body, label, marker+ansi.Truncate(limitsInputView(m.draft.inputs[i]), width, "…"))
		}
		if len(fields) < count {
			body = append(body, line("Other rate fields were not returned."))
		}
		if count == 3 {
			modes := []string{"Keep unless edited", "Replace (empty is allowed)", "Clear to null"}
			body = append(body, mark("Prefix: "+modes[m.draft.prefixMode], m.focus == count))
		}
		if unavailable {
			body = append(body, mark("Discard unavailable edits", m.focus == count))
		}
		body = append(body, mark("Review change", m.focus >= count && !((count == 3 || unavailable) && m.focus == count)))
		body = append(body, notice...)
		footer = "Enter: next / select\nF1: help  Esc: back  Ctrl+C: quit"
		if m.refreshRequired {
			footer = "r: refresh  Enter: next field\nF1: help  Esc: back  Ctrl+C: quit"
		}
	case "review", "details":
		body = append(body, m.viewport.View())
		if m.page == "review" {
			back, apply := "> Back", "  Apply change"
			if m.apply {
				back, apply = "  Back", "> Apply change"
			}
			if !m.viewport.AtBottom() {
				apply = "  Read to the end before applying"
			}
			body = append(body, line(back+"   "+apply))
			footer = "↑/↓ scroll  Tab action  Enter: select\nF1: help  Esc: back  Ctrl+C: quit"
			if m.previewCommand != "" {
				footer = "↑/↓ scroll  Tab action  Enter: select\nCtrl+P: print command and exit\nF1: help  Esc: back  Ctrl+C: quit"
			}
		} else {
			footer = "↑/↓: scroll  Esc: back  Ctrl+C: quit"
		}
	}
	if m.busy {
		status := "Loading…"
		if m.page == "review" {
			status = "Saving… Do not repeat this change."
		}
		footer = line(status) + "\nCtrl+C: interrupt"
	}
	// The action row and controls occupy reserved rows. Long data stays in the viewport.
	footerRows := strings.Count(footer, "\n") + 1
	available := max(0, height-2-footerRows)
	text := strings.Join(body, "\n")
	rows := strings.Split(text, "\n")
	if len(rows) > available {
		rows = rows[:available]
	}
	controls := strings.Split(footer, "\n")
	for i := range controls {
		controls[i] = line(controls[i])
	}
	view.Content = header + "\n" + strings.Join(rows, "\n") + "\n\n" + strings.Join(controls, "\n")
	return view
}

// Escape only a display copy, before the input applies cursor and overflow layout.
func limitsInputView(input textinput.Model) string {
	value := input.Value()
	escaped := readable.Text(value)
	if escaped != value {
		position := input.Position()
		prefix := string([]rune(value)[:position])
		input.Reset()
		input.SetValue(escaped)
		input.SetCursor(utf8.RuneCountInString(readable.Text(prefix)))
	}
	return input.View()
}

// Keep external text on one row after escaping terminal controls.
func limitsSafeLine(value string) string {
	return strings.NewReplacer("\n", " ↵ ", "\t", " → ").Replace(readable.Text(value))
}
