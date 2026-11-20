package webhooks

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

func webhookCreateLine(text string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(readable.Text(text))
}

func (i *webhookCreateInput) viewport(width int, placeholder string) (string, int) {
	if width <= 0 {
		return "", 0
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
	return before + ansi.Truncate(after, afterRoom, "…") + " ", ansi.StringWidth(before)
}

func (m *webhookCreatePicker) View() tea.View {
	width, height := max(1, min(76, m.width-2)), max(1, min(18, m.height-1))
	if m.canceled {
		return m.frame([]string{"Webhook creation canceled."}, width, height)
	}
	if m.confirmed {
		return m.frame([]string{"Creating webhook endpoint..."}, width, height)
	}
	if m.fullScreen {
		width, height = max(1, m.width), max(1, m.height)
	}
	if m.width < 36 || m.height < 10 {
		return m.frame([]string{"Resize to 36 x 10.", "Esc or Ctrl+C cancels."}, width, height)
	}
	if m.browser != nil && m.browser.active {
		return m.endpointView(width, height)
	}
	accent, muted, border := "#3159BC", "#657087", "#BAC4D8"
	if m.dark {
		accent, muted, border = "#8AA8FF", "#A4ACC2", "#51566B"
	}
	strong, quiet, selected := lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle()
	rule := lipgloss.NewStyle()
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if m.color {
		strong = strong.Bold(true).Foreground(lipgloss.Color(accent))
		quiet = quiet.Foreground(lipgloss.Color(muted))
		selected = selected.Foreground(lipgloss.Color(accent))
		panel = panel.BorderForeground(lipgloss.Color(border))
		rule = rule.Foreground(lipgloss.Color(border))
	}
	inner := width - 4 // Border and one padding cell on each side.
	clip := func(s string) string { return ansi.Truncate(s, inner, "…") }
	active := func(s string) string { return strong.Render(clip(s)) }
	// Narrow footers wrap; omit framing in short windows to retain every control.
	compact := height < 14 || height < 16 && width < 68
	if compact && m.stage < 3 {
		inner = width // Short terminals omit decoration, not fields or controls.
	}
	valueColumn, panelSplit, separator := 9, 0, " "
	if !compact && m.stage < 3 {
		valueColumn, panelSplit, separator = 11, 11, rule.Render(" │ ")
	}
	body := []string{}
	dividers := map[int]string{}
	divide := func(junction string) {
		dividers[len(body)] = junction
		body = append(body, "")
	}
	var cursor *tea.Cursor
	var controls []string
	cursorAt := func(column, row int) {
		x, y := column, 1+row
		if !compact {
			x += 2
			y++
		}
		cursor = tea.NewCursor(x, y)
		cursor.Shape = tea.CursorBar
		cursor.Blink = true
	}
	switch m.stage {
	case 0, 1, 2:
		field := func(label string, input *webhookCreateInput, placeholder string, focused bool, row int) string {
			label = fmt.Sprintf("%-6s", label)
			if focused {
				value, column := input.viewport(inner-valueColumn, placeholder)
				cursorAt(valueColumn+column, row)
				return strong.Render("› "+label) + separator + value
			}
			value := webhookCreateLine(string(input.value))
			if value == "" {
				value = quiet.Render(ansi.Truncate(placeholder, inner-valueColumn, "…"))
			} else {
				value = ansi.Truncate(value, inner-valueColumn, "…")
			}
			return "  " + selected.Render(label) + separator + value
		}
		body = append(body, field("Name", &m.name, "Webhook", m.stage == 0, 0),
			field("URL", &m.url, "https://example.com/webhook", m.stage == 1, 1))
		if !compact {
			divide("┴")
		}
		eventLabel := selected.Render("  Events")
		if m.stage == 2 && !m.reviewFocused && m.eventFocus == webhookCreateCategory {
			eventLabel = strong.Render("› Events")
		}
		categoryPrefix := eventLabel + "  "
		category := ansi.Truncate(m.categories()[m.category], inner-ansi.StringWidth(categoryPrefix)-4, "…")
		body = append(body, categoryPrefix+"‹ "+category+" ›")
		finding := m.stage == 2 && !m.reviewFocused && m.eventFocus == webhookCreateSearch
		marker := "  "
		if finding {
			marker = strong.Render("› ")
		}
		prompt := marker + selected.Render("/") + " " + lipgloss.NewStyle().Bold(m.color).Render("Find event") + "  "
		start := ansi.StringWidth(prompt)
		query, offset := m.search.viewport(inner-start, "")
		if finding && len(m.search.value) == 0 {
			query = quiet.Render(ansi.Truncate("Event or group name", inner-start, "…"))
		}
		if finding {
			cursorAt(start+offset, len(body))
		}
		body = append(body, prompt+query)
		if !compact {
			divide("─")
		}
		matches := m.filteredEvents()
		bindings := []string{"↑↓ move", "Tab next", "Esc cancel"}
		if m.reviewFocused {
			enter := "Enter continue"
			if m.existingFocused {
				enter = "Enter open"
			}
			bindings = []string{"←→ action", enter, "↑ back", "Esc cancel"}
			if m.browser == nil {
				bindings = []string{enter, "↑ back", "Esc cancel"}
			} else if compact {
				bindings = []string{"←→", enter, "Esc cancel"}
			}
		} else if m.stage == 2 {
			switch m.eventFocus {
			case webhookCreateCategory:
				bindings = []string{"←→ category", "↑↓ move", "Esc cancel"}
			case webhookCreateSearch:
				bindings = []string{"↑↓ move", "Tab next", "Esc cancel"}
			case webhookCreateChoices:
				move := "↑↓ move"
				if inner < 55 {
					move = "↑↓"
				}
				bindings = []string{move, "Space select", "Esc cancel"}
				if compact && m.note != "" {
					bindings = []string{"Space select", "Esc cancel"}
				}
			}
		}
		if m.stage == 2 && !m.reviewFocused && len(m.search.value) > 0 {
			bindings[len(bindings)-1] = "Esc clear"
		}
		controls = m.controls(inner, selected, quiet, bindings...)
		// Reserve complete controls before sizing the event viewport.
		overhead, tail := 1, 1+len(controls)
		if !compact {
			overhead += 2
			tail++
		}
		if m.note != "" {
			tail++
		}
		available := max(1, height-overhead-len(body)-tail)
		m.current = min(m.current, max(0, len(matches)-1))
		rows := []string{}
		headingRows := map[int]bool{}
		currentRow := 0
		lastGroup := ""
		needle := strings.ToLower(string(m.search.value))
		for index, event := range matches {
			_, group := webhookCreateEventGroup(event)
			if !compact && needle == "" && m.category == 0 && group != lastGroup {
				headingRows[len(rows)] = true
				rows = append(rows, lipgloss.NewStyle().Bold(m.color).Render(clip("  "+group)))
				lastGroup = group
			}
			marker, tick := " ", " "
			if index == m.current {
				currentRow = len(rows)
				if m.stage == 2 && !m.reviewFocused && m.eventFocus == webhookCreateChoices {
					marker = "›"
				}
			}
			if m.selected[event] {
				tick = "x"
			}
			name := ansi.Truncate(webhookCreateLine(event), inner-6, "…")
			checkbox := marker + " [" + tick + "] "
			emphasized := m.selected[event] || index == m.current && m.stage == 2 && !m.reviewFocused && m.eventFocus == webhookCreateChoices
			if emphasized {
				checkbox = selected.Render(checkbox)
			}
			if m.color && needle != "" {
				folded := strings.ToLower(name)
				if at := strings.Index(folded, needle); at >= 0 {
					// Case folding can change the byte length of an event name.
					first := utf8.RuneCountInString(folded[:at])
					last := first + utf8.RuneCountInString(needle)
					letters := []rune(name)
					style := lipgloss.NewStyle()
					if emphasized {
						style = selected
					}
					text := style.Render(string(letters[:first])) + style.Bold(true).Render(string(letters[first:last])) + style.Render(string(letters[last:]))
					rows = append(rows, checkbox+text)
					continue
				}
			}
			if marker == "›" {
				rows = append(rows, checkbox+strong.Render(name))
				continue
			}
			leaf := name
			if dot := strings.LastIndex(name, "."); dot >= 0 {
				leaf = name[dot+1:]
				name = quiet.Render(name[:dot+1])
			} else {
				name = ""
			}
			if emphasized {
				leaf = selected.Render(leaf)
			}
			row := checkbox + name + leaf
			rows = append(rows, row)
		}
		m.offset = min(m.offset, max(0, len(rows)-available))
		if currentRow < m.offset {
			m.offset = max(0, currentRow-1)
		} else if currentRow >= m.offset+available {
			m.offset = currentRow - available + 1
		}
		if len(rows) == 0 {
			body = append(body, clip("No matches. Edit the search."))
		} else {
			end := min(len(rows), m.offset+available)
			if headingRows[end-1] {
				end-- // Keep each group heading with an event choice.
			}
			body = append(body, rows[m.offset:end]...)
		}
		if m.fullScreen {
			// All spare height belongs to the event list, above the fixed action row.
			spare := height - overhead - tail - len(body)
			body = append(body, make([]string, max(0, spare))...)
		}
		if !compact {
			divide("─")
		}
		action := "  [ Review ]"
		if m.reviewFocused && !m.existingFocused {
			action = active("›" + action[1:])
		} else {
			action = selected.Render(action)
		}
		if m.browser != nil {
			label := "  [ Existing webhooks ]"
			if inner < 55 {
				label = "  [ Webhooks ]"
			}
			if m.reviewFocused && m.existingFocused {
				label = active("›" + label[1:])
			} else {
				label = selected.Render(label)
			}
			action += "  " + label
		}
		body = append(body, action)
		if m.note != "" {
			body = append(body, clip(m.note))
		}
	case 3:
		settings := m.settings()
		details := []string{"Name: " + webhookCreateLine(settings.name), "URL: " + webhookCreateLine(settings.url), "Events:"}
		for _, event := range settings.events {
			details = append(details, "  "+webhookCreateLine(event))
		}
		wrapped := strings.Split(ansi.Hardwrap(strings.Join(details, "\n"), inner, false), "\n")
		controls = m.controls(inner, selected, quiet, "←→ choose", "Enter confirm", "Esc cancel")
		available := max(1, height-5-len(controls))
		if m.note != "" {
			available = max(1, available-1)
		}
		if len(wrapped) > available {
			available = max(1, available-1)
		}
		m.reviewOffset = min(m.reviewOffset, max(0, len(wrapped)-available))
		body = append(body, wrapped[m.reviewOffset:min(len(wrapped), m.reviewOffset+available)]...)
		if len(wrapped) > available {
			body = append(body, quiet.Render(clip(fmt.Sprintf("↑↓ settings %d-%d/%d", m.reviewOffset+1, min(len(wrapped), m.reviewOffset+available), len(wrapped)))))
		}
		choice := strong.Render("[No]") + quiet.Render("   Yes, create")
		if m.confirmYes {
			choice = quiet.Render(" No   ") + strong.Render("[Yes, create]")
		}
		body = append(body, lipgloss.NewStyle().Bold(m.color).Render("Create endpoint now?"), choice)
	}
	if m.stage == 3 && m.note != "" {
		body = append(body, clip(m.note))
	}
	heading := lipgloss.NewStyle().Bold(m.color)
	lines := []string{heading.Render("Create webhook endpoint")}
	if compact && m.stage < 3 {
		lines = append(lines, body...)
	} else {
		lines = webhookCreatePanel(body, width, panelSplit, dividers, panel, rule, "Create webhook endpoint", heading)
	}
	lines = append(lines, controls...)
	view := m.frame(lines, width, height)
	view.Cursor = cursor
	return view
}

// Keep key/action pairs together instead of clipping controls in narrow terminals.
func (m *webhookCreatePicker) controls(width int, keys, quiet lipgloss.Style, bindings ...string) []string {
	var rows []string
	line := "  "
	for _, binding := range bindings {
		key, label, _ := strings.Cut(binding, " ")
		part := keys.Render(key)
		if label != "" {
			part += quiet.Render(" " + label)
		}
		gap := ""
		if line != "  " {
			gap = quiet.Render(" · ")
		}
		if ansi.StringWidth(line+gap+part) > width && line != "  " {
			rows = append(rows, line)
			line, gap = "  ", ""
		}
		line += gap + part
	}
	label := "expand"
	if m.fullScreen {
		label = "compact"
	}
	toggle := keys.Render("Ctrl+G") + quiet.Render(" "+label)
	gap := width - ansi.StringWidth(line) - ansi.StringWidth(toggle)
	if gap >= 2 {
		line += strings.Repeat(" ", gap) + toggle
	}
	return append(rows, line)
}

// Connect section rules to the outer frame without changing content row geometry.
func webhookCreatePanel(rows []string, width, split int, dividers map[int]string, panel, rule lipgloss.Style, title string, heading lipgloss.Style) []string {
	content := heading.Render(title) + "\n" + strings.Join(rows, "\n")
	lines := strings.Split(panel.Width(width).Render(content), "\n")
	line := func(left, junction, right string) string {
		middle := strings.Repeat("─", width-2)
		if split > 0 && split < width-1 {
			middle = strings.Repeat("─", split-1) + junction + strings.Repeat("─", width-split-2)
		}
		return rule.Render(left + middle + right)
	}
	for row, junction := range dividers {
		lines[row+2] = line("├", junction, "┤")
	}
	return lines
}

// Retain only the compact region this form has drawn. Padding goes after the
// contextual controls, so filtering and final frames erase owned rows without putting
// controls at the bottom of a tall terminal or clearing its scrollback.
func (m *webhookCreatePicker) frame(lines []string, width, height int) tea.View {
	if m.fullScreen && !m.canceled && !m.confirmed {
		// Expand in the primary buffer so switching views preserves shell history.
		return tea.NewView(strings.Join(lines[:min(len(lines), height)], "\n"))
	}
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
