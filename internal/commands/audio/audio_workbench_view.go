package audio

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

func (m *audioWorkbench) framed() bool { return m.height >= 24 && m.width >= 60 }

const audioFormLabelWidth = 17

func (m *audioWorkbench) formLabelWidth() int {
	if m.framed() {
		return audioFormLabelWidth
	}
	return 12
}

func (m *audioWorkbench) audioWidth() int {
	if m.framed() {
		return m.width - 3
	}
	return min(96, max(1, m.width-4))
}

func (m *audioWorkbench) audioHeight() int {
	if m.framed() {
		return m.height - 2
	}
	return max(1, m.height-1)
}

func (m *audioWorkbench) borderStyle() lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.color {
		color := "#BAC4D8"
		if m.dark {
			color = "#51566B"
		}
		s = s.Foreground(lipgloss.Color(color))
	}
	return s
}

func (m *audioWorkbench) selected(text string, width int) string {
	text = ansi.Strip(ansi.Truncate(text, width, "…"))
	s := lipgloss.NewStyle()
	if m.color {
		fill, foreground := "#E9EFFE", "#20283B"
		if m.dark {
			fill, foreground = "#343D58", "#F2F4FA"
		}
		s = s.Background(lipgloss.Color(fill)).Foreground(lipgloss.Color(foreground)).Bold(true)
	}
	return s.Render(text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text))))
}

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
	s := m.primaryText()
	if m.color {
		s = s.Bold(true)
	}
	return s
}

func (m *audioWorkbench) primaryText() lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.color {
		color := "#20283B"
		if m.dark {
			color = "#F2F4FA"
		}
		s = s.Foreground(lipgloss.Color(color))
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

// Success belongs to completed playback, never a seek or retained recording.
func (m *audioWorkbench) playbackProgressStyle() lipgloss.Style {
	style := lipgloss.NewStyle()
	if m.color {
		color := "#236B43"
		if m.dark {
			color = "#8DC9A0"
		}
		style = style.Foreground(lipgloss.Color(color))
	}
	return style
}

func (m *audioWorkbench) warningStyle() lipgloss.Style {
	style := lipgloss.NewStyle()
	if m.color {
		color := "#885B14"
		if m.dark {
			color = "#E2BC79"
		}
		style = style.Foreground(lipgloss.Color(color))
	}
	return style
}

func (m *audioWorkbench) failureStyle() lipgloss.Style {
	style := lipgloss.NewStyle()
	if m.color {
		color := "#B23A3A"
		if m.dark {
			color = "#E59A9A"
		}
		style = style.Foreground(lipgloss.Color(color))
	}
	return style
}

func (m *audioWorkbench) centeredRow(text string) string {
	text = ansi.Truncate(text, m.audioWidth()-4, "…")
	return strings.Repeat(" ", max(2, (m.audioWidth()-ansi.StringWidth(text))/2)) + text
}

func (m *audioWorkbench) label(text string, active bool) string {
	if !active {
		return "  " + text
	}
	text = ansi.Truncate(text, max(1, m.audioWidth()-2), "…")
	return m.accent().Render("› ") + text
}

func (m *audioWorkbench) choiceRow(text string, active bool) string {
	if active && m.framed() {
		return m.selected("› "+text, m.audioWidth())
	}
	return m.label(text, active)
}

func (m *audioWorkbench) editorView(editor textarea.Model) string {
	style := textarea.Styles{}
	style.Focused.Text, style.Focused.CursorLine = m.primaryText(), m.primaryText()
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
	label = m.muted().Render(label)
	prefix := m.label(label, active)
	rows[0] = prefix + " " + rows[0]
	for i := 1; i < len(rows); i++ {
		rows[i] = strings.Repeat(" ", labelWidth+3) + rows[i]
	}
	return rows
}

func (m *audioWorkbench) plainEditorRows(name string, editor textarea.Model, active bool) []string {
	rows := []string{m.label(m.muted().Render(name), active)}
	for _, line := range strings.Split(m.editorView(editor), "\n") {
		rows = append(rows, "  "+line)
	}
	return rows
}

// The outer panel groups the workspace; editors need no additional outline.
func (m *audioWorkbench) formEditorRows(name string, editor textarea.Model, active bool) []string {
	if !active {
		editor.Blur()
	}
	if name == "Text" && m.framed() {
		rows := []string{"  " + m.primaryText().Render("Text to speech")}
		for _, row := range strings.Split(m.editorView(editor), "\n") {
			rows = append(rows, "  "+row)
		}
		return rows
	}
	if name == "Instructions" {
		if m.height < 24 || m.width < 60 {
			return m.plainEditorRows(name, editor, active)
		}
		return m.inlineEditorRows(name, editor, active, m.formLabelWidth())
	}
	var rows []string
	for index, row := range strings.Split(m.editorView(editor), "\n") {
		rows = append(rows, m.label(row, active && !m.busy && index == 0))
	}
	return rows
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
	separator := " · "
	if m.framed() {
		separator = "  "
	}
	return strings.Join(items, m.muted().Render(separator))
}

func (m *audioWorkbench) shortcutLine(pairs ...[2]string) string {
	line := ""
	for _, pair := range pairs {
		item := m.keyHint(pair[0], pair[1])
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

// Keep all editing controls available when a footer needs two rows.
func (m *audioWorkbench) formShortcutRows(pairs ...[2]string) []string {
	var rows []string
	line := ""
	for _, pair := range pairs {
		item := m.keyHint(pair[0], pair[1])
		if line != "" && ansi.StringWidth(line)+2+ansi.StringWidth(item) > m.audioWidth()-2 {
			rows = append(rows, "  "+line)
			line = ""
		}
		if line != "" {
			line += "  "
		}
		line += item
	}
	return append(rows, "  "+line)
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
		footer := lines[len(lines)-1]
		lines = append(lines[:len(lines)-1], m.feedbackLines()...)
		return m.cursorView(append(lines, footer), 1, audioEditorCursor(m.speedInput, 2, 1))
	}
	if m.focus == audioFocusCommand && !m.busy {
		return m.commandView()
	}
	if m.framed() {
		return m.workspaceView()
	}

	lines := []string{m.strong().Render("Text to speech")}
	if m.focus == 4 && m.result == nil {
		lines = append(lines, "  "+m.emptyAudioStatus())
		timing, _ := m.playerTiming()
		lines = append(lines, "  "+timing, m.playbackActionRow("Play audio", m.resultAction == 0), m.playerActionRow("Save audio…", m.resultAction == 1, 14))
		footer := m.resultHintRows()
		return tea.NewView(m.fitAudio(append(lines, footer...), len(footer)))
	}
	if m.focus == 4 && m.result != nil {
		lines = append(lines, m.resultRows(true, true)...)
		return tea.NewView(m.fitAudio(lines, len(m.resultHintRows())))
	}
	cursor := m.formCursor(true)
	if cursor != nil {
		cursor.Y += len(lines)
	}
	lines = append(lines, m.formRows(true, true)...)
	lines = append(lines, m.formActionRows()...)
	return m.cursorView(lines, 0, cursor)
}

// Reuse the same field layout while opening secondary actions.
func (m *audioWorkbench) formRows(compact, interactive bool) []string {
	interactive = interactive && !m.busy
	var textRows, instructions []string
	labelWidth := m.formLabelWidth()
	for i, editor := range []textarea.Model{m.input, m.instructions} {
		name := []string{"Text", "Instructions"}[i]
		var rows []string
		if i == 1 && !m.instructionsSupported() {
			rows = append(rows, m.label(m.muted().Render(fmt.Sprintf("%-*s Not used by this model", labelWidth, "Instructions")), false))
			if !compact && editor.Value() != "" {
				rows = append(rows, "  "+readable.Text(strings.ReplaceAll(editor.Value(), "\n", " ")))
			}
		} else if interactive && m.focus == i || i == 0 && !compact {
			rows = append(rows, m.formEditorRows(name, editor, interactive && m.focus == i)...)
		} else {
			value := readable.Text(strings.ReplaceAll(editor.Value(), "\n", " "))
			if value == "" {
				value = "(optional)"
				if i == 0 {
					value = "(empty)"
				}
			}
			label := fmt.Sprintf("%-*s", labelWidth, name)
			rows = append(rows, m.label(m.muted().Render(label)+" "+value, false))
		}
		if i == 0 {
			textRows = rows
		} else {
			instructions = rows
		}
	}
	var settings []string
	values := []string{m.settings.Model, readable.ChoiceTitle(m.settings.Voice), strings.ToUpper(m.settings.Format), fmt.Sprintf("%.2f x", m.settings.Speed)}
	if !audioSupportsVoice(m.settings.Model, m.settings.Voice) {
		values[1] += " (unavailable)"
	}
	for index, name := range []string{"Model", "Voice", "Format", "Speed"} {
		active := interactive && m.focus == 2 && m.setting == index
		label := fmt.Sprintf("%-*s", labelWidth, name)
		if active && index == 3 && m.speedEditing {
			settings = append(settings, m.inlineEditorRows(name, m.speedInput, true, labelWidth)...)
			if m.speedError != "" {
				settings = append(settings, "  "+m.failureStyle().Render(m.speedError))
			}
		} else {
			value := readable.Text(values[index])
			if index == 1 && !audioSupportsVoice(m.settings.Model, m.settings.Voice) {
				value = m.warningStyle().Render(readable.Text(values[index]))
			}
			label = m.muted().Render(label)
			settings = append(settings, m.choiceRow(label+" "+value, active))
		}
	}
	if !compact {
		textRows = append(textRows, "")
	}
	lines := append(textRows, instructions...)
	if !compact {
		lines = append(lines, "")
	}
	return append(lines, settings...)
}

// Keep related actions in one row, matching Left/Right navigation.
func (m *audioWorkbench) formActionRows() []string {
	active := !m.busy && m.focus == 2 && m.setting == 4
	generate := m.actionRow("Generate speech", active)
	if m.busy {
		generate = m.label(m.progressView(), active)
	}
	rows := []string{
		generate,
		m.actionRow("History", m.focus == 2 && m.setting == audioSettingHistory),
		m.actionRow("More actions…", m.focus == 2 && m.setting == audioSettingMore),
	}
	if m.framed() {
		// Selected actions include a trailing cell. Reserve it before focus moves.
		for i, setting := range []int{4, audioSettingHistory, audioSettingMore} {
			if m.busy || m.focus != 2 || m.setting != setting {
				rows[i] += " "
			}
		}
	}
	if m.height >= 24 && m.width >= 60 {
		joined := strings.Join(rows, "  ")
		if ansi.StringWidth(joined) <= m.audioWidth() {
			return []string{joined}
		}
	}
	return rows
}

func (m *audioWorkbench) actionRow(label string, active bool) string {
	active = active && !m.busy
	if active && m.framed() {
		return m.selected("› "+label+" ", ansi.StringWidth(label)+3)
	}
	if active {
		label = m.primaryText().Render(label)
	} else if m.busy {
		label = m.muted().Render(label)
	}
	return m.label(label, active)
}

func (m *audioWorkbench) playbackActionRow(label string, active bool) string {
	return m.playerActionRow(label, active, 15)
}

func (m *audioWorkbench) playerActionRow(label string, active bool, width int) string {
	active = active && !m.busy
	if active {
		return m.selected("› "+label, max(width, ansi.StringWidth(label)+2))
	}
	return m.actionRow(label, false)
}

func (m *audioWorkbench) emptyAudioStatus() string {
	if m.playerNotice != "" {
		return m.warningStyle().Render(m.playerNotice)
	}
	return m.muted().Render("No recording yet")
}

func (m *audioWorkbench) resultRows(compact, full bool) []string {
	r := m.result
	title := strings.Join(strings.Fields(readable.Text(r.entry.Settings.Input)), " ")
	if title == "" {
		title = "Generated speech"
	}
	metadata := readable.Text(readable.ChoiceTitle(r.entry.Settings.Voice)) + " • " + strings.ToUpper(r.entry.Settings.Format) + " • " + audioFileSize(r.entry.Bytes)
	lines := []string{"  " + m.primaryText().Render(title)}
	if !full {
		return nil
	}
	lines = append(lines, "  "+m.muted().Render(metadata))
	if !compact {
		for range 1 + min(3, max(0, (m.audioHeight()-18)/3)) {
			lines = append(lines, "")
		}
	}
	position := audioDuration(r.playbackPosition().Seconds())
	longTime := false
	if r.hasWaveform() {
		height := 5
		if compact {
			height = 1
		}
		for index, row := range audioWaveform(r.analysis.Peaks, m.waveformWidth(), height, m.selectedBoundary()) {
			parts := strings.SplitN(row, "┃", 2)
			inset := 2
			if !compact {
				inset = max(2, (m.audioWidth()-ansi.StringWidth(row))/2)
			}
			prefix := strings.Repeat(" ", inset)
			if m.waveformFocused && index == height/2 {
				prefix = strings.Repeat(" ", inset-2) + m.accent().Render("› ")
			}
			style := m.waveformStyle()
			lines = append(lines, prefix+style.Render(parts[0])+m.primaryText().Render("┃")+style.Render(parts[1]))
		}
		if !compact {
			lines = append(lines, "")
		}
		timing, _ := m.playerTiming()
		if ansi.StringWidth(timing) > m.audioWidth()-2 {
			longTime = true
			timing = m.muted().Render("Duration " + audioDuration(r.analysis.Duration))
		}
		status := m.playerStatus()
		if ansi.StringWidth(status)+2+ansi.StringWidth(timing) <= m.audioWidth()-4 {
			timing = status + "  " + timing
		}
		if compact {
			lines = append(lines, "  "+timing)
		} else {
			lines = append(lines, m.centeredRow(timing))
		}
	} else {
		lines = append(lines, m.centeredRow(m.playerStatus()), "  "+m.muted().Render("Preview unavailable"))
		timing, _ := m.playerTiming()
		lines = append(lines, "  "+timing)
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
	playRow := m.playbackActionRow(play, m.focus == 4 && !m.waveformFocused && m.resultAction == 0)
	saveRow := m.playerActionRow("Save audio…", m.focus == 4 && !m.waveformFocused && m.resultAction == 1, 14)
	controlInset := max(2, (m.audioWidth()-44)/2)
	if !compact && ansi.StringWidth(playRow)+ansi.StringWidth(saveRow)+4 <= m.audioWidth() {
		gap := strings.Repeat(" ", max(4, 22-ansi.StringWidth(playRow)))
		lines = append(lines, strings.Repeat(" ", controlInset)+playRow+gap+saveRow)
	} else {
		lines = append(lines, playRow, saveRow)
	}
	if len(r.savedPaths) > 0 {
		destinationLabel, destinationPath := "Saved: ", r.savedPaths[len(r.savedPaths)-1]
		destinationInset := 2
		if !compact {
			destinationInset = controlInset + 2
		}
		available := max(0, m.audioWidth()-destinationInset-2-ansi.StringWidth(destinationLabel))
		destination := m.muted().Render(destinationLabel + audioPathSuffix(destinationPath, available))
		lines = append(lines, strings.Repeat(" ", destinationInset)+destination)
	}
	if !compact {
		lines = append(lines, "")
	}
	return append(lines, m.resultHintRows()...)
}

// Status describes the retained playback state, not whether output was audible.
func (m *audioWorkbench) playerStatus() string {
	label := "Ready"
	if m.playing {
		return m.accent().Bold(false).Render("Playing")
	}
	if m.result != nil {
		if m.result.finished {
			return m.playbackProgressStyle().Render("Completed")
		}
		if duration := m.result.seekDuration(); duration > 0 && m.result.playbackPosition() >= duration {
			label = "At end"
		} else if m.result.playbackPosition() > 0 {
			label = "Stopped"
		}
	}
	return m.muted().Render(label)
}

// Preserve the filename end without splitting a display grapheme or escape.
func audioPathSuffix(path string, available int) string {
	if available <= 0 {
		return ""
	}
	path = strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(readable.Text(path))
	width := 0
	for remaining := path; len(remaining) > 0; {
		_, cells, consumed := cliterminal.Sequence(remaining)
		width += cells
		remaining = remaining[consumed:]
	}
	if width <= available {
		return path
	}
	for len(path) > 0 && width > available-1 {
		_, cells, consumed := cliterminal.Sequence(path)
		path = path[consumed:]
		width -= cells
	}
	return "…" + path
}

func (m *audioWorkbench) resultHintRows() []string {
	var lines []string
	if m.focus == 4 {
		r := m.result
		playAction := "play"
		if m.playing {
			playAction = "stop"
		}
		navigation := [][2]string{{"←→", "choose"}}
		if r.hasWaveform() {
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
	var cursor *tea.Cursor
	for i, editor := range []textarea.Model{m.path, m.saveFolder} {
		name := []string{"Filename", "Save to"}[i]
		labelWidth := 14
		if compact {
			labelWidth = 8
		}
		if m.saveFocus == i {
			start := len(lines)
			lines = append(lines, m.inlineEditorRows(name, editor, true, labelWidth)...)
			cursor = audioEditorCursor(editor, labelWidth+3, start)
		} else {
			value := readable.Text(editor.Value())
			lines = append(lines, m.label(m.muted().Render(fmt.Sprintf("%-*s", labelWidth, name))+" "+value, false))
		}
	}
	if !compact {
		lines = append(lines, "")
	}
	lines = append(lines, m.actionRow("Save audio", m.saveFocus == 2))
	hint := m.hintLine([2]string{"Enter", "save"}, [2]string{"Tab", "edit"}, [2]string{"Esc", "back"})
	if m.saveFocus == 1 {
		hint = m.hintLine([2]string{"Tab", "next"}, [2]string{"Enter", "done"}, [2]string{"Esc", "back"})
	}
	lines = append(lines, m.feedbackLines()...)
	lines = append(lines, "  "+hint)
	return m.cursorView(lines, 1, cursor)
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
	lines := []string{m.strong().Render("  More actions"), ""}
	descriptions := map[string]string{
		"History":       "Browse retained recordings",
		"Preview voice": "Hear a short voice sample",
		"Clear form":    "Reset text and settings",
		"View command":  "Inspect the terminal command",
		"Help":          "Keyboard controls and tips",
		"Last error":    "Review the latest error",
	}
	for index, label := range m.moreItems() {
		row := label
		if m.framed() {
			row = fmt.Sprintf("%-18s", label) + m.muted().Render(ansi.Truncate(descriptions[label], m.audioWidth()-22, "…"))
		}
		lines = append(lines, m.choiceRow(row, index == m.moreChoice))
	}
	lines = append(lines, "", m.shortcutLine([2]string{"Enter", "choose"}, [2]string{"Esc", "back"}))
	return tea.NewView(m.fitAudio(lines, 1))
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
	visible := max(1, m.audioHeight()-len(lines)-len(footer))
	start := max(0, min(m.choice-visible+1, count-visible))
	for i := start; i < min(count, start+visible); i++ {
		lines = append(lines, m.choiceRow(readable.Text(m.choices[i]), i == m.choice))
	}
	return tea.NewView(m.fitAudio(append(lines, footer...), len(footer)))
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

// The footer count is structural; never infer controls from user-authored text.
func (m *audioWorkbench) fitAudio(lines []string, footerCount ...int) string {
	var rows []string
	for _, line := range lines {
		for _, row := range strings.Split(line, "\n") {
			if m.usable() {
				prefix := "  "
				if m.framed() {
					prefix = ""
				}
				rows = append(rows, prefix+ansi.Truncate(row, m.audioWidth(), "…"))
			} else {
				rows = append(rows, ansi.Truncate(row, max(1, m.width-1), "…"))
			}
		}
	}
	height := m.audioHeight()
	if !m.framed() {
		return strings.Join(rows[:min(len(rows), height)], "\n")
	}
	count := 0
	if len(footerCount) > 0 {
		count = min(footerCount[0], len(rows), height)
	}
	body, footer := rows[:len(rows)-count], rows[len(rows)-count:]
	body = body[:min(len(body), height-count)]
	rows = make([]string, height)
	copy(rows, body)
	copy(rows[height-count:], footer)
	border := m.borderStyle()
	width := m.audioWidth()
	framed := []string{border.Render("╭" + strings.Repeat("─", width) + "╮")}
	for _, row := range rows {
		framed = append(framed, border.Render("│")+row+strings.Repeat(" ", max(0, width-ansi.StringWidth(row)))+border.Render("│"))
	}
	return strings.Join(append(framed, border.Render("╰"+strings.Repeat("─", width)+"╯")), "\n")
}
