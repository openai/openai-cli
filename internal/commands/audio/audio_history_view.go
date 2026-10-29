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
	entries := m.visibleHistory()
	count := "recordings"
	if len(m.entries) == 1 {
		count = "recording"
	}
	title := fmt.Sprintf("  History (%d %s)", len(m.entries), count)
	if m.historySearch.query != "" {
		title = fmt.Sprintf("  History (%d of %d %s)", len(entries), len(m.entries), count)
	}
	search, cursor := m.historySearchRow()
	lines := []string{m.strong().Render(title), search}
	if m.framed() {
		lines = append(lines, m.listeningRule())
	}
	footer := []string{""}
	if m.store.Directory != "" {
		footer = append(footer, m.historyPathRow("History", m.store.Directory))
	}
	if len(entries) == 0 {
		empty := "  No saved audio yet."
		hints := [][2]string{{"Esc", "back"}}
		if len(m.entries) > 0 {
			empty = "  No matching recordings."
			hints = [][2]string{{"Ctrl+U", "clear"}, {"Esc", "back"}}
		}
		footer = append(footer, m.shortcutLine(hints...))
		return m.cursorView(append(append(lines, empty), footer...), len(footer), cursor)
	}
	choice := max(0, min(m.choice, len(entries)-1))
	selected := entries[choice]
	metadata := readable.ChoiceTitle(selected.Settings.Voice) + "  " + strings.ToUpper(selected.Settings.Format)
	if selected.Bytes > 0 {
		metadata += "  " + audioFileSize(selected.Bytes)
	}
	footer = append([]string{"", "  " + m.muted().Render(ansi.Truncate(audioHistoryCell(metadata), max(1, m.audioWidth()-4), "…"))}, footer[1:]...)
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
	footer = append(footer, m.shortcutLine(move, [2]string{"Enter", "open"}, [2]string{"Esc", "back"}))
	lines = append(lines, "  "+m.muted().Render(audioHistoryTableRow("Clip", "Voice", "Format", "Created time", m.audioWidth()-4)))

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
			lines = append(lines, "  "+m.muted().Render(rows[start].date))
			budget--
		}
	}
	end := min(len(rows), start+budget)
	if end > start && rows[end-1].entry < 0 {
		end-- // Never show a group heading without its first recording.
	}
	for _, r := range rows[start:end] {
		if r.entry < 0 {
			lines = append(lines, "  "+m.muted().Render(r.date))
			continue
		}
		entry := entries[r.entry]
		prompt := audioHistoryClip(entry.Settings.Input)
		stamp := "--:--"
		if !entry.CreatedAt.IsZero() {
			stamp = entry.CreatedAt.In(now.Location()).Format("15:04")
		}
		text := audioHistoryTableRow(prompt, readable.ChoiceTitle(entry.Settings.Voice), strings.ToUpper(entry.Settings.Format), stamp, m.audioWidth()-4)
		lines = append(lines, m.choiceRow(text, r.entry == choice && !m.historySearch.focused, m.audioWidth()))
	}
	return m.cursorView(append(lines, footer...), len(footer), cursor)
}

// The caller reserves two cells for selection and two for the right inset.
func audioHistoryTableRow(clip, voice, format, created string, width int) string {
	cell := func(value string, cells int, right bool) string {
		value = ansi.Truncate(audioHistoryCell(value), cells, "…")
		padding := strings.Repeat(" ", max(0, cells-ansi.StringWidth(value)))
		if right {
			return padding + value
		}
		return value + padding
	}
	if width < 52 {
		return cell(clip, max(1, width), false)
	}
	return cell(clip, width-32, false) + "  " + cell(voice, 8, false) + "  " + cell(format, 6, false) + "  " + cell(created, 12, true)
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
	if day == now.AddDate(0, 0, -1).Format("2006-01-02") {
		return "Yesterday"
	}
	if created.Year() == now.Year() {
		return created.Format("Jan 2")
	}
	return fmt.Sprintf("%s, %d", created.Format("Jan 2"), created.Year())
}
