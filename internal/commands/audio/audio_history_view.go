package audio

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

// Date headings replace repeated metadata. Keep each prompt on one selectable row.
func (m *audioWorkbench) historyView(now time.Time) tea.View {
	lines := []string{m.strong().Render("  History")}
	footer := []string{"  " + m.hintLine([2]string{"Enter", "open"}, [2]string{"Esc", "back"})}
	if len(m.entries) == 0 {
		return tea.NewView(m.fitAudio(append(append(lines, "  No saved audio yet."), footer...)))
	}
	choice := max(0, min(m.choice, len(m.entries)-1))
	selected := m.entries[choice]
	if m.height >= 24 && m.width >= 60 {
		metadata := readable.ChoiceTitle(selected.Settings.Voice) + " · " + strings.ToUpper(selected.Settings.Format)
		if selected.Bytes > 0 {
			metadata += " · " + audioFileSize(selected.Bytes)
		}
		footer = append([]string{"", "  " + m.muted().Render(readable.Text(metadata))}, footer...)
	}

	type row struct {
		entry int
		date  string
	}
	rows := make([]row, 0, len(m.entries)+1)
	selectedRow, previous := 0, ""
	for i, entry := range m.entries {
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
	budget := max(1, m.height-1-len(lines)-len(footer))
	start := max(0, selectedRow-budget+1)
	// Scrolling within a group needs a replacement date heading.
	if start > 0 && rows[start].entry >= 0 && budget > 1 {
		start++
		if rows[start].entry >= 0 {
			lines = append(lines, "  "+m.muted().Render(rows[start].date))
			budget--
		}
	}
	for _, r := range rows[start:min(len(rows), start+budget)] {
		if r.entry < 0 {
			lines = append(lines, "  "+m.muted().Render(r.date))
			continue
		}
		entry := m.entries[r.entry]
		prompt := strings.Join(strings.Fields(readable.Text(entry.Settings.Input)), " ")
		if prompt == "" {
			prompt = "Generated speech"
		}
		if m.width < 60 {
			lines = append(lines, m.label(prompt, r.entry == choice))
			continue
		}
		stamp := "--:--"
		if !entry.CreatedAt.IsZero() {
			stamp = entry.CreatedAt.In(now.Location()).Format("15:04")
		}
		width := max(1, m.audioWidth()-2)
		promptWidth := max(1, width-7)
		prompt = ansi.Truncate(prompt, promptWidth, "…")
		padding := strings.Repeat(" ", max(2, width-ansi.StringWidth(prompt)-len(stamp)))
		lines = append(lines, m.label(prompt+padding+m.muted().Render(stamp), r.entry == choice))
	}
	return tea.NewView(m.fitAudio(append(lines, footer...)))
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
