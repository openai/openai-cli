package audio

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/openai/openai-cli/internal/readable"
)

func (m *terminalTranslationModel) resultView(layout translationLayout) tea.View {
	status := m.resultStatus()
	statusStyle := layout.muted
	switch status {
	case "Completed":
		statusStyle = m.resultColor("#236B43", "#8DC9A0")
	case "Stopped":
		statusStyle = m.resultColor("#885B14", "#E2BC79")
	case "Interrupted":
		statusStyle = audioFailureStyle(m.color, m.dark)
	}
	rows := []string{
		layout.heading(layout.title.Render("Live translation"), statusStyle.Render(status)),
		layout.muted.Render(m.fieldValue("Source") + " → " + audiolive.LanguageName(m.options.Config.Language)),
		layout.divider,
	}
	if m.state.Original == "" && m.state.Translation == "" && m.state.AudioBytes == 0 && (!m.finished || !m.hasResult()) {
		return m.resultPlaceholder(layout, rows, status)
	}

	footer := layout.footer(m.resultShortcuts()...)
	wide := layout.width >= 74
	// Keep two caption rows per track before allocating space for audio details.
	minCaptionRows := 6
	if wide {
		minCaptionRows = 3
	}
	available := layout.height - len(rows) - len(footer)
	details := m.resultDetails(layout, max(0, available-minCaptionRows))
	rows = append(rows, m.resultCaptions(layout, max(minCaptionRows, available-len(details)), wide)...)
	if len(details) > 0 && len(rows)+len(details) < layout.height-len(footer) {
		rows = append(rows, "")
	}
	rows = append(rows, details...)
	return layout.panel(rows, footer, nil)
}

func (m *terminalTranslationModel) resultStatus() string {
	switch {
	case m.finished && m.result.Complete:
		return "Completed"
	case m.finished && m.stopping && m.lastErr == nil:
		return "Stopped"
	case m.finished:
		return "Interrupted"
	case m.stopping || m.state.State == "stopping":
		return "Finishing"
	case m.form.source == translationText || (m.form.source == translationFile && m.state.State == "listening"):
		return "Translating"
	case m.state.State == "listening":
		return "Listening"
	default:
		return "Connecting"
	}
}

func (m *terminalTranslationModel) resultColor(light, dark string) lipgloss.Style {
	style := lipgloss.NewStyle()
	if !m.color {
		return style
	}
	if m.dark {
		return style.Foreground(lipgloss.Color(dark))
	}
	return style.Foreground(lipgloss.Color(light))
}

func (m *terminalTranslationModel) resultPlaceholder(layout translationLayout, rows []string, status string) tea.View {
	instruction := "Opening the translation session…"
	controls := [][2]string{{"Esc", "cancel"}}
	switch {
	case m.finished:
		instruction = "No captions or audio received."
		controls = [][2]string{{"Enter", "back to setup"}, {"Esc", "exit"}}
	case status == "Finishing":
		instruction = "Waiting for final captions…"
		controls = [][2]string{{"Ctrl+C", "exit"}}
	case status == "Listening":
		instruction = "Speak into your microphone."
		controls = [][2]string{{"Space", "stop"}}
	case m.form.source == translationText:
		instruction = "Translating your text…"
	case status == "Translating":
		instruction = "Translating your audio file…"
		controls = [][2]string{{"Space", "stop"}}
	}
	rows = append(rows, "", instruction)
	if notice := m.resultNotice(); notice != "" {
		rows = append(rows, notice)
	}
	return layout.panel(rows, layout.footer(controls...), nil)
}

func (m *terminalTranslationModel) resultShortcuts() [][2]string {
	if !m.finished {
		return [][2]string{{"Space", "stop"}, {"Ctrl+C", "exit"}}
	}
	var controls [][2]string
	if m.hasResult() {
		controls = append(controls, [2]string{"T", "read"})
	}
	if m.result.AudioBytes > 0 {
		if m.replayDone != nil {
			controls = append(controls, [2]string{"P", "stop"})
		} else {
			controls = append(controls, [2]string{"P", "play"}, [2]string{"←→", "seek"})
		}
	}
	if m.hasResult() && !m.saved && !m.saving {
		controls = append(controls, [2]string{"S", "save"})
	}
	if m.replayDone == nil && !m.saving {
		controls = append(controls, [2]string{"N", "new"})
	}
	if m.saving {
		return append(controls, [2]string{"Ctrl+C", "exit"})
	}
	return append(controls, [2]string{"Esc", "exit"})
}

func (m *terminalTranslationModel) resultNotice() string {
	if m.note != "" {
		style := m.resultColor("#885B14", "#E2BC79")
		if m.lastErr != nil {
			style = audioFailureStyle(m.color, m.dark)
		}
		return style.Render(audioHistoryCell(m.note))
	}
	if m.state.Warning != "" {
		return m.resultColor("#885B14", "#E2BC79").Render(audioHistoryCell(m.state.Warning))
	}
	return ""
}

func (m *terminalTranslationModel) resultDetails(layout translationLayout, budget int) []string {
	var feedback []string
	if m.saved {
		label := "Saved  "
		path, _ := audioHistorySuffix(audioHistoryCell(m.savedPath), max(1, layout.width-len(label)))
		feedback = append(feedback, layout.muted.Render(label)+path)
	} else if m.saving {
		feedback = append(feedback, layout.muted.Render("Saving…"))
	}
	if notice := m.resultNotice(); notice != "" {
		feedback = append(feedback, notice)
	}
	remaining := max(0, budget-len(feedback))
	var rows []string
	if heading := m.translationAudioHeading(layout); heading != "" && remaining > 0 {
		rows = append(rows, heading)
		remaining--
	}
	var metrics []string
	if summary := m.metricSummary(); summary != "" {
		metrics = strings.Split(ansi.Wordwrap(summary, layout.width, ""), "\n")
		metrics = metrics[:min(len(metrics), remaining)]
		for i, line := range metrics {
			metrics[i] = layout.muted.Render(line)
		}
	}
	rows = append(rows, m.translationWaveformRows(layout, remaining-len(metrics))...)
	rows = append(rows, metrics...)
	rows = append(rows, feedback...)
	return rows[:min(len(rows), budget)]
}

func (m *terminalTranslationModel) resultCaptions(layout translationLayout, budget int, wide bool) []string {
	original, translation := layout.title.Render("Original"), layout.title.Render("Translation")
	if m.state.Original != "" {
		if m.languageGuess == "" {
			original += layout.muted.Render(" · Language unsure")
		} else {
			original += layout.muted.Render(" · Likely ") + layout.accent.Render(m.languageGuess)
		}
	}
	if wide {
		const gap = "    "
		leftWidth := (layout.width - len(gap)) / 2
		rightWidth := layout.width - leftWidth - len(gap)
		left := append([]string{ansi.Truncate(original, leftWidth, "…")}, terminalCaptionRows(m.state.Original, leftWidth, budget-1)...)
		right := append([]string{translation}, terminalCaptionRows(m.state.Translation, rightWidth, budget-1)...)
		rows := make([]string, max(len(left), len(right)))
		for i := range rows {
			if i < len(left) {
				rows[i] = left[i]
			}
			if i < len(right) {
				rows[i] += strings.Repeat(" ", max(0, leftWidth-ansi.StringWidth(rows[i]))) + gap + right[i]
			}
		}
		return rows
	}
	count := (budget - 2) / 2
	rows := append([]string{original}, terminalCaptionRows(m.state.Original, layout.width, count)...)
	translated := terminalCaptionRows(m.state.Translation, layout.width, count)
	if len(rows)+len(translated)+1 < budget {
		rows = append(rows, "")
	}
	rows = append(rows, translation)
	return append(rows, translated...)
}

func terminalCaptionRows(text string, width, count int) []string {
	if text == "" {
		return []string{"…"}
	}
	text = strings.ReplaceAll(readable.Text(text), "\t", "    ")
	rows := strings.Split(ansi.Wrap(text, width, ""), "\n")
	if len(rows) > count {
		rows = rows[len(rows)-count:]
	}
	return rows
}
