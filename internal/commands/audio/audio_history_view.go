package audio

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	var details []string
	hasFailure := m.activeFailure() && (m.note == audioHistoryClearFailureNote ||
		m.note == audioHistoryAutoplayFailureNote || m.note == audioHistoryAutoplayReadFailureNote)
	if hasFailure {
		details = append(details, "  "+m.failureStyle().Render(m.note))
	}
	if m.historySearch.showFolder && m.store.Directory != "" {
		details = append(details, m.historyPathRow("History", m.store.Directory))
	}
	autoplay := "autoplay off"
	if m.historyAutoplay {
		autoplay = "autoplay on"
	}
	settings := [][2]string{{"Ctrl+P", autoplay}, {"Ctrl+D", "clear history"}}
	if m.store.Directory != "" {
		settings = append(settings, [2]string{"Ctrl+L", "folder"})
	}
	if hasFailure {
		settings = append(settings, [2]string{"F1", "details"})
	}
	if len(entries) == 0 {
		empty := "  No saved audio yet."
		hints := [][2]string{{"Esc", "back"}}
		if m.note == audioHistoryClearedNote && len(m.entries) == 0 {
			empty = "  " + audioHistoryClearedNote
		} else if len(m.entries) > 0 {
			empty = "  No matching recordings."
			hints = [][2]string{{"Ctrl+U", "clear"}, {"Esc", "back"}}
		}
		footer := m.formShortcutRows(append(hints, settings...)...)
		return m.panelView(append(lines, empty), details, footer, cursor)
	}
	if m.note == audioHistoryClearedNote {
		details = append(details, "  "+audioHistoryClearedNote)
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
	detail += m.muted().Render(ansi.Truncate(audioHistoryCell(strings.Join(metadata, "  ")), available, "…"))
	// Export locations are session evidence, not persisted History metadata.
	for _, result := range []*audioWorkResult{m.result, m.priorResult} {
		if result != nil && selected.ID != "" && result.entry.ID == selected.ID && len(result.savedPaths) > 0 {
			if path := result.savedPaths[len(result.savedPaths)-1]; path != "" {
				details = append(details, m.historyPathRow("Saved", path))
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
	footer := m.formShortcutRows(append(hints, settings...)...)
	lines = append(lines, "  "+m.historyTableRow("Clip", "Voice", "Format", "Time", true))
	room := m.audioHeight() - len(lines) - len(details) - len(footer) - 1
	if room > 0 {
		details = append([]string{detail}, details...)
	}
	if room > 1 {
		details = append([]string{""}, details...)
	}

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
	budget := max(1, m.audioHeight()-len(lines)-len(details)-len(footer))
	start := max(0, selectedRow-budget+1)
	dateStyle := m.accent()
	if m.color && m.dark {
		dateStyle = dateStyle.Foreground(lipgloss.Color("#A8DAFF"))
	}
	// Reserve a replacement date heading when the viewport starts within a group.
	if rows[start].entry >= 0 && budget > 1 {
		start = max(0, selectedRow-budget+2)
		if rows[start].entry >= 0 {
			lines = append(lines, "  "+dateStyle.Render(rows[start].date))
			budget--
		}
	}
	end := min(len(rows), start+budget)
	if end > start && rows[end-1].entry < 0 {
		end-- // Never show a group heading without its first recording.
	}
	for _, r := range rows[start:end] {
		if r.entry < 0 {
			lines = append(lines, "  "+dateStyle.Render(r.date))
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
	return m.panelView(lines, details, footer, cursor)
}

func (m *audioWorkbench) clearHistoryView() tea.View {
	if m.busy {
		return m.panelView([]string{"  Clearing History…"}, nil, m.formShortcutRows([2]string{"Ctrl+C", "exit"}), nil)
	}
	lines := []string{
		"  " + m.warningStyle().Render("Clear all recording history?"), "",
		"  Deletes cached recordings",
		"  and voice previews.",
		"  Keeps your draft and exports.",
	}
	footer := m.formShortcutRows([2]string{"Esc", "keep"}, [2]string{"Y", "clear all"})
	return m.panelView(lines, nil, footer, nil)
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
