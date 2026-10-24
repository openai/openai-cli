package audio

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	requestconfig "github.com/openai/openai-cli/internal/request"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Keep the audio form aligned with the image picker's palette and cell budgets.
func (m *audioWorkbench) audioWidth() int { return min(96, max(1, m.width-4)) }

func (m *audioWorkbench) accent() lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.color {
		s = s.Bold(true)
		color := "#3159BC"
		if m.dark {
			color = "#8AA8FF"
		}
		s = s.Foreground(lipgloss.Color(color))
	}
	return s
}

func (m *audioWorkbench) strong() lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.color {
		s = s.Bold(true)
	}
	return s
}

func (m *audioWorkbench) muted() lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.color {
		color := "#657087"
		if m.dark {
			color = "#A4ACC2"
		}
		s = s.Foreground(lipgloss.Color(color))
	}
	return s
}

func (m *audioWorkbench) label(text string, active bool) string {
	if !active {
		return "  " + text
	}
	text = ansi.Truncate(text, max(1, m.audioWidth()-2), "…")
	return m.accent().Render("› ") + text
}

func (m *audioWorkbench) editorView(editor textarea.Model) string {
	style := textarea.Styles{}
	style.Focused.Placeholder, style.Blurred.Placeholder = m.muted(), m.muted()
	style.Focused.Selection = m.accent().Underline(true)
	style.Blurred.Selection = style.Focused.Selection
	editor.SetStyles(style)
	return editor.View()
}

// Keep editor values in the same column as their inactive summaries.
func (m *audioWorkbench) inlineEditorRows(name string, editor textarea.Model, active bool, labelWidth int) []string {
	rows := strings.Split(m.editorView(editor), "\n")
	label := fmt.Sprintf("%-*s", labelWidth, name)
	if name != "Instructions" {
		label = m.muted().Render(label)
	}
	rows[0] = m.label(label, active) + " " + rows[0]
	for i := 1; i < len(rows); i++ {
		rows[i] = strings.Repeat(" ", labelWidth+3) + rows[i]
	}
	return rows
}

func (m *audioWorkbench) plainEditorRows(name string, editor textarea.Model, active bool) []string {
	rows := []string{m.label(name, active)}
	for _, line := range strings.Split(m.editorView(editor), "\n") {
		rows = append(rows, "  "+line)
	}
	return rows
}

// Use one outlined editor in roomy forms, as in the image picker.
// Keep the terminal background and the compact form unchanged.
func (m *audioWorkbench) formEditorRows(name string, editor textarea.Model, active bool) []string {
	if !active {
		editor.Blur()
	}
	if name == "Instructions" {
		if m.height < 24 || m.width < 60 {
			return m.plainEditorRows(name, editor, active)
		}
		return m.inlineEditorRows(name, editor, active, 14)
	}
	if m.height < 24 || m.width < 60 {
		var rows []string
		for _, row := range strings.Split(m.editorView(editor), "\n") {
			rows = append(rows, "  "+row)
		}
		return rows
	}
	width := m.audioWidth()
	border := m.muted()
	if active {
		border = m.accent().Bold(false)
	}
	caption := "╭"
	rows := []string{border.Render(caption + strings.Repeat("─", max(0, width-ansi.StringWidth(caption)-1)) + "╮")}
	for _, row := range strings.Split(m.editorView(editor), "\n") {
		body := ansi.Truncate(row, width-4, "")
		rows = append(rows, border.Render("│")+" "+body+strings.Repeat(" ", max(0, width-4-ansi.StringWidth(body)))+" "+border.Render("│"))
	}
	return append(rows, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
}

// Keep keyboard controls distinct from their quieter descriptions without
// changing footer text, spacing, or focus styles.
func (m *audioWorkbench) keyHint(key, action string) string {
	text := m.accent().Bold(false).Render(key)
	if action != "" {
		text += " " + m.muted().Render(action)
	}
	return text
}

func (m *audioWorkbench) hintLine(pairs ...[2]string) string {
	items := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		items = append(items, m.keyHint(pair[0], pair[1]))
	}
	return strings.Join(items, m.muted().Render(" · "))
}

func (m *audioWorkbench) shortcutLine(pairs ...[2]string) string {
	line := ""
	for _, pair := range pairs {
		item := m.keyHint("["+pair[0]+"]", pair[1])
		if line != "" {
			item = "  " + item
		}
		if ansi.StringWidth(line+item) > m.audioWidth()-2 {
			break
		}
		line += item
	}
	return "  " + line
}

func (m *audioWorkbench) View() tea.View {
	if m.quit {
		return tea.NewView("")
	}
	if !m.usable() {
		return tea.NewView(m.fitAudio([]string{"Resize to 40 x 12.", m.keyHint("Ctrl+C", "exits.")}))
	}
	switch m.modal {
	case "recovery":
		return m.recoveryView()
	case "error":
		return m.failureView()
	case "info":
		return m.informationView()
	case "more":
		return m.moreView()
	case "choice", "history":
		return m.choiceView()
	case "save":
		return m.saveView()
	case "speed":
		lines := []string{m.strong().Render("  Speed"), "  " + m.editorView(m.speedInput), "  " + m.muted().Render("0.25–4.00×"), "  " + m.hintLine([2]string{"Enter", "apply"}, [2]string{"Esc", "back"})}
		return tea.NewView(m.fitAudio(append(lines, m.feedbackLines()...)))
	}

	compact := m.height < 24 || m.width < 60
	title := "Text to speech"
	if !compact {
		title = "  " + title
	}
	lines := []string{m.strong().Render(title)}
	if m.focus == 4 && m.result != nil {
		lines = append(lines, m.resultRows(compact, true)...)
		return tea.NewView(m.fitAudio(lines))
	}
	lines = append(lines, m.formRows(compact, true)...)
	if !compact {
		lines = append(lines, "")
	}
	lines = append(lines, m.formActionRows()...)
	if !compact {
		lines = append(lines, "")
	}
	if !compact {
		next := "Settings"
		if m.focus == 0 && m.instructionsSupported() {
			next = "Instructions"
		}
		if m.focus == 2 {
			next = "Text"
			if m.result != nil {
				next = "Audio"
			}
		}
		shortcuts := [][2]string{{"Ctrl+C", "Exit"}, {"Ctrl+G", "Generate"}, {"Tab", next}}
		if m.focus == 0 || m.focus == 1 {
			shortcuts = append(shortcuts, [2]string{"Alt+Enter", "New line"})
		}
		lines = append(lines, m.shortcutLine(shortcuts...))
	}

	limit := m.height - 1
	if len(lines) > limit {
		if m.focus == 4 {
			lines = append(lines[:1], lines[len(lines)-(limit-1):]...)
		} else {
			lines = lines[:limit]
		}
	}
	return tea.NewView(m.fitAudio(lines))
}

// Reuse the same field layout while opening secondary actions.
func (m *audioWorkbench) formRows(compact, interactive bool) []string {
	var lines []string
	for i, editor := range []textarea.Model{m.input, m.instructions} {
		name := []string{"Text", "Instructions"}[i]
		if i == 1 && !compact {
			lines = append(lines, m.strong().Render("  Settings"))
		}
		if i == 1 && !m.instructionsSupported() {
			lines = append(lines, m.label(m.muted().Render("Instructions   Not used by this model"), false))
			if !compact && editor.Value() != "" {
				lines = append(lines, "  "+m.muted().Render(readable.Text(strings.ReplaceAll(editor.Value(), "\n", " "))))
			}
		} else if interactive && m.focus == i || i == 0 && !compact {
			lines = append(lines, m.formEditorRows(name, editor, interactive && m.focus == i)...)
		} else {
			value := readable.Text(strings.ReplaceAll(editor.Value(), "\n", " "))
			if value == "" {
				value = "(optional)"
				if i == 0 {
					value = "(empty)"
				}
			}
			label := fmt.Sprintf("%-14s", name)
			lines = append(lines, m.label(label+" "+value, false))
		}
	}
	values := []string{m.settings.Model, readable.ChoiceTitle(m.settings.Voice), strings.ToUpper(m.settings.Format), fmt.Sprintf("%.2f x", m.settings.Speed)}
	if !audioSupportsVoice(m.settings.Model, m.settings.Voice) {
		values[1] += " (unavailable)"
	}
	for index, name := range []string{"Model", "Voice", "Format", "Speed"} {
		active := interactive && m.focus == 2 && m.setting == index
		label := fmt.Sprintf("%-14s", name)
		lines = append(lines, m.label(label+" "+readable.Text(values[index]), active))
	}
	return lines
}

// Keep primary and secondary actions together when they fit the reading column.
func (m *audioWorkbench) formActionRows() []string {
	active := m.focus == 2 && m.setting == 4
	generate := m.actionRow("Generate speech", active, true)
	if m.busy {
		generate = m.label(m.progressView(), active)
	}
	rows := []string{
		generate,
		m.actionRow("History", m.focus == 2 && m.setting == audioSettingHistory, false),
		m.actionRow("More actions…", m.focus == 2 && m.setting == audioSettingMore, false),
	}
	if m.height >= 24 && m.width >= 60 {
		joined := strings.Join(rows, "  ")
		if ansi.StringWidth(joined) <= m.audioWidth() {
			return []string{joined}
		}
	}
	return rows
}

func (m *audioWorkbench) actionRow(label string, active, primary bool) string {
	if !active {
		if primary {
			label = m.strong().Render(label)
		} else {
			label = m.muted().Render(label)
		}
	}
	return m.label(label, active)
}

func (m *audioWorkbench) resultRows(compact, full bool) []string {
	r := m.result
	title := strings.Join(strings.Fields(readable.Text(r.entry.Settings.Input)), " ")
	if title == "" {
		title = "Generated speech"
	}
	metadata := readable.Text(readable.ChoiceTitle(r.entry.Settings.Voice)) + " · " + strings.ToUpper(r.entry.Settings.Format) + " · " + audioFileSize(r.entry.Bytes)
	lines := []string{"  " + title}
	if !full {
		return nil
	}
	lines = append(lines, "  "+m.muted().Render(metadata))
	if !compact {
		lines = append(lines, "")
	}
	position := audioDuration(r.playbackPosition().Seconds())
	longTime := false
	if r.measured {
		height := 5
		if compact {
			height = 1
		}
		for index, row := range audioWaveform(r.analysis.Peaks, m.waveformWidth(), height, m.selectedBoundary()) {
			parts := strings.SplitN(row, "┃", 2)
			prefix := "  "
			if m.waveformFocused && index == height/2 {
				prefix = m.accent().Render("› ")
			}
			lines = append(lines, prefix+m.accent().Bold(false).Render(parts[0])+m.strong().Render("┃")+m.muted().Render(parts[1]))
		}
		if !compact {
			lines = append(lines, "")
		}
		timing := position + " / " + audioDuration(r.analysis.Duration)
		if ansi.StringWidth(timing) > m.audioWidth()-2 {
			longTime = true
			timing = "Duration " + audioDuration(r.analysis.Duration)
		}
		lines = append(lines, "  "+m.muted().Render(timing))
	} else {
		lines = append(lines, "  Audio preview unavailable.")
	}
	if !compact {
		lines = append(lines, "")
	}
	play := "Play audio"
	if m.playing {
		play = "Stop audio"
	} else if r.finished {
		play = "Replay audio"
	} else if duration := r.seekDuration(); duration > 0 && r.playbackPosition() >= duration {
		play = "At end"
	} else if longTime {
		play += " · " + position
	}
	playRow := m.actionRow(play, m.focus == 4 && !m.waveformFocused && m.resultAction == 0, false)
	saveRow := m.actionRow("Save audio…", m.focus == 4 && !m.waveformFocused && m.resultAction == 1, false)
	if !compact && ansi.StringWidth(playRow)+ansi.StringWidth(saveRow)+4 <= m.audioWidth() {
		lines = append(lines, playRow+"    "+saveRow)
	} else {
		lines = append(lines, playRow, saveRow)
	}
	if len(r.savedPaths) == 0 {
		lines = append(lines, "  "+m.muted().Render("Save to  "+audioDefaultSaveFolder))
	} else {
		lines = append(lines, "  "+m.muted().Render("Saved: "+readable.Text(r.savedPaths[len(r.savedPaths)-1])))
	}
	if !compact {
		lines = append(lines, "")
	}
	if m.focus == 4 {
		playAction := "play"
		if m.playing {
			playAction = "stop"
		}
		navigation := [][2]string{{"←→", "choose"}}
		if r.seekDuration() > 0 {
			navigation = append(navigation, [2]string{"↑", "waveform"})
		}
		control := [2]string{"Space", playAction}
		if m.resultAction == 1 && !m.waveformFocused {
			control = [2]string{"Enter", "save"}
		}
		if m.waveformFocused {
			navigation = [][2]string{{"←→", "seek"}, {"↓", "actions"}}
		}
		movement := m.hintLine(navigation...)
		controls := m.hintLine(control, [2]string{"Esc", "back"})
		if m.busy {
			lines = append(lines, "  "+m.progressView())
		} else if ansi.StringWidth(movement)+ansi.StringWidth(controls)+4 <= m.audioWidth()-2 {
			lines = append(lines, "  "+movement+"    "+controls)
		} else {
			lines = append(lines, "  "+movement, "  "+controls)
		}
	}
	return lines
}

func (m *audioWorkbench) saveView() tea.View {
	compact := m.height < 24 || m.width < 60
	lines := []string{m.strong().Render("  Save audio")}
	if m.result != nil {
		lines = append(lines, "  "+m.muted().Render(strings.ToUpper(m.result.entry.Settings.Format)+" · "+audioFileSize(m.result.entry.Bytes)+" · Full recording"))
	}
	if !compact {
		lines = append(lines, "")
	}
	for i, editor := range []textarea.Model{m.path, m.saveFolder} {
		name := []string{"Filename", "Save to"}[i]
		labelWidth := 14
		if compact {
			labelWidth = 8
		}
		if m.saveFocus == i {
			lines = append(lines, m.inlineEditorRows(name, editor, true, labelWidth)...)
		} else {
			value := readable.Text(editor.Value())
			lines = append(lines, m.label(m.muted().Render(fmt.Sprintf("%-*s", labelWidth, name))+" "+value, false))
		}
	}
	if !compact {
		lines = append(lines, "")
	}
	lines = append(lines, m.actionRow("Save audio", m.saveFocus == 2, true))
	hint := m.hintLine([2]string{"Enter", "save"}, [2]string{"Tab", "edit"}, [2]string{"Esc", "back"})
	if m.saveFocus == 1 {
		hint = m.hintLine([2]string{"Tab", "next"}, [2]string{"Enter", "done"}, [2]string{"Esc", "back"})
	}
	lines = append(lines, "  "+hint)
	lines = append(lines, m.feedbackLines()...)
	return tea.NewView(m.fitAudio(lines))
}

func audioFileSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"} {
		value /= 1024
		if value < 1024 || unit == "EiB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return ""
}

func (m *audioWorkbench) moreView() tea.View {
	items := m.moreItems()
	lines := []string{m.strong().Render("  More actions")}
	if m.height >= 24 && m.width >= 60 {
		context := append([]string{m.strong().Render("  Text to speech")}, m.formRows(false, false)...)
		if len(context)+len(items)+4 <= m.height-1 {
			lines = append(context, "", m.strong().Render("  More actions"))
		} else {
			prompt := m.formEditorRows("Text", m.input, false)
			if len(prompt)+len(items)+5 < m.height {
				lines = append([]string{m.strong().Render("  Text to speech")}, prompt...)
				lines = append(lines, m.strong().Render("  More actions"))
			}
		}
	}
	for index, label := range items {
		lines = append(lines, m.label(label, index == m.moreChoice))
	}
	if m.height > len(lines)+2 {
		lines = append(lines, "")
	}
	lines = append(lines, m.shortcutLine([2]string{"Esc", "Back"}))
	return tea.NewView(m.fitAudio(lines))
}

func (m *audioWorkbench) choiceView() tea.View {
	if m.modal == "history" {
		return m.historyView(time.Now())
	}
	title := []string{"Model", "Voice", "Response format"}[m.setting]
	count := len(m.choices)
	lines := []string{m.strong().Render("  " + title)}
	footer := []string{"  " + m.hintLine([2]string{"Enter", "select"}, [2]string{"Esc", "back"})}
	if m.setting == 0 && count > 0 {
		note := ""
		if strings.HasSuffix(m.choices[m.choice], "1106") {
			note = "Historical snapshot · access unverified"
		} else if strings.Contains(m.choices[m.choice], "-2025-") {
			note = "Snapshot · fixed model release"
		}
		if note != "" {
			footer = append([]string{"  " + m.muted().Render(note)}, footer...)
		}
	}
	if m.height >= 24 && m.width >= 60 {
		footer = append([]string{""}, footer...)
	}
	visible := max(1, m.height-1-len(lines)-len(footer))
	start := max(0, min(m.choice-visible+1, count-visible))
	for i := start; i < min(count, start+visible); i++ {
		lines = append(lines, m.label(readable.Text(m.choices[i]), i == m.choice))
	}
	return tea.NewView(m.fitAudio(append(lines, footer...)))
}

func (m *audioWorkbench) feedbackLines() []string {
	if m.activeFailure() {
		return m.failureSummaryRows()
	}
	note := m.note
	if note == "" {
		return nil
	}
	if note == audioActionFailedNote {
		switch {
		case m.modal != "":
			note += " Esc returns."
		case m.focus == 4 && m.result != nil && m.result.entry.Settings.Preview && m.priorResult != nil:
			note += " Esc restores your previous result."
		case m.focus == 4:
			note += " Esc returns to Text."
		default:
			note += " Esc exits with the error."
		}
	}
	lines := strings.Split(ansi.Wrap(readable.Text(note), max(1, m.audioWidth()), ""), "\n")
	if len(lines) > 2 {
		lines = []string{lines[0], ansi.Truncate(strings.Join(lines[1:], " "), m.audioWidth(), "…")}
	}
	return lines
}

func (m *audioWorkbench) fitAudio(lines []string) string {
	var rows []string
	for _, line := range lines {
		for _, row := range strings.Split(line, "\n") {
			if m.usable() {
				rows = append(rows, "  "+ansi.Truncate(row, m.audioWidth(), "…"))
			} else {
				rows = append(rows, ansi.Truncate(row, max(1, m.width-1), "…"))
			}
		}
	}
	if len(rows) > max(1, m.height-1) {
		rows = rows[:max(1, m.height-1)]
	}
	return strings.Join(rows, "\n")
}

// Bash/zsh preview uses protected piped JSON for exact literal editor text.
// The complete command remains horizontally scrollable, including instructions.
func (m *audioWorkbench) commandPreview() string {
	s := m.effectiveSettings()
	requestArgs, omit := requestconfig.NextCommandOptions(m.command.Root())
	if omit {
		return "Command preview unavailable with private request overrides."
	}
	body := map[string]any{"model": s.Model, "voice": s.Voice, "response_format": s.Format, "speed": s.Speed, "input": s.Input}
	if s.Instructions != "" {
		body["instructions"] = s.Instructions
	}
	data, _ := json.Marshal(body)
	invocation := m.invocation
	for _, arg := range requestArgs {
		invocation += " " + audioShellQuote(arg)
	}
	return "printf %s " + audioShellQuote(string(data)) + " | OPENAI_UNTRUSTED_STDIN=true " + invocation + " audio speak --output speech." + s.Format
}

func audioShellQuote(value string) string {
	// Reuse the existing shell quoter, including its exact control-byte escapes.
	return cliterminal.ShellQuote(value)
}
