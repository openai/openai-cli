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

// Reserve the frame and its inset before sizing editors or wrapping content.
func (m *audioWorkbench) framed() bool { return m.width >= 60 && m.height >= 24 }

func (m *audioWorkbench) audioWidth() int {
	if m.framed() {
		return min(92, m.width-8)
	}
	return min(96, max(1, m.width-4))
}

func (m *audioWorkbench) frameRows() int {
	if m.framed() {
		return 3 // Two border rows and the gap before keyboard guidance.
	}
	return 0
}

func (m *audioWorkbench) edge() lipgloss.Style {
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

func (m *audioWorkbench) divider() string {
	return m.edge().Render(strings.Repeat("─", m.audioWidth()))
}

// Use one frame per surface. Guidance stays outside the content panel.
func (m *audioWorkbench) panelView(lines []string, footer ...string) tea.View {
	if !m.framed() {
		return tea.NewView(m.fitAudio(append(lines, footer...)))
	}
	width := m.audioWidth()
	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if m.color {
		color := "#BAC4D8"
		if m.dark {
			color = "#51566B"
		}
		frame = frame.BorderForeground(lipgloss.Color(color))
	}
	var body []string
	for _, line := range lines {
		for _, row := range strings.Split(line, "\n") {
			body = append(body, ansi.Truncate(row, width, "…"))
		}
	}
	view := frame.Width(width + 4).Render(strings.Join(body, "\n"))
	rows := strings.Split(view, "\n")
	if len(footer) > 0 {
		rows = append(rows, "")
		rows = append(rows, footer...)
	}
	for i, row := range rows {
		rows[i] = "  " + row
	}
	return tea.NewView(strings.Join(rows, "\n"))
}

func (m *audioWorkbench) selectedRow(text string, active bool, width int) string {
	if !active {
		return "  " + ansi.Truncate(text, max(1, width-2), "…")
	}
	// Paint the complete row once. Nested metadata styles must not break its fill.
	text = "› " + ansi.Truncate(ansi.Strip(text), max(1, width-2), "…")
	text += strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
	style := m.accent()
	if m.color {
		fill := "#E9EFFE"
		if m.dark {
			fill = "#343D58"
		}
		style = style.Background(lipgloss.Color(fill))
	}
	return style.Render(text)
}

// Keyboard names carry color. Explanations retain secondary contrast.
func (m *audioWorkbench) keyHint(text string) string {
	parts := strings.Split(text, " · ")
	for i, part := range parts {
		key, description, _ := strings.Cut(part, " ")
		parts[i] = m.accent().Bold(false).Render(key)
		if description != "" {
			parts[i] += " " + m.muted().Render(description)
		}
	}
	return strings.Join(parts, m.muted().Render(" · "))
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
	label := m.muted()
	if active {
		label = m.accent().Bold(false)
	}
	rows[0] = m.label(label.Render(fmt.Sprintf("%-*s", labelWidth, name)), active) + " " + rows[0]
	for i := 1; i < len(rows); i++ {
		rows[i] = strings.Repeat(" ", labelWidth+3) + rows[i]
	}
	return rows
}

func (m *audioWorkbench) plainEditorRows(name string, editor textarea.Model, active bool) []string {
	label := m.muted()
	if active {
		label = m.accent().Bold(false)
	}
	rows := []string{m.label(label.Render(name), active)}
	for _, line := range strings.Split(m.editorView(editor), "\n") {
		rows = append(rows, "  "+line)
	}
	return rows
}

// The outer panel groups the form; editors never add another box.
func (m *audioWorkbench) formEditorRows(name string, editor textarea.Model, active bool) []string {
	if !active {
		editor.Blur()
	}
	if !m.framed() {
		if name == "Instructions" {
			return m.plainEditorRows(name, editor, active)
		}
		var rows []string
		for _, row := range strings.Split(m.editorView(editor), "\n") {
			rows = append(rows, "  "+row)
		}
		return rows
	}
	rows := m.inlineEditorRows(name, editor, active, 14)
	if name == "Text" {
		label := fmt.Sprintf("%-14s", name)
		if active {
			label = m.accent().Bold(false).Render(label)
		}
		rows[0] = "  " + label + " " + strings.Split(m.editorView(editor), "\n")[0]
	}
	return rows
}

func (m *audioWorkbench) shortcutLine(pairs ...[2]string) string {
	line := ""
	for _, pair := range pairs {
		item := m.keyHint(pair[0] + " " + pair[1])
		if line != "" {
			item = m.muted().Render(" · ") + item
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
		return tea.NewView(m.fitAudio([]string{"Resize to 40 x 12.", "Ctrl+C exits."}))
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
		lines := []string{m.strong().Render("  Speed"), "  " + m.editorView(m.speedInput), "  " + m.muted().Render("0.25–4.00×")}
		return m.panelView(append(lines, m.feedbackLines()...), "  "+m.keyHint("Enter apply · Esc back"))
	}

	compact := m.height < 24 || m.width < 60
	title := "Text to speech"
	if !compact {
		title = "  " + title
	}
	lines := []string{m.strong().Render(title)}
	if m.focus == 4 && m.result != nil {
		lines = append(lines, m.resultRows(compact, true)...)
		return m.panelView(lines[:len(lines)-1], lines[len(lines)-1])
	}
	lines = append(lines, m.formRows(compact, true)...)
	if !compact {
		lines = append(lines, "")
	}
	lines = append(lines, m.formActionRows()...)
	if !compact {
		next := "Settings"
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

	if !compact {
		return m.panelView(lines[:len(lines)-1], lines[len(lines)-1])
	}
	return tea.NewView(m.fitAudio(lines))
}

// Reuse the same field layout while opening secondary actions.
func (m *audioWorkbench) formRows(compact, interactive bool) []string {
	var lines []string
	for i, editor := range []textarea.Model{m.input, m.instructions} {
		if i == 1 && !compact {
			lines = append(lines, m.divider())
		}
		name := []string{"Text", "Instructions"}[i]
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
			label = m.muted().Render(label)
			lines = append(lines, m.label(label+" "+value, false))
		}
	}
	if !compact {
		lines = append(lines, "", m.muted().Render("  Settings"))
	}
	values := []string{m.settings.Model, readable.ChoiceTitle(m.settings.Voice), strings.ToUpper(m.settings.Format), fmt.Sprintf("%.2f x", m.settings.Speed)}
	if !audioSupportsVoice(m.settings.Model, m.settings.Voice) {
		values[1] += " (unavailable)"
	}
	for index, name := range []string{"Model", "Voice", "Format", "Speed"} {
		active := interactive && m.focus == 2 && m.setting == index
		label := fmt.Sprintf("%-14s", name)
		lines = append(lines, m.selectedRow(label+" "+readable.Text(values[index]), active, m.audioWidth()))
	}
	return lines
}

// Keep primary and secondary actions together when they fit the reading column.
func (m *audioWorkbench) formActionRows() []string {
	generate := "Generate speech"
	if m.busy {
		generate = m.progressText()
	}
	rows := []string{
		m.actionRow(generate, m.focus == 2 && m.setting == 4, true),
		m.actionRow("History", m.focus == 2 && m.setting == audioSettingHistory, false),
		m.actionRow("More actions…", m.focus == 2 && m.setting == audioSettingMore, false),
	}
	if m.height >= 24 && m.width >= 60 {
		joined := strings.Join(rows, "    ")
		if ansi.StringWidth(joined) <= m.audioWidth() {
			return []string{joined}
		}
	}
	return rows
}

func (m *audioWorkbench) actionRow(label string, active, primary bool) string {
	return m.selectedRow(label, active, ansi.StringWidth(label)+2)
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
		for _, row := range audioWaveform(r.analysis.Peaks, m.waveformWidth(), height, m.selectedBoundary()) {
			parts := strings.SplitN(row, "┃", 2)
			lines = append(lines, "  "+m.accent().Bold(false).Render(parts[0])+m.strong().Render("┃")+m.muted().Render(parts[1]))
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
		play = "At end · ← to seek"
	} else if longTime {
		play += " · " + position
	}
	playRow := m.actionRow(play, m.focus == 4 && m.resultAction == 0, false)
	saveRow := m.actionRow("Save audio…", m.focus == 4 && m.resultAction == 1, false)
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
	if m.focus == 4 {
		hint := "←→ seek · Space play · Esc back"
		if m.playing {
			hint = "←→ seek · Space stop · Esc back"
		}
		if r.seekDuration() <= 0 {
			hint = "Space play/stop · Esc back"
		}
		if m.resultAction == 1 {
			hint = "Enter save · Esc back"
		}
		if m.busy {
			hint = m.progressText()
		}
		lines = append(lines, "  "+m.keyHint(hint))
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
			value := readable.Text(strings.ReplaceAll(editor.Value(), "\n", " "))
			lines = append(lines, m.label(m.muted().Render(fmt.Sprintf("%-*s", labelWidth, name))+" "+value, false))
		}
	}
	if !compact {
		lines = append(lines, "")
	}
	lines = append(lines, m.actionRow("Save audio", m.saveFocus == 2, true))
	hint := "Enter save · Tab edit · Esc back"
	if m.saveFocus == 1 {
		hint = "Tab next · Enter done · Esc back"
	}
	if compact {
		lines = append(lines, "  "+m.keyHint(hint))
		return tea.NewView(m.fitAudio(append(lines, m.feedbackLines()...)))
	}
	lines = append(lines, m.feedbackLines()...)
	return m.panelView(lines, "  "+m.keyHint(hint))
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
	choice := max(0, min(m.moreChoice, len(items)-1))
	lines := []string{m.strong().Render("  More actions")}
	if m.framed() {
		prompt := strings.Join(strings.Fields(readable.Text(m.input.Value())), " ")
		if prompt != "" {
			lines = append(lines, "  "+ansi.Truncate(prompt, m.audioWidth()-2, "…"))
		}
		lines = append(lines, m.divider())
	}
	for index, label := range items {
		lines = append(lines, m.selectedRow(label, index == choice, m.audioWidth()))
	}
	if m.framed() {
		details := map[string]string{
			"History":       "Open a previous recording and its settings.",
			"Preview voice": "Generate and play a short voice sample.",
			"Clear form":    "Clear this draft. Keep saved audio and History.",
			"Show command":  "Inspect or print the command for this draft.",
			"Help":          "Read the audio controls and keyboard shortcuts.",
			"Last error":    "Review the last failure and recovery steps.",
		}
		lines = append(lines, m.divider(), "  "+m.muted().Render(details[items[choice]]))
	}
	return m.panelView(lines, "  "+m.keyHint("↑↓ choose · Enter open · Esc back"))
}

func (m *audioWorkbench) choiceView() tea.View {
	if m.modal == "history" {
		return m.historyView(time.Now())
	}
	title := []string{"Model", "Voice", "Response format"}[m.setting]
	count := len(m.choices)
	lines := []string{m.strong().Render("  " + title)}
	footer := []string{"  " + m.keyHint("Enter select · Esc back")}
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
		lines = append(lines, m.divider())
	}
	visible := max(1, m.height-1-m.frameRows()-len(lines)-len(footer))
	start := max(0, min(m.choice-visible+1, count-visible))
	for i := start; i < min(count, start+visible); i++ {
		lines = append(lines, m.selectedRow(readable.Text(m.choices[i]), i == m.choice, m.audioWidth()))
	}
	return m.panelView(lines, footer...)
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
