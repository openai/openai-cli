package audio

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Keep one selectable row per recording. Date groups and footer details do not
// consume the selected recording's row, including in the compact layout.
func (m *audioWorkbench) historyView(now time.Time) tea.View {
	entries := m.visibleHistory(now)
	count := "recordings"
	if len(m.entries) == 1 {
		count = "recording"
	}
	summary := fmt.Sprintf("%d %s", len(m.entries), count)
	if m.historySearch.query != "" {
		summary = fmt.Sprintf("%d of %d %s", len(entries), len(m.entries), count)
	}
	title := m.strong().Render("  History") + strings.Repeat(" ", max(1, m.audioWidth()-11-ansi.StringWidth(summary))) + m.muted().Render(summary)
	search, cursor := m.historySearchRow()
	lines := []string{title, search}
	if m.framed() {
		lines = append(lines, m.listeningRule())
	}
	footer := []string{""}
	if m.historySearch.showFolder && m.store.Directory != "" {
		footer = append(footer, m.historyPathRow("History", m.store.Directory))
	}
	folderHint := [2]string{"Ctrl+L", "folder"}
	if len(entries) == 0 {
		empty := "  No saved audio yet."
		hints := [][2]string{{"Esc", "back"}}
		if len(m.entries) > 0 {
			empty = "  No matching recordings."
			hints = [][2]string{{"Ctrl+U", "clear"}, {"Esc", "back"}}
		}
		if m.store.Directory != "" {
			hints = append(hints, folderHint)
		}
		footer = append(footer, m.formShortcutRows(hints...)...)
		return m.cursorView(append(append(lines, empty), footer...), len(footer), cursor)
	}
	choice := max(0, min(m.choice, len(entries)-1))
	selected := entries[choice]
	metadata := []string{readable.ChoiceTitle(selected.Settings.Voice), strings.ToUpper(selected.Settings.Format)}
	if selected.Bytes > 0 {
		metadata = append(metadata, audioFileSize(selected.Bytes))
	}
	detail := "  "
	if !selected.CreatedAt.IsZero() {
		detail += m.primaryText().Render(selected.CreatedAt.In(now.Location()).Format("2006-01-02 15:04")) + "  "
	}
	available := max(1, m.audioWidth()-ansi.StringWidth(detail)-2)
	for len(metadata) > 1 && ansi.StringWidth(audioHistoryCell(strings.Join(metadata, "  "))) > available {
		metadata = metadata[1:]
	}
	footer[0] = detail + m.muted().Render(ansi.Truncate(audioHistoryCell(strings.Join(metadata, "  ")), available, "…"))
	footer = append([]string{""}, footer...)
	// Export locations are session evidence, not persisted History metadata.
	for _, result := range []*audioWorkResult{m.result, m.priorResult} {
		if result != nil && selected.ID != "" && result.entry.ID == selected.ID && len(result.savedPaths) > 0 {
			if path := result.savedPaths[len(result.savedPaths)-1]; path != "" {
				footer = append(footer, m.historyPathRow("Saved", path))
				break
			}
		}
	}
	move := [2]string{"↑↓", "move"}
	if m.historySearch.focused {
		move = [2]string{"↓", "recordings"}
	}
	hints := [][2]string{move, {"Enter", "open"}, {"Esc", "back"}}
	if m.historySearch.query != "" {
		hints = append(hints, [2]string{"Ctrl+U", "clear"})
	}
	if m.store.Directory != "" {
		hints = append(hints, folderHint)
	}
	footer = append(footer, m.formShortcutRows(hints...)...)
	lines = append(lines, "  "+m.historyTableRow("Clip", "Voice", "Format", "Time", true))

	type row struct {
		entry int
		date  string
	}
	rows := make([]row, 0, len(entries)+1)
	selectedRow, previous := 0, ""
	for i, entry := range entries {
		date := audioHistoryDate(entry.CreatedAt, now)
		if date != previous {
			rows = append(rows, row{entry: -1, date: date})
			previous = date
		}
		if i == choice {
			selectedRow = len(rows)
		}
		rows = append(rows, row{entry: i, date: date})
	}
	budget := max(1, m.audioHeight()-len(lines)-len(footer))
	start := max(0, selectedRow-budget+1)
	// Reserve a replacement date heading when the viewport starts within a group.
	if rows[start].entry >= 0 && budget > 1 {
		start = max(0, selectedRow-budget+2)
		if rows[start].entry >= 0 {
			lines = append(lines, "  "+m.strong().Render(rows[start].date))
			budget--
		}
	}
	end := min(len(rows), start+budget)
	if end > start && rows[end-1].entry < 0 {
		end-- // Never show a group heading without its first recording.
	}
	for _, r := range rows[start:end] {
		if r.entry < 0 {
			lines = append(lines, "  "+m.strong().Render(r.date))
			continue
		}
		entry := entries[r.entry]
		prompt := audioHistoryClip(entry.Settings.Input)
		stamp := "--:--"
		if !entry.CreatedAt.IsZero() {
			stamp = entry.CreatedAt.In(now.Location()).Format("15:04")
		}
		text := m.historyTableRow(prompt, readable.ChoiceTitle(entry.Settings.Voice), strings.ToUpper(entry.Settings.Format), stamp, false)
		lines = append(lines, m.choiceRow(text, r.entry == choice && !m.historySearch.focused, m.audioWidth()))
	}
	return m.cursorView(append(lines, footer...), len(footer), cursor)
}

// The caller reserves two cells for selection and two for the right inset.
func (m *audioWorkbench) historyTableRow(clip, voice, format, created string, header bool) string {
	width := m.audioWidth() - 4
	cell := func(value string, cells int, right bool) string {
		value = ansi.Truncate(audioHistoryCell(value), cells, "…")
		padding := strings.Repeat(" ", max(0, cells-ansi.StringWidth(value)))
		if right {
			return padding + value
		}
		return value + padding
	}
	clipStyle := m.primaryText()
	if header {
		clipStyle = m.muted()
	}
	if width < 52 {
		return clipStyle.Render(cell(clip, max(1, width), false))
	}
	metadata := "  " + cell(voice, 8, false) + "  " + cell(format, 6, false) + "  " + cell(created, 12, true)
	return clipStyle.Render(cell(clip, width-32, false)) + m.muted().Render(metadata)
}

func audioHistoryCell(value string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(readable.Text(value))
}

func (m *audioWorkbench) historyPathRow(label, path string) string {
	label += ": "
	available := max(1, m.audioWidth()-4-ansi.StringWidth(label))
	path, _ = audioHistorySuffix(audioHistoryCell(path), available)
	return "  " + m.muted().Render(label+path)
}

func audioHistorySuffix(text string, available int) (string, int) {
	width := 0
	for remaining := text; len(remaining) > 0; {
		_, cells, consumed := cliterminal.Sequence(remaining)
		width += cells
		remaining = remaining[consumed:]
	}
	if width > available {
		for len(text) > 0 && width > available-1 {
			_, cells, consumed := cliterminal.Sequence(text)
			text = text[consumed:]
			width -= cells
		}
		return "…" + text, width + 1
	}
	return text, width
}

func audioHistoryDate(created, now time.Time) string {
	if created.IsZero() {
		return "Earlier"
	}
	created = created.In(now.Location())
	day := created.Format("2006-01-02")
	if day == now.Format("2006-01-02") {
		return "Today"
	}
	if day == audioHistoryYesterday(now) {
		return "Yesterday"
	}
	if created.Year() == now.Year() {
		return created.Format("Jan 2")
	}
	return fmt.Sprintf("%s, %d", created.Format("Jan 2"), created.Year())
}
