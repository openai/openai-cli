package custom

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiomedia"
	"github.com/openai/openai-cli/internal/readable"
	requestconfig "github.com/openai/openai-cli/internal/request"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Keep the audio form aligned with the image picker's palette and cell budgets.
func (m *audioWorkbench) audioWidth() int { return max(1, min(100, m.width-4)) }

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
	selected, accent := lipgloss.NewStyle(), m.accent()
	if m.color {
		fill, foreground := "#E9EFFE", "#20283B"
		if m.dark {
			fill, foreground = "#343D58", "#F2F4FA"
		}
		selected = selected.Bold(true).Foreground(lipgloss.Color(foreground)).Background(lipgloss.Color(fill))
		accent = accent.Background(lipgloss.Color(fill))
	}
	return accent.Render("› ") + selected.Render(text+strings.Repeat(" ", max(0, m.audioWidth()-2-ansi.StringWidth(text))))
}

func (m *audioWorkbench) editorView(editor textarea.Model) string {
	style := textarea.Styles{}
	style.Focused.Placeholder, style.Blurred.Placeholder = m.muted(), m.muted()
	if m.color {
		fill, foreground := "#E9EFFE", "#20283B"
		if m.dark {
			fill, foreground = "#343D58", "#F2F4FA"
		}
		style.Focused.CursorLine = lipgloss.NewStyle().Foreground(lipgloss.Color(foreground)).Background(lipgloss.Color(fill))
		style.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(foreground))
	}
	editor.SetStyles(style)
	return editor.View()
}

func (m *audioWorkbench) editorRows(name string, editor textarea.Model, active bool) []string {
	if m.height < 24 || m.width < 50 {
		return []string{m.label(name, active), "  " + m.editorView(editor)}
	}
	border := lipgloss.NewStyle()
	if m.color {
		color := "#BAC4D8"
		if m.dark {
			color = "#51566B"
		}
		border = border.Foreground(lipgloss.Color(color))
		if active {
			border = m.accent().Bold(false)
		}
	}
	caption := "╭ " + name + " "
	if active && !m.color {
		caption = "╭ › " + name + " "
	}
	width := m.audioWidth()
	rows := []string{border.Render(caption + strings.Repeat("─", max(0, width-ansi.StringWidth(caption)-1)) + "╮")}
	for _, line := range strings.Split(m.editorView(editor), "\n") {
		line = ansi.Truncate(line, width-4, "…")
		rows = append(rows, border.Render("│")+" "+line+strings.Repeat(" ", max(0, width-4-ansi.StringWidth(line)))+" "+border.Render("│"))
	}
	return append(rows, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
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
		lines := []string{m.strong().Render("Save audio"), "", "Save original audio to:", m.editorView(m.path), "Enter saves · Esc back"}
		return tea.NewView(m.fitAudio(append(lines, m.feedbackLines()...)))
	case "speed":
		lines := []string{m.strong().Render("Speed"), "", "Choose a speed from 0.25 to 4:", m.editorView(m.speedInput), "Enter applies · Esc back"}
		return tea.NewView(m.fitAudio(append(lines, m.feedbackLines()...)))
	}

	compact := m.height < 24 || m.width < 60
	lines := []string{m.strong().Render("Text to speech")}
	if !compact {
		lines = append(lines, "")
	}
	for i, editor := range []textarea.Model{m.input, m.instructions} {
		name := []string{"Text", "Instructions"}[i]
		if i == 1 && !m.instructionsSupported() {
			lines = append(lines, m.label(m.muted().Render("Instructions   Not used by this model"), false))
			if !compact && editor.Value() != "" {
				lines = append(lines, "  "+m.muted().Render(readable.Text(strings.ReplaceAll(editor.Value(), "\n", " "))))
			}
		} else if m.focus == i || !compact && m.result == nil {
			lines = append(lines, m.editorRows(name, editor, m.focus == i)...)
		} else {
			value := readable.Text(strings.ReplaceAll(editor.Value(), "\n", " "))
			if value == "" {
				value = "(optional)"
				if i == 0 {
					value = "(empty)"
				}
			}
			lines = append(lines, m.label(m.muted().Render(fmt.Sprintf("%-14s", name))+" "+value, false))
		}
		if !compact {
			lines = append(lines, "")
		}
	}
	if !compact {
		lines = append(lines, m.strong().Render("  Settings"))
	}
	values := []string{m.settings.Model, imagePickerTitle(m.settings.Voice), strings.ToUpper(m.settings.Format), fmt.Sprintf("%.2f x", m.settings.Speed)}
	if !audioSupportsVoice(m.settings.Model, m.settings.Voice) {
		values[1] += " (unavailable)"
	}
	for index, name := range []string{"Model", "Voice", "Format", "Speed"} {
		active := m.focus == 2 && m.setting == index
		label := fmt.Sprintf("%-14s", name)
		if !active {
			label = m.muted().Render(label)
		}
		lines = append(lines, m.label(label+" "+readable.Text(values[index]), active))
	}
	if !compact {
		lines = append(lines, "")
	}
	generate := "[ Generate speech ]"
	if m.busy {
		generate = m.progressText()
	}
	lines = append(lines, m.actionRow(generate, m.focus == 2 && m.setting == 4, true),
		"  "+m.muted().Render("Saves audio + inputs locally."),
		m.actionRow("More actions…", m.focus == 2 && m.setting == 5, false))

	if m.result != nil {
		if !compact {
			lines = append(lines, "")
		}
		full := m.focus == 4 || !compact && m.height >= 32
		lines = append(lines, m.resultRows(compact, full)...)
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

func (m *audioWorkbench) actionRow(label string, active, primary bool) string {
	if !active {
		if primary {
			label = m.accent().Render(label)
		} else {
			label = m.muted().Render(label)
		}
	}
	return m.label(label, active)
}

func (m *audioWorkbench) resultRows(compact, full bool) []string {
	r := m.result
	lines := []string{m.strong().Render("  Result: " + readable.Text(imagePickerTitle(r.entry.Settings.Voice)) + " · " + strings.ToUpper(r.entry.Settings.Format) + fmt.Sprintf(" · %d bytes", r.entry.Bytes))}
	if !full {
		return lines
	}
	if !compact {
		lines = append(lines, "  "+m.muted().Render(readable.Text(r.entry.Settings.Model)))
	}
	if r.measured {
		height := 5
		if compact {
			height = 1
		}
		for _, row := range strings.Split(audiomedia.Waveform(r.analysis.Peaks, max(1, m.audioWidth()-2), height), "\n") {
			lines = append(lines, "  "+m.accent().Render(row))
		}
		lines = append(lines, "  Duration "+audioDuration(r.analysis.Duration))
	} else {
		lines = append(lines, "  Audio preview unavailable.")
	}
	play := "Play audio"
	if m.playing {
		play = "Stop audio"
	}
	lines = append(lines, m.actionRow(play, m.focus == 4 && m.resultAction == 0, false),
		m.actionRow("Save audio…", m.focus == 4 && m.resultAction == 1, false))
	for _, path := range r.savedPaths {
		lines = append(lines, "  Saved: "+readable.Text(path))
	}
	return lines
}

func (m *audioWorkbench) moreView() tea.View {
	lines := []string{m.strong().Render("More actions"), ""}
	for index, label := range m.moreItems() {
		lines = append(lines, m.label(label, index == m.moreChoice))
	}
	if m.moreChoice == 0 {
		lines = append(lines, m.muted().Render("History keeps audio, text, and"), m.muted().Render("instructions on this device."))
	}
	return tea.NewView(m.fitAudio(lines))
}

func (m *audioWorkbench) choiceView() tea.View {
	title, count := "History", len(m.entries)
	if m.modal == "choice" {
		title = []string{"Model", "Voice", "Response format"}[m.setting]
		count = len(m.choices)
	}
	lines := []string{m.strong().Render(title), ""}
	footer := []string{m.muted().Render("Enter select · Esc back")}
	if m.modal == "history" {
		footer = append([]string{m.muted().Render("Audio and inputs saved on this device.")}, footer...)
	}
	if m.modal == "choice" && m.setting == 0 && strings.HasSuffix(m.choices[m.choice], "1106") {
		footer = append([]string{"Legacy model; API access unverified."}, footer...)
	}
	visible := max(1, m.height-1-len(lines)-len(footer))
	start := max(0, min(m.choice-visible+1, count-visible))
	for i := start; i < min(count, start+visible); i++ {
		label := ""
		if m.modal == "choice" {
			label = m.choices[i]
		} else {
			entry := m.entries[i]
			label = entry.CreatedAt.Local().Format("Jan 02 15:04") + "  " + entry.Settings.Voice + "  " + entry.ID
		}
		lines = append(lines, m.label(readable.Text(label), i == m.choice))
	}
	if count == 0 {
		lines = append(lines, "No saved audio yet.")
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
