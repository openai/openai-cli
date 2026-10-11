package custom

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiomedia"
	"github.com/openai/openai-cli/internal/readable"
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

func (m *audioWorkbench) previewRows(compact bool) []string {
	preview := readable.Text(m.commandPreview())
	// Preserve the existing byte-exact, shell-quoted command and horizontal navigation.
	if m.focus == 3 {
		offset := min(m.previewOffset, max(0, ansi.StringWidth(preview)-m.audioWidth()+2))
		preview = ansi.Cut(preview, offset, ansi.StringWidth(preview))
	}
	height := 3
	if compact {
		height = 1
	}
	var rows []string
	for preview != "" && len(rows) < height {
		line := ansi.Cut(preview, 0, m.audioWidth()-2)
		preview = ansi.Cut(preview, ansi.StringWidth(line), ansi.StringWidth(preview))
		row := m.label(line, m.focus == 3 && len(rows) == 0)
		if m.focus != 3 {
			row = m.muted().Render(row)
		}
		rows = append(rows, row)
	}
	return rows
}

func (m *audioWorkbench) footerRows(compact bool) []string {
	footer := m.previewRows(compact)
	if !compact {
		footer = append([]string{""}, footer...)
	}
	if m.busy {
		footer = append(footer, fmt.Sprintf("%s · %.1f s · Ctrl+C cancels", m.note, time.Since(m.started).Seconds()))
	} else {
		hint := "↑↓/Tab move · Enter applies · Ctrl+G generates"
		if m.focus < 2 {
			hint = "↑↓/Tab move · Enter generates · Alt+Enter newline"
			if compact {
				hint = "↑↓/Tab move · Enter run · Alt+Enter ↵"
			}
		}
		if m.focus == 2 {
			hint = "↑↓ move · Enter chooses · ←→ speed"
		}
		if m.focus == 4 {
			hint = "Space play/stop · X stop · S save · H history"
		}
		if m.focus == 3 {
			hint = "Bash/zsh · ↑↓ scroll · Enter run"
		}
		footer = append(footer, hint, "History keeps audio/text/instructions")
	}
	if m.note != "" && !m.busy {
		footer = append(footer, m.feedbackLines()...)
	}
	return footer
}

func (m *audioWorkbench) View() tea.View {
	if m.quit {
		return tea.NewView("")
	}
	if !m.usable() {
		return tea.NewView(m.fitAudio([]string{"Resize to 40 x 12.", "Ctrl+C exits."}))
	}
	lines := []string{m.strong().Render("Text to speech")}
	if m.modal == "choice" || m.modal == "history" {
		title := "History"
		count := len(m.entries)
		if m.modal == "choice" {
			title = []string{"Model", "Voice", "Response format"}[m.setting]
			count = len(m.choices)
		}
		lines = append(lines, title)
		hint := "Up/Down choose · Enter apply · Esc back"
		if m.modal == "choice" && m.setting == 1 {
			hint = "Enter chooses · P generates and plays"
		}
		footer := []string{hint, "Choosing never generates speech."}
		if m.modal == "choice" && m.setting == 0 && strings.HasSuffix(m.choices[m.choice], "1106") {
			footer = append([]string{"Legacy snapshot; API access unverified."}, footer...)
		}
		footer = append(footer, m.feedbackLines()...)
		visible := max(1, m.height-1-len(lines)-len(footer))
		start := max(0, min(m.choice-visible+1, count-visible))
		for i := start; i < min(count, start+visible); i++ {
			var label string
			if m.modal == "choice" {
				label = m.choices[i]
			} else {
				e := m.entries[i]
				label = e.CreatedAt.Local().Format("Jan 02 15:04") + "  " + e.Settings.Voice + "  " + e.ID
			}
			lines = append(lines, m.label(readable.Text(label), i == m.choice))
		}
		if count == 0 {
			lines = append(lines, "No retained results.")
		}
		return tea.NewView(m.fitAudio(append(lines, footer...)))
	}
	if m.modal == "save" {
		lines = append(lines, "Save original audio to:", m.editorView(m.path), "Enter saves · Esc returns")
		lines = append(lines, m.feedbackLines()...)
		return tea.NewView(m.fitAudio(lines))
	}
	if m.modal == "speed" {
		lines = append(lines, "Speed from 0.25 to 4:", m.editorView(m.speedInput), "Enter applies · Esc returns")
		lines = append(lines, m.feedbackLines()...)
		return tea.NewView(m.fitAudio(lines))
	}
	compact := m.height < 24
	footer := m.footerRows(compact)
	for i, editor := range []textarea.Model{m.input, m.instructions} {
		name := []string{"Text", "Instructions"}[i]
		if m.focus == i || i == 0 && !compact && m.width >= 50 && m.result == nil {
			lines = append(lines, m.editorRows(name, editor, m.focus == i)...)
		} else {
			value := readable.Text(strings.ReplaceAll(editor.Value(), "\n", " "))
			if value == "" {
				value = "(optional)"
				if i == 0 {
					value = "(empty)"
				}
			}
			lines = append(lines, m.label(fmt.Sprintf("%-14s %s", name, value), false))
		}
	}
	settings := []string{
		fmt.Sprintf("%-14s %s", "Model", m.settings.Model),
		fmt.Sprintf("%-14s %s", "Voice", imagePickerTitle(m.settings.Voice)),
		fmt.Sprintf("%-14s %s", "Format", strings.ToUpper(m.settings.Format)),
		fmt.Sprintf("%-14s %.2f x", "Speed", m.settings.Speed),
		"Preview voice  Generate and play a sample", "Generate speech", "History", "Clear form",
	}
	for i := range settings {
		settings[i] = readable.Text(settings[i])
	}
	if compact {
		lines = append(lines, m.label(settings[m.setting], m.focus == 2))
	} else {
		lines = append(lines, m.strong().Render("  Settings"))
		count := len(settings)
		if m.result != nil {
			count = 5
		}
		available := max(1, m.height-1-len(footer)-len(lines))
		selected := m.setting
		if m.result != nil {
			selected = min(selected, 4)
		}
		start := 0
		if m.focus == 2 {
			start = max(0, selected-available+1)
		}
		for i := start; i < min(count, start+available); i++ {
			text, active := settings[i], m.focus == 2 && m.setting == i
			if m.result != nil && i == 4 {
				if m.focus == 2 && m.setting >= 4 {
					text, active = settings[m.setting], true
				} else {
					text = "Preview voice · Generate · History · Clear"
				}
			}
			lines = append(lines, m.label(text, active))
		}

	}
	if m.result != nil {
		r := m.result
		lines = append(lines, m.label(readable.Text("Result: "+r.entry.Settings.Voice+" · "+strings.ToUpper(r.entry.Settings.Format)+fmt.Sprintf(" · %d bytes", r.entry.Bytes)), m.focus == 4))
		if !compact || m.focus == 4 {
			if !compact {
				lines = append(lines, "  Model: "+readable.Text(r.entry.Settings.Model))
			}
			if r.measured {
				height := 5
				if compact {
					height = 1
				}
				for _, row := range strings.Split(audiomedia.Waveform(r.analysis.Peaks, max(1, m.audioWidth()-2), height), "\n") {
					lines = append(lines, "  "+m.accent().Render(row))
				}
				lines = append(lines, "  Duration "+audioDuration(r.analysis.Duration)+" · Space play/stop · S save")
			} else {
				lines = append(lines, "  Duration/waveform unavailable. S still saves.")
			}
			for _, path := range r.savedPaths {
				lines = append(lines, "  Saved: "+readable.Text(path))
			}
		}
	}
	budget := max(1, m.height-1-len(footer))
	// Keep the focused result visible when the terminal contracts.
	var flat []string
	for _, line := range lines {
		flat = append(flat, strings.Split(line, "\n")...)
	}
	if len(flat) > budget {
		if m.focus == 4 {
			flat = append(flat[:1], flat[len(flat)-(budget-1):]...)
		} else {
			flat = flat[:budget]
		}
	}
	return tea.NewView(m.fitAudio(append(flat, footer...)))
}

func (m *audioWorkbench) feedbackLines() []string {
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
	s := m.settings
	requestArgs, omit := fileReceiptRequestOptions(m.command.Root())
	if omit {
		return "Command preview unavailable with private request overrides."
	}
	body := map[string]any{"model": s.Model, "voice": s.Voice, "response_format": s.Format, "speed": s.Speed, "input": m.input.Value()}
	if m.instructions.Value() != "" {
		body["instructions"] = m.instructions.Value()
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
	return imagePickerShellQuote(value)
}
