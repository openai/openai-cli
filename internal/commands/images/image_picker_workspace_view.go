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
	width, height := m.width-4, m.viewHeight()
	lines := []string{strong.Render("  Create image")}
	if height >= 12 && m.width >= 50 {
		caption := "╭ Prompt "
		prompt = cliterminal.FitPrompt(prompt, width-4)
		lines = append(lines,
			border.Render(caption+strings.Repeat("─", width-ansi.StringWidth(caption)-1)+"╮"),
			border.Render("│")+ansi.EraseCharacter(width-2)+" "+prompt+" "+ansi.CursorHorizontalAbsolute(width+2)+border.Render("│"),
			border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	} else {
		caption := "  Prompt"
		if m.focus == "prompt" {
			caption = "› Prompt"
		}
		lines = append(lines, border.Render(caption), "  "+prompt)
	}
	commandWidth, commandHeight := m.commandDimensions()
	command, commandStart, commandTotal := m.commandPreview(commandWidth, commandHeight)
	footer := m.footer(width-2, keyStyle, muted)
	commandHeading := height >= 12
	reserved := len(lines) + 1 + commandHeight + len(footer)
	if height >= 12 {
		reserved += 3 // Separate sections without empty command rows.
	}
	if commandHeading {
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
	lines = append(lines, strong.Render("  "+heading))
	if m.page == "path" {
		path, x := imagePickerEditLine(m.folder.draft, m.folder.cursor, width-2)
		if len(m.folder.draft) == 0 {
			path = muted.Render("Enter a folder path…")
		}
		if m.focus == "path" {
			view.Cursor = tea.NewCursor(4+x, len(lines))
			view.Cursor.Shape = tea.CursorBar
		}
		lines = append(lines, "  "+path)
	}
	lines = append(lines, m.optionLines(rows, start, end, width, strong, accent, muted)...)
	if height >= 12 {
		lines = append(lines, "")
	}
	if commandHeading {
		label := "Command preview"
		if commandTotal > len(command) {
			label += fmt.Sprintf(" · %d–%d of %d", commandStart+1, commandStart+len(command), commandTotal)
		}
		lines = append(lines, "  "+muted.Render(label))
	}
	for i, line := range command {
		if !m.generating && m.focus == "command" && i == 0 {
			lines = append(lines, accent.Render("› ")+strong.Render(line))
		} else {
			lines = append(lines, "  "+muted.Render(line))
		}
	}
	if height >= 12 {
		lines = append(lines, "")
	}
	for _, line := range footer {
		lines = append(lines, "  "+line)
	}
	if height >= 12 {
		lines = append(lines, "")
	}
	view.Content = m.fit(lines, width, height)
	return view
}

// Show contextual shortcuts and retain their row count during a request.
func (m *imagePicker) workspaceFooter(width int, key, muted lipgloss.Style) []string {
	primary := [][2]string{{"Ctrl+C", "Exit"}}
	secondary := [][2]string{{"Ctrl+G", "Generate"}}
	switch m.focus {
	case "prompt":
		if m.hasImages {
			secondary = append(secondary, [2]string{"Shift+Tab", "Images"})
		} else {
			secondary = append(secondary, [2]string{"Tab", "Settings"})
		}
		secondary = append(secondary, [2]string{"Alt+Enter", "New line"})
	case "images":
		secondary = [][2]string{{"↑↓", "Scroll"}, {"←→", "Images"}, {"O", "Open"}, {"Enter", "View"}, {"Esc", "Prompt"}}
	case "command":
		secondary = [][2]string{{"Esc", "Prompt"}}
		if cliterminal.ShellQuoter(m.shell) != nil {
			secondary = append(secondary, [2]string{"Ctrl+P", "Print"})
		}
	case "path":
		secondary = [][2]string{{"Tab", "Complete"}, {"Esc", "Back"}}
	default:
		secondary = append(secondary, [2]string{"Esc", "Prompt"})
	}

	rows := []string{imagePickerShortcutLine(append(append([][2]string(nil), primary...), secondary...), width, key, muted)}
	if width < 84 {
		rows = []string{imagePickerShortcutLine(primary, width, key, muted), imagePickerShortcutLine(secondary, width, key, muted)}
	}
	if m.generating {
		controls := [][2]string{{"Ctrl+C", "Cancel generation"}}
		if m.hasImages {
			if m.focus == "images" {
				controls = append(controls, [2]string{"↑↓", "Scroll"}, [2]string{"Enter", "View"}, [2]string{"O", "Open"}, [2]string{"Esc", "Prompt"})
			} else {
				controls = append(controls, [2]string{"Tab", "Images"})
			}
		}
		rows[0] = imagePickerShortcutLine(controls, width, key, muted)
		if len(rows) > 1 {
			rows[0] = imagePickerShortcutLine(controls[:1], width, key, muted)
			rows[1] = imagePickerShortcutLine(controls[1:], width, key, muted)
		}
	}
	return rows
}
