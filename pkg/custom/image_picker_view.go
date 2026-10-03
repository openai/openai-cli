package custom

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

func imagePickerTitle(value string) string {
	if value == "xhigh" {
		return "Extra high"
	}
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func imagePickerSizeLabel(size string) string {
	switch size {
	case "1024x1024":
		return "Square     1024 x 1024"
	case "1536x1024":
		return "Landscape  1536 x 1024"
	case "1024x1536":
		return "Portrait   1024 x 1536"
	default:
		return "Auto"
	}
}

func imagePickerLine(value string) string {
	return strings.NewReplacer("\n", " ↵ ", "\t", " → ").Replace(readable.Text(value))
}

func (m *imagePicker) heading() string {
	switch m.page {
	case "choose":
		return "Choose " + m.field
	case "more":
		return "More options"
	default:
		return "Settings"
	}
}

func (m *imagePicker) View() tea.View {
	view := tea.NewView("")
	if m.result.Canceled || len(m.result.Args) != 0 {
		return view
	}
	if m.width <= 0 || m.height <= 0 {
		return view
	}
	height := m.viewHeight()
	if m.width < 40 || m.height < 12 {
		view.Content = m.fit([]string{"Resize to at least 40 x 12.", "Esc prompt · Ctrl+C quit"}, m.width, height)
		return view
	}
	width := min(m.width-4, 100)
	strong := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle()
	selected := lipgloss.NewStyle().Bold(true)
	accent := selected
	border := lipgloss.NewStyle()
	input := lipgloss.NewStyle()
	focusColor, fillColor, textColor, mutedColor, borderColor := "#3159BC", "#E9EFFE", "#20283B", "#657087", "#BAC4D8"
	if m.dark {
		focusColor, fillColor, textColor, mutedColor, borderColor = "#8AA8FF", "#343D58", "#F2F4FA", "#A4ACC2", "#51566B"
	}
	if m.color {
		muted = muted.Foreground(lipgloss.Color(mutedColor))
		selected = selected.Foreground(lipgloss.Color(textColor)).Background(lipgloss.Color(fillColor))
		accent = accent.Foreground(lipgloss.Color(focusColor)).Background(lipgloss.Color(fillColor))
		border = border.Foreground(lipgloss.Color(borderColor))
		if m.focus == "prompt" {
			border = border.Foreground(lipgloss.Color(focusColor))
			input = input.Foreground(lipgloss.Color(textColor)).Background(lipgloss.Color(fillColor))
		}
	}
	highlight := func(text string, active bool) string {
		if active {
			text = ansi.Truncate(text, width-2, "…")
			return accent.Render("› ") + selected.Render(text+strings.Repeat(" ", max(0, width-2-ansi.StringWidth(text))))
		}
		return "  " + text
	}
	roomy := height >= 16
	_, commandHeight := m.commandDimensions()
	commandLines, commandStart, commandTotal := m.commandPreview(width-2, commandHeight)
	prompt := imagePickerLine(m.settings.prompt)
	if m.focus == "prompt" {
		before := imagePickerLine(string(m.draft[:m.cursor]))
		after := imagePickerLine(string(m.draft[m.cursor:]))
		leftWidth := max(0, ansi.StringWidth(before)-(width-6))
		before = ansi.TruncateLeft(before, leftWidth, "…")
		prompt = before + "▏" + after
	}
	if m.settings.prompt == "" {
		prompt = "Describe your image…"
		if m.focus == "prompt" {
			prompt = "▏ " + prompt
		}
	}
	lines := []string{strong.Render("openai") + "  " + muted.Render("Images")}
	if height >= 18 && m.width >= 50 {
		prompt = ansi.Truncate(prompt, width-4, "…")
		prompt += strings.Repeat(" ", max(0, width-4-ansi.StringWidth(prompt)))
		caption := "╭─ Prompt "
		lines = append(lines, "",
			border.Render(caption+strings.Repeat("─", width-ansi.StringWidth(caption)-1)+"╮"),
			border.Render("│")+input.Render(" "+prompt+" ")+border.Render("│"),
			border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	} else {
		lines = append(lines, highlight("Prompt", m.focus == "prompt"), "  "+prompt)
	}
	if roomy {
		lines = append(lines, "")
	}
	lines = append(lines, strong.Render(m.heading()))
	// Prompt, command and help remain visible. The option list scrolls
	// independently, keeping every selected choice accessible in a short terminal.
	reserved := len(lines) + 1 + len(commandLines)
	if m.note != "" {
		reserved++
	}
	if roomy {
		reserved++
	}
	available := max(1, height-reserved)
	rows := m.rows()
	start := max(0, min(m.selected-available+1, len(rows)-available))
	end := min(len(rows), start+available)
	for i := start; i < end; i++ {
		row := rows[i]
		text := row.label
		if row.id == "choice" {
			field := m.field
			if row.value == m.value(field) {
				text += "  ✓"
			}
			if row.detail != "" {
				text += "  " + row.detail
			}
		} else if row.value != "" {
			text = fmt.Sprintf("%-14s %s", row.label, imagePickerLine(row.value))
		}
		active := i == m.selected && m.focus == "options"
		lines = append(lines, highlight(ansi.Truncate(text, width-2, "…"), active))
	}
	if roomy {
		lines = append(lines, "")
	}
	for i, line := range commandLines {
		line = highlight(line, m.focus == "command" && i == 0)
		if m.focus != "command" {
			line = muted.Render(line)
		}
		lines = append(lines, line)
	}
	footer := "Ctrl+C exit · Enter select · ↑↓"
	if m.focus == "prompt" {
		footer = "Ctrl+C exit · Enter generate · ↓ settings"
		if ansi.StringWidth(footer) > width {
			footer = "Ctrl+C exit · Enter run · ↓ settings"
		}
	} else if m.focus == "command" {
		footer = "Ctrl+C exit · Enter generate"
		if commandTotal > len(commandLines) {
			footer = fmt.Sprintf("Ctrl+C exit · ↑↓ scroll %d-%d/%d · Enter generate", commandStart+1, commandStart+len(commandLines), commandTotal)
			if ansi.StringWidth(footer) > width {
				footer = "Ctrl+C exit · Enter run · ↑↓ scroll"
			}
		}
		if imagePickerShellQuoter(m.shell) != nil && ansi.StringWidth(footer+" · Ctrl+P print") <= width {
			footer += " · Ctrl+P print"
		}

	}
	if len(rows) > available && m.focus == "options" {
		footer = fmt.Sprintf("Ctrl+C exit · ↑↓ %d/%d · Enter select", m.selected+1, len(rows))
	}
	if m.note != "" {
		lines = append(lines, muted.Render(imagePickerLine(m.note)))
	}
	lines = append(lines, muted.Render(footer))
	view.Content = m.fit(lines, width, height)
	return view
}

// Leave room for previous shell output while keeping the active controls short.
// Tiny terminals retain a visible resize hint and keyboard cancellation.
func (m *imagePicker) viewHeight() int {
	if m.height < 12 {
		return max(0, min(m.height, 2))
	}
	return min(18, m.height-3)
}

func (m *imagePicker) commandPreview(width, height int) ([]string, int, int) {
	lines := m.commandLines(width)
	start := max(0, min(m.commandOffset, len(lines)-height))
	return lines[start:min(len(lines), start+height)], start, len(lines)
}

func (m *imagePicker) commandLines(width int) []string {
	command := formatImagePickerCommand(m.settings.args(), m.shell)
	if command == "" {
		command = imagePickerUnsupportedShell
	}
	var lines []string
	var line strings.Builder
	used, width := 0, max(1, width)
	for len(command) > 0 {
		sequence, cells, read := imagePickerSequence(command)
		command = command[read:]
		if used > 0 && used+cells > width {
			lines = append(lines, line.String())
			line.Reset()
			used = 0
		}
		line.WriteString(sequence)
		used += cells
	}
	return append(lines, line.String())
}

func (m *imagePicker) commandDimensions() (width, height int) {
	height = 1
	if m.viewHeight() >= 16 {
		height = 3
	}
	if m.viewHeight() >= 18 {
		height = 4
	}
	return max(1, min(m.width-4, 100)-2), height
}

func (m *imagePicker) clampCommandOffset() {
	width, height := m.commandDimensions()
	m.commandOffset = max(0, min(m.commandOffset, len(m.commandLines(width))-height))
}

func (m *imagePicker) scrollCommand(key string) bool {
	width, height := m.commandDimensions()
	switch key {
	case "up":
		m.commandOffset--
	case "down":
		m.commandOffset++
	case "pgup":
		m.commandOffset -= height
	case "pgdown":
		m.commandOffset += height
	case "home":
		m.commandOffset = 0
	case "end":
		m.commandOffset = len(m.commandLines(width)) - height
	default:
		return false
	}
	m.clampCommandOffset()
	return true
}

func (m *imagePicker) fit(lines []string, width, height int) string {
	lines = lines[:min(len(lines), height)]
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
		if m.width >= 40 && m.height >= 12 {
			lines[i] = "  " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}
