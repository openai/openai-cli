package custom

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

func (m *limitsWorkspace) View() tea.View {
	view := tea.NewView("")
	view.AltScreen = true
	if m.quitting {
		return view
	}
	if m.small() {
		view.Content = "Resize to at least 40 x 12.\nCtrl+C: quit"
		return view
	}
	width := m.width - 2
	accent := lipgloss.NewStyle().Bold(true)
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
			if i == m.section {
				sections[i] = accent.Render("[" + sections[i] + "]")
			}
		}
		body = append(body, strings.Join(sections, "   "), line(m.sourceSummary()), line(m.costsSummary()))
		if m.section == 0 && m.snapshot.Limit.Exists() {
			state := m.snapshot.Limit.Get("enforcement.status")
			status := "not reported"
			if limitsUniqueJSON(m.snapshot.Limit) != nil {
				status = "ambiguous; inspect original"
			} else if state.Type == gjson.String {
				status = state.String()
			}
			body = append(body, line("Returned enforcement: "+status))
		}
		rows := m.menu()
		room := max(1, m.height-6-len(body))
		if m.locked {
			room = max(1, room-1)
		}
		start := max(0, m.selection-room+1)
		for i := start; i < min(len(rows), start+room); i++ {
			body = append(body, mark(rows[i].label, i == m.selection))
		}
		if len(rows) == 0 {
			body = append(body, "No editable records loaded.")
		}
		body = append(body, line(m.note))
		if m.locked {
			body = append(body, "Writes locked. Inspect before retrying.")
		}
		footer = "←/→: section  ↑/↓: select  Enter: edit\nr: refresh  i: inspect  d: delete alert\nq: quit  ?: help  Ctrl+C: interrupt"
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
		fieldRows := m.height - 4 - 2 - len(notice)
		if count == 3 {
			fieldRows--
		}
		visible := max(1, fieldRows/2)
		start := max(0, min(m.focus, count-1)-visible+1)
		end := min(count, start+visible)
		for i := start; i < end; i++ {
			m.draft.inputs[i].SetWidth(max(1, width-4))
			marker, label := "  ", line(m.draft.labels[i])
			if i == m.focus {
				marker = "> "
				label = accent.Render(label)
			}
			body = append(body, label, marker+ansi.Truncate(m.draft.inputs[i].View(), width, "…"))
		}
		if count == 3 {
			modes := []string{"Keep unless edited", "Replace (empty is allowed)", "Clear to null"}
			body = append(body, mark("Prefix: "+modes[m.draft.prefixMode], m.focus == count))
		}
		body = append(body, mark("Review change", m.focus >= count && !(count == 3 && m.focus == count)))
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
			footer = "↑/↓: scroll  Tab: action  Enter: select\nF1: help  Esc: back  Ctrl+C: quit"
			if m.previewCommand != "" {
				footer = "↑/↓: scroll  Tab: action  Enter: select\nCtrl+P: print command and exit\nF1: help  Esc: back  Ctrl+C: quit"
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
	available := max(0, m.height-2-footerRows)
	text := strings.Join(body, "\n")
	rows := strings.Split(text, "\n")
	if len(rows) > available {
		rows = rows[:available]
	}
	view.Content = header + "\n" + strings.Join(rows, "\n") + strings.Repeat("\n", max(1, available-len(rows)+1)) + footer
	return view
}

// Keep external text on one row after escaping terminal controls.
func limitsSafeLine(value string) string {
	return strings.NewReplacer("\n", " ↵ ", "\t", " → ").Replace(readable.Text(value))
}
