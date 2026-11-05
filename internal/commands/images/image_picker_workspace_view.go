package images

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Keep the form's geometry independent of request state and result history.
// Settings use the same bounded area for their overview and editable list.
func (m *imagePicker) workspaceView(view tea.View, prompt string, strong, muted, accent, border, keyStyle lipgloss.Style) tea.View {
	width, height := m.width-8, m.viewHeight()
	framed := height >= 16 && m.width >= 50
	frameRows := 0
	if framed {
		frameRows = 2
	}
	lines := []string{strong.Render("Create image")}
	label := muted
	if m.focus == "prompt" && !m.generating {
		label = accent.Bold(false)
	}
	const promptPrefix = "Prompt "
	promptInset := ansi.StringWidth(promptPrefix)
	promptWidth := max(1, width-promptInset)
	if m.focus == "prompt" {
		var x int
		prompt, x = imagePickerEditLine(m.draft, m.cursor, promptWidth)
		if len(m.draft) == 0 {
			prompt = muted.Render("Describe your image…")
		}
		view.Cursor = tea.NewCursor(4+promptInset+x, len(lines)+frameRows/2)
		view.Cursor.Shape = tea.CursorBar
	}
	prompt = cliterminal.FitPrompt(prompt, promptWidth)
	lines = append(lines, label.Render("Prompt")+" "+prompt)
	rule := m.workspaceImageBorder(strings.Repeat("─", width))
	if height >= 12 {
		lines = append(lines, rule)
	}
	command := m.workspaceCommandLines(width, strong.Bold(false), muted, accent)
	footer := m.footer(m.width-6, keyStyle, muted)
	commandRule := height >= 12
	reserved := len(lines) + 1 + len(command) + len(footer) + frameRows
	// Keep one row available for results in short, unframed terminals.
	if !framed {
		reserved++
	}
	if commandRule {
		reserved++
	}
	if m.page == "path" {
		reserved++
	}
	available := max(1, height-reserved)
	rows := m.rows()
	start, end := m.optionWindow(len(rows), available)
	heading := m.heading()
	if len(rows) > available {
		heading += fmt.Sprintf(" · %d–%d of %d", start+1, end, len(rows))
	}
	headingStyle := muted
	if !m.generating && (m.focus == "options" || m.focus == "path") {
		headingStyle = accent.Bold(false)
	}
	lines = append(lines, headingStyle.Render(heading))
	if m.page == "path" {
		path, x := imagePickerEditLine(m.folder.draft, m.folder.cursor, width)
		if len(m.folder.draft) == 0 {
			path = muted.Render("Enter a folder path…")
		}
		if m.focus == "path" {
			view.Cursor = tea.NewCursor(4+x, len(lines)+frameRows/2)
			view.Cursor.Shape = tea.CursorBar
		}
		lines = append(lines, path)
	}
	selection := strong
	if m.color {
		fill := "#E9EFFE"
		if m.dark {
			fill = "#343D58"
		}
		selection = accent.Background(lipgloss.Color(fill)).Width(width - 2)
	}
	lines = append(lines, m.optionLines(rows, start, end, width, selection, accent, muted)...)
	if commandRule {
		lines = append(lines, rule)
	}
	lines = append(lines, command...)
	body := make([]string, 0, len(lines)+frameRows+len(footer)+1)
	if framed {
		body = append(body, m.workspaceImageFrame("", m.width, true))
	}
	for _, line := range lines {
		line = ansi.Truncate(line, width, "…")
		if framed {
			body = append(body, m.workspaceImageRow(line, m.width))
		} else {
			body = append(body, "    "+line)
		}
	}
	if framed {
		body = append(body, m.workspaceImageFrame("", m.width, false))
	}
	for _, line := range footer {
		body = append(body, "    "+line)
	}
	view.Content = strings.Join(body, "\n")
	return view
}

// Keep the command quiet while editing. Focusing it reveals the complete,
// scrollable preview; command printing always retains the original bytes.
func (m *imagePicker) workspaceCommandLines(width int, text, muted, accent lipgloss.Style) []string {
	if m.focus != "command" {
		command := cliterminal.FormatCommand(m.settings.args(), m.shell)
		if command == "" {
			command = imagePickerUnsupportedShell
		}
		const label, gap = "Command", "   "
		available := max(1, width-ansi.StringWidth(label+gap))
		return []string{text.Render(label) + gap + muted.Render(ansi.Truncate(command, available, "…"))}
	}
	commandWidth, commandHeight := m.commandDimensions()
	command, _, _ := m.commandPreview(commandWidth, commandHeight)
	label := muted
	if !m.generating {
		label = accent.Bold(false)
	}
	lines := []string{label.Render("Command")}
	for _, line := range command {
		lines = append(lines, muted.Render(line))
	}
	return lines
}

// Show contextual shortcuts and retain their row count during a request.
func (m *imagePicker) workspaceFooter(width int, key, muted lipgloss.Style) []string {
	primary := [][2]string{{"Ctrl+C", "Exit"}}
	secondary := [][2]string{{"Ctrl+G", "Generate"}}
	switch m.focus {
	case "prompt":
		secondary = append(secondary, [2]string{"Tab", "Settings"})
		secondary = append(secondary, [2]string{"Alt+Enter", "New line"})
	case "images":
		secondary = [][2]string{{"Esc", "Prompt"}, {"←→", "Images"}, {"↑↓", "Scroll"}, {"O", "Open"}}
	case "command":
		secondary = [][2]string{{"Esc", "Prompt"}}
		commandWidth, commandHeight := m.commandDimensions()
		if len(m.commandLines(commandWidth)) > commandHeight {
			secondary = append(secondary, [2]string{"↑↓", "Scroll"})
		}
	case "path":
		secondary = [][2]string{{"Tab", "Complete"}, {"Esc", "Back"}}
	default:
		secondary = append(secondary, [2]string{"Esc", "Prompt"})
	}

	rows := imagePickerWorkspaceShortcutRows(append(primary, secondary...), width, key, muted)
	controls := [][2]string{{"Ctrl+C", "Cancel generation"}}
	if m.focus == "images" {
		controls = append(controls, [2]string{"Esc", "Prompt"}, [2]string{"←→", "Images"}, [2]string{"↑↓", "Scroll"}, [2]string{"O", "Open"})
	} else {
		controls = append(controls, [2]string{"Tab", "Images"})
	}
	busy := imagePickerWorkspaceShortcutRows(controls, width, key, muted)
	// The first arriving image enables navigation. Reserve that space before
	// submission, without advertising image navigation before results exist.
	count := max(len(rows), len(busy))
	if m.generating {
		if m.hasImages {
			rows = busy
		} else {
			rows = imagePickerWorkspaceShortcutRows(controls[:1], width, key, muted)
		}
	}
	for len(rows) < count {
		rows = append(rows, "")
	}
	return rows
}

// Wrap complete key/action pairs only when they do not fit on the current row.
func imagePickerWorkspaceShortcutRows(shortcuts [][2]string, width int, key, muted lipgloss.Style) []string {
	var rows []string
	start, used := 0, 0
	for i, shortcut := range shortcuts {
		cells := ansi.StringWidth(shortcut[0] + " " + strings.ToLower(shortcut[1]))
		if i > start && used+3+cells > width {
			rows = append(rows, imagePickerWorkspaceShortcutLine(shortcuts[start:i], width, key, muted))
			start, used = i, 0
		}
		if i > start {
			used += 3
		}
		used += cells
	}
	return append(rows, imagePickerWorkspaceShortcutLine(shortcuts[start:], width, key, muted))
}

// Keep key names distinct without turning every shortcut into a bold badge.
func imagePickerWorkspaceShortcutLine(shortcuts [][2]string, width int, key, muted lipgloss.Style) string {
	var parts []string
	used := 0
	for _, shortcut := range shortcuts {
		label := strings.ToLower(shortcut[1])
		cells := ansi.StringWidth(shortcut[0] + " " + label)
		gap := 0
		if len(parts) > 0 {
			gap = 3
		}
		if used+gap+cells > width {
			continue
		}
		parts = append(parts, key.Bold(false).Render(shortcut[0])+" "+muted.Render(label))
		used += gap + cells
	}
	return strings.Join(parts, muted.Render(" · "))
}
