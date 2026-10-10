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

func (m *audioWorkbench) accent() lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.color {
		color := "#3159BC"
		if m.dark {
			color = "#8AA8FF"
		}
		s = s.Foreground(lipgloss.Color(color)).Bold(true)
	}
	return s
}

func (m *audioWorkbench) label(text string, active bool) string {
	if active {
		return m.accent().Render("> " + text)
	}
	return "  " + text
}

func (m *audioWorkbench) editorView(editor textarea.Model) string {
	// Keep colors within the shared palette. NO_COLOR also clears library defaults.
	style := textarea.Styles{}
	if m.color {
		style.Focused.Prompt = m.accent()
		style.Focused.CursorLine = lipgloss.NewStyle()
	}
	editor.SetStyles(style)
	return editor.View()
}

func (m *audioWorkbench) View() tea.View {
	if m.quit {
		return tea.NewView("")
	}
	if !m.usable() {
		return tea.NewView(m.fitAudio([]string{"Resize to 40 x 12.", "Ctrl+C exits."}))
	}
	lines := []string{m.accent().Render("Text to speech")}
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
	for i, editor := range []textarea.Model{m.input, m.instructions} {
		name := []string{"Text", "Instructions"}[i]
		if m.focus == i {
			lines = append(lines, m.label(name, true), m.editorView(editor))
		} else {
			value := readable.Text(strings.ReplaceAll(editor.Value(), "\n", " "))
			if value == "" {
				value = "(empty)"
			}
			lines = append(lines, m.label(name+": "+value, false))
		}
	}
	settings := []string{"Model: " + m.settings.Model, "Voice: " + m.settings.Voice, "Format: " + strings.ToUpper(m.settings.Format), fmt.Sprintf("Speed: %.2f x", m.settings.Speed), "Preview voice (generates and plays)", "Generate speech", "History", "Clear form"}
	for i := range settings {
		settings[i] = readable.Text(settings[i])
	}
	if compact {
		lines = append(lines, m.label(settings[m.setting], m.focus == 2))
	} else {
		for i := 0; i < 4; i++ {
			lines = append(lines, m.label(settings[i], m.focus == 2 && m.setting == i))
		}
		if m.setting >= 4 && m.focus == 2 {
			lines = append(lines, m.label(settings[m.setting], true))
		} else {
			lines = append(lines, "  Preview voice · Generate · History · Clear")
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
				lines = append(lines, m.accent().Render(audiomedia.Waveform(r.analysis.Peaks, min(94, m.width-4), height)))
				lines = append(lines, "  Duration "+audioDuration(r.analysis.Duration)+" · Space play/stop · S save")
			} else {
				lines = append(lines, "  Duration/waveform unavailable. S still saves.")
			}
			for _, path := range r.savedPaths {
				lines = append(lines, "  Saved: "+readable.Text(path))
			}
		}
	}
	preview := "Bash/zsh: " + readable.Text(m.commandPreview())
	if m.focus == 3 {
		offset := min(m.previewOffset, max(0, ansi.StringWidth(preview)-m.width+4))
		preview = ansi.Cut(preview, offset, ansi.StringWidth(preview))
	}
	footer := []string{m.label(preview, m.focus == 3)}
	if m.busy {
		footer = append(footer, fmt.Sprintf("%s · %.1f s · Ctrl+C cancels", m.note, time.Since(m.started).Seconds()))
	} else {
		hint := "Tab moves · Enter applies · Ctrl+G generates"
		if m.focus < 2 {
			hint = "Enter generates · Alt+Enter newline · Tab moves"
		}
		if m.focus == 2 {
			hint = "Up/Down settings · Enter chooses · Left/Right speed"
		}
		if m.focus == 4 {
			hint = "Space play/stop · X stop · S save · H history"
		}
		if m.focus == 3 {
			hint = "Left/Right scroll command · Enter generates"
		}
		footer = append(footer, hint, "History saves audio, text, instructions.")
	}
	if m.note != "" && !m.busy {
		footer = append(footer, m.feedbackLines()...)
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
	lines := strings.Split(ansi.Wrap(readable.Text(note), max(1, m.width-1), ""), "\n")
	if len(lines) > 2 {
		lines = []string{lines[0], ansi.Truncate(strings.Join(lines[1:], " "), max(1, m.width-1), "…")}
	}
	return lines
}

func (m *audioWorkbench) fitAudio(lines []string) string {
	var rows []string
	for _, line := range lines {
		for _, row := range strings.Split(line, "\n") {
			rows = append(rows, ansi.Truncate(row, max(1, m.width-1), "…"))
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
