package batches

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/tidwall/gjson"
)

// The action menu keeps its batch overview and local save choices across visits.
type batchBrowserActionsState struct {
	overview              gjson.Result
	csvSource, saveFolder string
	saveDrafts            map[string]batchBrowserSaveDraft
	saveExpanded          bool
}

func newBatchBrowserActions(id string) *batchBrowserScreen {
	m := newBatchBrowserScreen("actions", id, "", nil)
	m.actionsState = &batchBrowserActionsState{}
	m.setOverview(gjson.Result{})
	return m
}

func (m *batchBrowserScreen) setOverview(batch gjson.Result) {
	selected := ""
	if m.index >= 0 && m.index < len(m.visible) {
		selected = m.choices[m.visible[m.index]].value
	}
	m.actionsState.overview = batch
	m.choices = []batchBrowserChoice{{value: "output", title: "View results"}}
	joinedCSV := m.actionsState.csvSource != "" && batchBrowserCanExportCSV(batch)
	filesTitle := "Save a file"
	if joinedCSV {
		m.choices[0].title = "View rows with answers"
		filesTitle = "Files"
	}
	if !batchBrowserSuccessful(batch) {
		m.choices = append(m.choices, batchBrowserChoice{value: "error", title: "View errors"})
	}
	m.choices = append(m.choices,
		batchBrowserChoice{value: "input", title: "View input"},
		batchBrowserChoice{value: "status", title: "Batch details"},
		batchBrowserChoice{value: "download", title: filesTitle})
	if m.actionsState.saveExpanded {
		m.choices = append(m.choices,
			batchBrowserChoice{value: "save:output", title: "Output file"},
			batchBrowserChoice{value: "save:error", title: "Error file"},
			batchBrowserChoice{value: "save:input", title: "Input file"})
		if joinedCSV {
			m.choices = append(m.choices, batchBrowserChoice{value: "raw-output", title: "View API results"})
		}
	}
	m.filter()
	for i, choice := range m.choices {
		if choice.value == selected {
			m.index = i
			break
		}
	}
}

func (m *batchBrowserScreen) setSaveExpanded(expanded bool) {
	m.actionsState.saveExpanded = expanded
	m.setOverview(m.actionsState.overview)
	selected := "download"
	if expanded {
		selected = "save:output"
	}
	for i, choice := range m.choices {
		if choice.value == selected {
			m.index = i
			break
		}
	}
}

func (m *batchBrowserScreen) actionHint(action string) (string, string) {
	selection := strings.TrimPrefix(action, "save:")
	switch selection {
	case "status":
		return m.actionsState.overview.Get("endpoint").Str, ""
	case "output", "raw-output":
		if action == "output" && m.actionsState.csvSource != "" && batchBrowserCanExportCSV(m.actionsState.overview) {
			return filepath.Base(m.actionsState.csvSource), ""
		}
		if m.actionsState.overview.Get("output_file_id").Str != "" {
			return "Output ready", "positive"
		}
	case "error":
		if diagnostics := m.actionsState.overview.Get("errors"); diagnostics.Exists() && !batchBrowserNoValidationErrors(diagnostics) && action == "error" {
			return "Check diagnostics", "warning"
		}
		if m.actionsState.overview.Get("error_file_id").Str != "" {
			return "Error file ready", batchBrowserCountTone(m.actionsState.overview, "failed")
		}
	case "input":
		if m.actionsState.overview.Get("input_file_id").Str != "" {
			return "Request JSONL", ""
		}
	default:
		return "", ""
	}
	if selection == "output" || selection == "raw-output" || selection == "error" {
		if batchBrowserProcessing(m.actionsState.overview) {
			return "No file yet", ""
		}
		if batchCSVTerminalStatus(m.actionsState.overview.Get("status").Str) {
			return "No file", ""
		}
	}
	return "", ""
}

func batchBrowserSuccessful(batch gjson.Result) bool {
	file := batch.Get("error_file_id")
	succeeded, succeededOK := batchCount(batch.Get("request_counts.completed"))
	failed, failedOK := batchCount(batch.Get("request_counts.failed"))
	total, totalOK := batchCount(batch.Get("request_counts.total"))
	return batchBrowserUniqueFields(batch) && batchBrowserUniqueFields(batch.Get("request_counts")) &&
		batch.Get("status").Str == "completed" && succeededOK && failedOK && totalOK &&
		total > 0 && succeeded == total && failed == 0 && file.Exists() &&
		(file.Type == gjson.Null || file.Type == gjson.String && file.Str == "") && batchBrowserNoValidationErrors(batch.Get("errors"))
}

func batchBrowserUniqueFields(object gjson.Result) bool {
	if !object.IsObject() {
		return false
	}
	seen := make(map[string]bool)
	unique := true
	object.ForEach(func(key, _ gjson.Result) bool {
		unique = !seen[key.Str]
		seen[key.Str] = true
		return unique
	})
	return unique
}

// Missing or unfamiliar diagnostics remain inspectable. Only explicit absence hides View errors.
func batchBrowserNoValidationErrors(errors gjson.Result) bool {
	if !errors.Exists() {
		return false
	}
	if errors.Type == gjson.Null {
		return true
	}
	if !errors.IsObject() {
		return false
	}
	valid, dataSeen, objectSeen := true, false, false
	errors.ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "data":
			valid = !dataSeen && value.IsArray() && value.Get("#").Int() == 0
			dataSeen = true
		case "object":
			valid = !objectSeen && value.Type == gjson.String && value.Str == "list"
			objectSeen = true
		default:
			valid = false
		}
		return valid
	})
	return valid && dataSeen
}

func (m *batchBrowserScreen) overviewText(separator string, width int, render func(string, string) string) string {
	status := batchBrowserStatusLabel(m.actionsState.overview.Get("status").Str)
	fields := []string{render(ansi.Truncate(batchInputLabel(status), width, "…"), batchBrowserStatusTone(m.actionsState.overview))}
	succeeded, succeededOK := batchCount(m.actionsState.overview.Get("request_counts.completed"))
	failed, failedOK := batchCount(m.actionsState.overview.Get("request_counts.failed"))
	total, totalOK := batchCount(m.actionsState.overview.Get("request_counts.total"))
	consistent := succeededOK && failedOK && totalOK && succeeded <= total && failed <= total-succeeded
	if m.actionsState.overview.Get("status").Str == "completed" {
		consistent = consistent && failed == total-succeeded
	}
	if separator != ":" && consistent {
		fields = append(fields, render(fmt.Sprint(succeeded), batchBrowserCountTone(m.actionsState.overview, "completed"))+
			render(" of "+fmt.Sprint(total)+" succeeded", ""))
		if failed > 0 {
			fields = append(fields, render(fmt.Sprint(failed), "negative")+render(" failed", ""))
		}
		return strings.Join(fields, "  ")
	}
	for _, count := range []struct{ label, field string }{
		{"Succeeded", "completed"}, {"Failed", "failed"}, {"Total", "total"},
	} {
		value := "—"
		if n, ok := batchCount(m.actionsState.overview.Get("request_counts." + count.field)); ok {
			value = fmt.Sprint(n)
		}
		fields = append(fields, render(count.label+separator, "")+render(value, batchBrowserCountTone(m.actionsState.overview, count.field)))
	}
	return strings.Join(fields, "  ")
}

func (m *batchBrowserScreen) selectedAction() string {
	if len(m.visible) == 0 {
		return ""
	}
	return m.choices[m.visible[m.index]].value
}

func batchBrowserFileAction(action string) bool {
	return action == "raw-output" || strings.HasPrefix(action, "save:")
}

func (m *batchBrowserScreen) updateActions(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(1, msg.Width-20))
	case tea.KeyPressMsg:
		selected := m.selectedAction()
		saveSelected := selected == "download" || batchBrowserFileAction(selected)
		switch msg.String() {
		case "esc":
			if saveSelected && m.actionsState.saveExpanded {
				m.setSaveExpanded(false)
				return m, nil
			}
			m.chosen, m.closed = "back", true
			return m, tea.Quit
		case "ctrl+c":
			m.closed = true
			return m, tea.Quit
		case "ctrl+r":
			m.chosen, m.closed = "refresh", true
			return m, tea.Quit
		case "left":
			if saveSelected {
				m.setSaveExpanded(false)
			}
		case "right":
			if selected == "download" {
				if m.actionsState.saveExpanded {
					m.index++
				} else {
					m.setSaveExpanded(true)
				}
			}
		case "up", "shift+tab":
			m.index = max(0, m.index-1)
		case "down", "tab":
			m.index = min(max(0, len(m.visible)-1), m.index+1)
		case "enter":
			if m.width < 30 || m.height < 7 || selected == "" {
				return m, nil
			}
			if selected == "download" {
				m.setSaveExpanded(!m.actionsState.saveExpanded)
				return m, nil
			}
			m.chosen, m.closed = selected, true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *batchBrowserScreen) actionKeys(shortcut func(string, string) string) []string {
	if m.pathState != nil {
		return m.pathKeys(shortcut)
	}
	selected := m.selectedAction()
	enter, escape := "open", "back"
	if selected == "download" {
		enter = "expand"
		if m.actionsState.saveExpanded {
			enter = "collapse"
		}
	}
	if m.actionsState.saveExpanded && (selected == "download" || batchBrowserFileAction(selected)) {
		escape = "collapse"
	}
	return []string{shortcut("↑↓", "move"), shortcut("Enter", enter), shortcut("Ctrl+R", "refresh"), shortcut("Esc", escape)}
}

// The save editor inserts its field below the active file row in the action menu.
func (m *batchBrowserScreen) actionLines(width, height, contentWidth int, footer []string, sectionRule func() string, muted, accent, highlight lipgloss.Style, semantic func(lipgloss.Style, string) lipgloss.Style, colors bool) ([]string, *tea.Cursor) {
	prefix := ""
	if contentWidth >= 55 {
		prefix = "Batches / "
	}
	id := ansi.Truncate(batchInputLabel(m.title), contentWidth-ansi.StringWidth(prefix), "…")
	lines := []string{"  " + muted.Render(prefix) + lipgloss.NewStyle().Bold(colors).Render(id)}
	var pathLines []string
	var cursor *tea.Cursor
	pathCursorX := 0
	if m.pathState != nil {
		pathLines, pathCursorX = m.pathLines(contentWidth, muted, semantic(lipgloss.NewStyle(), "warning"))
	}
	reserved := 1 + len(pathLines)
	summaryValue := func(text, tone string) string { return semantic(muted, tone).Render(text) }
	summary := strings.Split(ansi.Wrap(m.overviewText(" ", contentWidth, summaryValue), contentWidth, ""), "\n")
	if len(lines)+len(summary)+len(footer)+reserved > height {
		// Keep counts and navigation before decorative spacing on compact terminals.
		footer = footer[1:]
	}
	if len(lines)+len(summary)+len(footer)+reserved > height {
		if m.pathState != nil {
			footer = m.compactPathFooter(width, func(key, action string) string {
				return accent.Render(key) + " " + muted.Render(action)
			})
		} else {
			footer = []string{accent.Render("↑↓ Enter Ctrl+R Esc")}
		}
	}
	if len(lines)+len(summary)+len(footer)+reserved > height {
		// Numeric counts can occupy 20 cells. Use the full width for this fallback.
		summary = strings.Split(ansi.Wrap(m.overviewText(":", width, summaryValue), width, ""), "\n")
		for _, text := range summary[:min(len(summary), max(0, height-len(lines)-len(footer)-reserved))] {
			lines = append(lines, muted.Render(text))
		}
	} else {
		for _, text := range summary {
			lines = append(lines, "  "+muted.Render(text))
		}
	}
	if height-len(lines)-len(footer)-len(pathLines) > len(m.visible) {
		lines = append(lines, sectionRule())
	}
	if m.progress != nil && batchBrowserProcessing(m.actionsState.overview) && len(lines)+len(footer)+2 <= height {
		message := "Updates every 10s while this screen is open"
		if m.progress.paused {
			message = "Updates paused. Ctrl+R retries."
		}
		lines = append(lines, "  "+muted.Render(ansi.Truncate(batchInputLabel(message), contentWidth, "…")))
	}
	capacity := max(1, height-len(lines)-len(footer)-len(pathLines))
	start := max(0, m.index-capacity+1)
	for i := start; i < len(m.visible) && i < start+capacity; i++ {
		row := m.choices[m.visible[i]]
		selected := i == m.index
		base := lipgloss.NewStyle()
		if selected {
			base = highlight
		}
		name := ansi.Truncate(batchInputLabel(row.title), contentWidth, "…")
		if row.value == "download" {
			marker := " ▸"
			if m.actionsState.saveExpanded {
				marker = " ▾"
			}
			name = ansi.Truncate(name, contentWidth-2, "…") + marker
		} else if batchBrowserFileAction(row.value) {
			name = "  " + ansi.Truncate(name, contentWidth-2, "…")
		}
		suffix := ""
		if contentWidth >= 55 {
			hint, tone := m.actionHint(row.value)
			if hint != "" {
				column := max(24, ansi.StringWidth(name)+2)
				hint = ansi.Truncate(batchInputLabel(hint), contentWidth-column, "…")
				hintStyle := base
				if colors {
					hintStyle = base.Foreground(muted.GetForeground())
				}
				suffix = base.Render(strings.Repeat(" ", column-ansi.StringWidth(name))) + semantic(hintStyle, tone).Render(hint)
			}
		}
		if selected {
			padding := strings.Repeat(" ", max(0, width-2-ansi.StringWidth(name+suffix)))
			label := highlight.Bold(colors)
			if colors {
				label = label.Foreground(accent.GetForeground())
			}
			// Each styled segment retains the selected background, including gaps and padding.
			lines = append(lines, highlight.Render("> ")+label.Render(name)+suffix+highlight.Render(padding))
			if m.pathState != nil {
				cursor = tea.NewCursor(pathCursorX, len(lines))
				cursor.Shape, cursor.Blink = tea.CursorBar, true
				lines = append(lines, pathLines...)
			}
		} else {
			lines = append(lines, "  "+name+suffix)
		}
	}
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	return append(lines, footer...), cursor
}
