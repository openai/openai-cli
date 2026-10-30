package audio

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *audioWorkbench) settingsSummary(active bool) string {
	toggle := "▸"
	if m.settingsExpanded {
		toggle = "▾"
	}
	style := m.muted()
	if active {
		style = m.accent()
	}
	if m.settingsExpanded {
		return style.Render(toggle)
	}
	model := m.primaryText().Render(audioChoiceLabel(0, m.settings.Model))
	format := m.primaryText().Render(audioChoiceLabel(2, m.settings.Format))
	speed := m.primaryText().Render(fmt.Sprintf("%.2f×", m.settings.Speed))
	summary := strings.Join([]string{
		m.muted().Render("Model") + " " + model,
		m.muted().Render("Format") + " " + format,
		m.muted().Render("Speed") + " " + speed,
	}, "  ")
	width := m.settingsWidth() - m.settingsLabelWidth() - 5 // Label, marker, toggle, and spaces.
	if ansi.StringWidth(summary) > width {
		rest := "  " + format + "  " + speed
		summary = ansi.Truncate(model, max(1, width-ansi.StringWidth(rest)), "…") + rest
	}
	return style.Render(toggle) + " " + summary
}

func (m *audioWorkbench) choiceView() tea.View {
	title := []string{"Model", "Voice", "Response format"}[m.setting]
	current := []string{m.settings.Model, m.settings.Voice, m.settings.Format}[m.setting]
	lines := []string{m.strong().Render("  " + title), "  " + m.muted().Render("Current") + "  " + m.primaryText().Render(audioChoiceLabel(m.setting, current))}
	if m.framed() {
		lines = append(lines, m.listeningRule())
	}

	hints := [][2]string{{"↑↓", "move"}, {"Enter", "select"}, {"Esc", "back"}}
	if m.setting == 1 {
		hints = append(hints, [2]string{"P", "preview"})
	}
	footer := m.formShortcutRows(hints...)
	count := len(m.choices)
	if count == 0 {
		return tea.NewView(m.fitAudio(append(lines, footer...), len(footer)))
	}
	column, detailWidth := 0, 0
	for _, value := range m.choices {
		column = max(column, ansi.StringWidth(audioChoiceLabel(m.setting, value)))
		detail, _ := audioChoiceDescription(m.setting, value)
		detailWidth = max(detailWidth, ansi.StringWidth(detail))
	}
	sideBySide := detailWidth > 0 && column+3+detailWidth <= m.audioWidth()-4
	// A small terminal shows just the highlighted option's detail below the list.
	choice := max(0, min(m.choice, count-1))
	detail, retires := audioChoiceDescription(m.setting, m.choices[choice])
	var notes []string
	if !sideBySide && detail != "" {
		for _, row := range strings.Split(ansi.Wrap(detail, m.audioWidth()-4, ""), "\n") {
			notes = append(notes, "  "+m.muted().Render(row))
		}
	}
	if retires {
		notes = append(notes, "  "+m.warningStyle().Render("Retirement  Jan 6, 2027"))
	}
	if m.framed() && len(notes) > 0 {
		notes = append([]string{""}, notes...)
	}
	visible := max(1, m.audioHeight()-len(lines)-len(footer)-len(notes))
	start := max(0, min(choice-visible+1, count-visible))
	for i := start; i < min(count, start+visible); i++ {
		label := audioChoiceLabel(m.setting, m.choices[i])
		row := m.primaryText().Render(label)
		if sideBySide {
			desc, _ := audioChoiceDescription(m.setting, m.choices[i])
			row += strings.Repeat(" ", column-ansi.StringWidth(label)+3) + m.muted().Render(desc)
		}
		lines = append(lines, m.choiceRow(row, i == choice, m.audioWidth()))
	}
	lines = append(lines, notes...)
	return tea.NewView(m.fitAudio(append(lines, footer...), len(footer)))
}

func audioChoiceLabel(setting int, value string) string {
	if setting == 2 {
		value = strings.ToUpper(value)
	}
	return audioHistoryCell(value)
}

// The Speech reference lists a January 6, 2027 shutdown for five IDs.
// Keep the compatibility IDs distinct from those documented models.
func audioChoiceDescription(setting int, value string) (detail string, retires bool) {
	if setting == 0 {
		switch value {
		case "gpt-4o-mini-tts":
			return "Supports voice instructions", true
		case "gpt-4o-mini-tts-2025-03-20", "gpt-4o-mini-tts-2025-12-15":
			return "Pinned snapshot with voice instructions", true
		case "tts-1":
			return "Lower latency", true
		case "tts-1-hd":
			return "Higher audio quality", true
		case "tts-1-1106", "tts-1-hd-1106":
			return "Legacy ID (unlisted)", false
		}
	}
	if setting == 2 {
		switch value {
		case "mp3":
			return "Compressed, widely playable", false
		case "opus":
			return "Low-latency compressed audio", false
		case "aac":
			return "Compressed for media playback", false
		case "flac":
			return "Lossless compression", false
		case "wav":
			return "Uncompressed with a file header", false
		case "pcm":
			return "Raw samples without a header", false
		}
	}
	return "", false
}
