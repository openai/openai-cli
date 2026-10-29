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

// A single tapered rule separates composing from listening.
func (m *audioWorkbench) listeningRule() string {
	fill, edge, secondary := "#E9EFFE", "#BAC4D8", "#657087"
	if m.dark {
		fill, edge, secondary = "#343D58", "#51566B", "#A4ACC2"
	}
	return "  " + cliterminal.SectionDivider(m.audioWidth()-4, m.color, fill, edge, secondary)
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

func (m *audioWorkbench) label(text string, active bool) string {
	if !active {
		return "  " + text
	}
	text = ansi.Truncate(text, max(1, m.audioWidth()-2), "…")
	return m.accent().Render("› ") + text
}

func (m *audioWorkbench) choiceRow(text string, active bool, width int) string {
	if active && m.framed() {
		return m.selected("› "+text, width)
	}
	return m.label(text, active)
}

func (m *audioWorkbench) editorView(editor textarea.Model) string {
	style := textarea.Styles{}
	// Use the terminal foreground for editable text. Only the caret marks focus.
	style.Focused.Placeholder, style.Blurred.Placeholder = m.muted(), m.muted()
	if m.color {
		style.Focused.Selection = m.accent().Underline(true)
	}
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
	return strings.Join(items, "  ")
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
		if m.inlineActions() {
			return m.workspaceView()
		}
		return m.moreView()
	case "choice", "history":
		return m.choiceView()
	case "save":
		return m.saveView()
	case "seek":
		if m.framed() {
			return m.workspaceView()
		}
		return m.seekView()
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

	if m.focus == 4 {
		lines := m.workspaceAudioHeader()
		footer := m.resultHintRows()
		controls := m.workspacePlayerControls()
		budget := m.audioHeight() - len(lines) - len(controls) - len(footer)
		// Keep the seek rail visible when a save receipt and recovery hint share this screen.
		if !m.result.hasWaveform() || budget > 2 {
			lines = append([]string{m.strong().Render("Text to speech")}, lines...)
			budget--
		}
		wave := m.workspaceWaveformRows(budget)
		free := budget - len(wave)
		if len(wave) > 0 && free > 0 && len(m.result.savedPaths) == 0 {
			// Keep the inline frame the same height when the save receipt appears.
			controls = append(controls, "")
			free--
		}
		if len(wave) > 0 && free > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, wave...)
		lines = append(lines, controls...)
		return tea.NewView(m.fitAudio(append(lines, footer...), len(footer)))
	}
	rows, cursor, active := m.formRows(true)
	actions := m.formActionRows()
	if m.focus == 2 && m.setting >= 4 && m.setting <= audioSettingMore {
		active = len(rows)
		if len(actions) > 1 {
			active += m.setting - 4
		}
	}
	rows = append(rows, actions...)
	footer := m.workspaceFooter()
	if warning := m.retainedGenerationErrorRow(); warning != "" {
		footer = append([]string{warning}, footer...)
	} else if m.result == nil {
		rows = append(rows, "  "+m.emptyAudioStatus())
	}
	// Keep the active field visible even in the smallest usable terminal.
	room := max(1, m.audioHeight()-len(footer))
	if cursor != nil {
		active = cursor.Y
	}
	start := max(0, active-room+2)
	start = min(start, max(0, len(rows)-room))
	if cursor != nil {
		cursor.Y -= start
	}
	rows = rows[start:min(len(rows), start+room)]
	return m.cursorView(append(rows, footer...), len(footer), cursor)
}

// Position the native caret from the rendered rows so disclosures and validation
// messages can move it without duplicating the layout in a second calculation.
func (m *audioWorkbench) formRows(compact bool) ([]string, *tea.Cursor, int) {
	labelWidth := m.formLabelWidth()
	rows := []string{"  " + m.strong().Render("Text to speech")}
	var cursor *tea.Cursor
	focusRow := 0
	if m.focus == 0 && !m.busy {
		cursor = audioEditorCursor(m.input, 2, len(rows))
	}
	for _, line := range strings.Split(m.editorView(m.input), "\n") {
		rows = append(rows, "  "+line)
	}
	validation := m.fieldValidationRows(audioFieldText)
	rows = append(rows, validation...)
	// A short draft already has a spare editor row before Instructions.
	spareEditorRow := m.input.LineCount() == 1 && m.input.LineInfo().Height < m.input.Height()
	if !compact && (!spareEditorRow || len(validation) > 0) {
		rows = append(rows, "")
	}
	instructions := m.instructions
	switch {
	case !m.instructionsSupported():
		label := m.muted().Render(fmt.Sprintf("%-*s", labelWidth, "Instructions"))
		rows = append(rows, "  "+label+" "+m.muted().Render("Not used by this model"))
		if !compact && instructions.Value() != "" {
			value := readable.Text(strings.Join(strings.Fields(instructions.Value()), " "))
			rows = append(rows, strings.Repeat(" ", labelWidth+3)+m.muted().Render(value))
		}
	case m.focus == 1 && !m.busy:
		if compact {
			cursor = audioEditorCursor(instructions, 2, len(rows)+1)
			rows = append(rows, m.plainEditorRows("Instructions", instructions, true)...)
		} else {
			cursor = audioEditorCursor(instructions, labelWidth+3, len(rows))
			rows = append(rows, m.inlineEditorRows("Instructions", instructions, true, labelWidth)...)
		}
	default:
		value := readable.Text(strings.Join(strings.Fields(instructions.Value()), " "))
		if value == "" {
			value = m.muted().Render("(optional)")
		}
		label := m.muted().Render(fmt.Sprintf("%-*s", labelWidth, "Instructions"))
		rows = append(rows, "  "+label+" "+value)
	}

	if !compact && m.height >= 32 {
		rows = append(rows, "")
	}
	fields := []int{1, audioSettingDisclosure}
	if m.settingsExpanded {
		fields = append(fields, 0, 2, 3)
	}
	for _, field := range fields {
		active := !m.busy && m.focus == 2 && m.setting == field
		if active {
			focusRow = len(rows)
		}
		var name, value string
		switch field {
		case 0:
			name, value = "Model", readable.Text(m.settings.Model)
		case 1:
			name, value = "Voice", readable.Text(readable.ChoiceTitle(m.settings.Voice))
			if !audioSupportsVoice(m.settings.Model, m.settings.Voice) {
				value = m.warningStyle().Render(value + " (unavailable)")
			}
		case 2:
			name, value = "Format", readable.Text(strings.ToUpper(m.settings.Format))
		case 3:
			name, value = "Speed", fmt.Sprintf("%.2f x", m.settings.Speed)
		case audioSettingDisclosure:
			name = "Settings"
			value = "▾"
			if !m.settingsExpanded {
				summary := fmt.Sprintf("%s  %s  %.2f×", m.settings.Model, strings.ToUpper(m.settings.Format), m.settings.Speed)
				value = "▸ " + m.muted().Render(readable.Text(summary))
			}
		}
		if active && field == 3 && m.speedEditing {
			cursor = audioEditorCursor(m.speedInput, labelWidth+3, len(rows))
			rows = append(rows, m.inlineEditorRows(name, m.speedInput, true, labelWidth)...)
		} else {
			label := m.muted().Render(fmt.Sprintf("%-*s", labelWidth, name))
			rows = append(rows, m.choiceRow(label+" "+value, active, m.composerWidth()))
		}
		rows = append(rows, m.fieldValidationRows(field)...)
	}
	return rows, cursor, focusRow
}

func (m *audioWorkbench) fieldValidationRows(field int) []string {
	message := m.fieldValidation(field)
	if message == "" {
		return nil
	}
	lines := strings.Split(ansi.Wrap(message, max(1, m.composerWidth()-2), ""), "\n")
	for i, line := range lines {
		lines[i] = "  " + m.warningStyle().Render(line)
	}
	return lines
}

// Keep related actions in one row, matching Left/Right navigation.
func (m *audioWorkbench) formActionRows() []string {
	active := !m.busy && m.focus == 2 && m.setting == 4
	label := m.primaryText().Render("Generate speech") + "  " + m.keyHint("Ctrl+G", "")
	generate := m.actionRow(label, active)
	if m.busy {
		generate = m.label(m.progressView(), active)
	}
	if m.inlineActions() {
		return []string{generate}
	}
	rows := []string{
		generate,
		m.actionRow(m.muted().Render("History"), m.focus == 2 && m.setting == audioSettingHistory),
		m.actionRow(m.muted().Render("More actions…"), m.focus == 2 && m.setting == audioSettingMore),
	}
	if m.framed() {
		// Selected actions include a trailing cell. Reserve it before focus moves.
		for i, setting := range []int{4, audioSettingHistory, audioSettingMore} {
			if m.busy || m.focus != 2 || m.setting != setting {
				rows[i] += " "
			}
		}
	}
	joined := strings.Join(rows, "  ")
	if ansi.StringWidth(joined) <= m.audioWidth() {
		return []string{joined}
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
	return m.muted().Render("Your recording will appear here.")
}

// Status describes the retained playback state, not whether output was audible.
func (m *audioWorkbench) playerStatus() string {
	if m.playing {
		return m.accent().Bold(false).Render("Playing")
	}
	if m.result != nil {
		if m.result.finished {
			return m.playbackProgressStyle().Render("Completed")
		}
		if duration := m.result.seekDuration(); duration > 0 && m.result.playbackPosition() >= duration {
			return m.warningStyle().Render("At end")
		} else if m.result.resumable {
			return m.warningStyle().Render("Paused")
		} else if m.result.playbackPosition() > 0 {
			return m.warningStyle().Render("Stopped")
		}
	}
	return m.muted().Render("Ready")
}

func (m *audioWorkbench) playheadStyle() lipgloss.Style {
	if !m.playing && m.result != nil && !m.result.finished && m.result.playbackPosition() > 0 {
		return m.warningStyle()
	}
	return m.primaryText()
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
		if r == nil && !m.busy {
			return m.formShortcutRows([2]string{"Ctrl+G", "Generate"}, [2]string{"Esc", "back"})
		}
		playAction := "play"
		if r != nil {
			playAction = strings.ToLower(m.playbackVerb())
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
		if r.seekDuration() > 0 {
			navigation = append(navigation, [2]string{"G", "time"})
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
		lines = append(lines, "  "+m.muted().Render(strings.ToUpper(m.result.entry.Settings.Format)+"  "+audioFileSize(m.result.entry.Bytes)+"  Full recording"))
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

var audioActionDescriptions = map[string]string{
	"History": "Find a recording", "Preview voice": "Hear a sample",
	"Clear form": "Reset the draft", "View command": "Inspect the command",
	"Help": "Keyboard controls", "Last error": "Recovery details",
}

func (m *audioWorkbench) actionName(label string) string {
	switch label {
	case "Clear form":
		return m.warningStyle().Render(label)
	case "Last error":
		return m.failureStyle().Render(label)
	default:
		return label
	}
}

func (m *audioWorkbench) withActionColumn(rows []string) []string {
	left := m.composerWidth()
	panel := []string{"  " + m.strong().Render("Actions"), ""}
	for index, label := range m.moreItems() {
		gap := strings.Repeat(" ", max(0, 16-ansi.StringWidth(label)))
		row := m.actionName(label) + gap + m.muted().Render(audioActionDescriptions[label])
		if m.modal == "more" && index == m.moreChoice {
			row = m.selected("› "+row, audioActionWidth)
		} else {
			row = "  " + ansi.Truncate(row, audioActionWidth-2, "…")
		}
		panel = append(panel, row)
	}
	for i := range max(len(rows), len(panel)) {
		if i >= len(rows) {
			rows = append(rows, "")
		}
		rows[i] = ansi.Truncate(rows[i], left, "…")
		if i < len(panel) && panel[i] != "" {
			rows[i] += strings.Repeat(" ", left+audioActionGap-ansi.StringWidth(rows[i])) + panel[i]
		}
	}
	return rows
}

func (m *audioWorkbench) moreView() tea.View {
	lines := []string{m.strong().Render("  More actions"), ""}
	for index, label := range m.moreItems() {
		row := m.actionName(label)
		if m.framed() {
			row += strings.Repeat(" ", 18-ansi.StringWidth(label)) + m.muted().Render(ansi.Truncate(audioActionDescriptions[label], m.audioWidth()-22, "…"))
		}
		lines = append(lines, m.choiceRow(row, index == m.moreChoice, m.audioWidth()))
	}
	lines = append(lines, "", m.shortcutLine([2]string{"↑↓", "choose"}, [2]string{"Enter", "open"}, [2]string{"Esc", "back"}))
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
			note = "Historical snapshot, access unverified"
		} else if strings.Contains(m.choices[m.choice], "-2025-") {
			note = "Snapshot, fixed model release"
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
		lines = append(lines, m.choiceRow(readable.Text(m.choices[i]), i == m.choice, m.audioWidth()))
	}
	return tea.NewView(m.fitAudio(append(lines, footer...), len(footer)))
}

func (m *audioWorkbench) feedbackLines() []string {
	if m.activeFailure() && m.note == m.failureNote {
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
