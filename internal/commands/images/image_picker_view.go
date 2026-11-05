package images

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

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
	case "folder", "path":
		return "Save to"
	case "choose":
		if m.field == "count" {
			return "Choose image count"
		}
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
	if m.workspace {
		width = m.width - 4
	}
	strong := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle()
	selected := lipgloss.NewStyle().Bold(true)
	accent := selected
	keyStyle := strong
	border := lipgloss.NewStyle()
	focusColor, textColor, mutedColor, borderColor := "#3159BC", "#20283B", "#657087", "#BAC4D8"
	if m.dark {
		focusColor, textColor, mutedColor, borderColor = "#8AA8FF", "#F2F4FA", "#A4ACC2", "#51566B"
	}
	if m.color {
		keyStyle = keyStyle.Foreground(lipgloss.Color(focusColor))
		muted = muted.Foreground(lipgloss.Color(mutedColor))
		selected = selected.Foreground(lipgloss.Color(textColor))
		accent = accent.Foreground(lipgloss.Color(focusColor))
		border = border.Foreground(lipgloss.Color(borderColor))
		if m.focus == "prompt" {
			border = border.Foreground(lipgloss.Color(focusColor))
		}
	}
	highlight := func(text string, active bool) string {
		if active {
			text = ansi.Truncate(text, width-2, "…")
			return accent.Render("› ") + selected.Render(text)
		}
		return "  " + text
	}
	prompt := imagePickerLine(m.settings.prompt)
	if m.focus == "prompt" {
		var x int
		prompt, x = imagePickerEditLine(m.draft, m.cursor, width-4)
		view.Cursor = tea.NewCursor(4+x, 2)
		view.Cursor.Shape = tea.CursorBar
	}
	if m.settings.prompt == "" {
		prompt = "Describe your image…"
		prompt = muted.Render(prompt)
	}
	if m.workspace {
		return m.workspaceView(view, prompt, strong, muted, accent, border, keyStyle)
	}
	roomy := height >= 16
	commandWidth, commandHeight := m.commandDimensions()
	commandLines, commandStart, commandTotal := m.commandPreview(commandWidth, commandHeight)
	lines := []string{strong.Render("Create image")}
	if height >= 18 && m.width >= 50 {
		prompt = cliterminal.FitPrompt(prompt, width-4)
		caption := "╭ Prompt "
		// Match the painter's conservative width budget. Position the closing
		// edge explicitly because terminals disagree on emoji cluster widths.
		row := border.Render("│") + ansi.EraseCharacter(width-2) + " " + prompt + " " +
			ansi.CursorHorizontalAbsolute(width+2) + border.Render("│")
		lines = append(lines,
			border.Render(caption+strings.Repeat("─", width-ansi.StringWidth(caption)-1)+"╮"),
			row,
			border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	} else {
		caption := "  Prompt"
		if m.focus == "prompt" {
			caption = "› Prompt"
		}
		lines = append(lines, border.Render(caption), "  "+prompt)
	}
	headingText := m.heading()
	if height < 12 && m.page == "folder" {
		headingText = "Save to"
	}
	heading := strong.Render("  " + headingText)
	if height < 12 && m.page != "path" {
		key, action := "Tab", "Switch"
		if m.focus == "command" {
			key, action = "↑↓", "Scroll"
		}
		heading += "  " + keyStyle.Render("["+key+"]") + " " + muted.Render(action)
	}
	lines = append(lines, heading)
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
	// Prompt, command and contextual help remain visible. The option list scrolls
	// independently, keeping every selected choice accessible in a short terminal.
	footer := m.footer(width-2, keyStyle, muted)
	reserved := len(lines) + len(commandLines) + len(footer)
	if m.note != "" {
		reserved++
	}
	rows := m.rows()
	commandHeading := roomy && height-reserved > len(rows)
	if commandHeading {
		reserved++
	}
	commandGap := roomy && height-reserved > len(rows)
	if commandGap {
		reserved++
	}
	footerGap := roomy && height-reserved > len(rows)
	if footerGap {
		reserved++
	}
	available := max(1, height-reserved)
	start, end := m.optionWindow(len(rows), available)
	lines = append(lines, m.optionLines(rows, start, end, width, selected, accent, muted)...)
	if commandGap {
		lines = append(lines, "")
	}
	commandLabel := "Command preview"
	if commandTotal > len(commandLines) {
		commandLabel += fmt.Sprintf(" · %d–%d of %d", commandStart+1, commandStart+len(commandLines), commandTotal)
	}
	if commandHeading {
		lines = append(lines, muted.Render("  "+commandLabel))
	}
	for i, line := range commandLines {
		line = highlight(line, m.focus == "command" && i == 0)
		if m.focus != "command" {
			line = muted.Render(line)
		}
		lines = append(lines, line)
	}
	if footerGap {
		lines = append(lines, "")
	}
	if m.note != "" {
		lines = append(lines, "  "+muted.Render(imagePickerLine(m.note)))
	}
	for _, line := range footer {
		lines = append(lines, "  "+line)
	}
	view.Content = m.fit(lines, width, height)
	return view
}

// Share choice alignment and escaping between inline and workspace forms.
func (m *imagePicker) optionLines(rows []imagePickerRow, start, end, width int, selected, accent, muted lipgloss.Style) []string {
	var lines []string
	choiceWidth := 0
	for _, row := range rows {
		if row.id == "choice" {
			choiceWidth = max(choiceWidth, ansi.StringWidth(row.label))
		}
	}
	choiceWidth = min(choiceWidth, width-5)
	for i := start; i < end; i++ {
		row := rows[i]
		text := row.label
		if row.id == "choice" {
			text = ansi.Truncate(text, choiceWidth, "…")
			text += strings.Repeat(" ", choiceWidth-ansi.StringWidth(text)) + "  "
			if row.value == m.value(m.field) {
				text += "✓"
			} else {
				text += " "
			}
			if row.detail != "" {
				text += "  " + row.detail
			}
		} else if row.value != "" {
			text = fmt.Sprintf("%-14s %s", row.label, imagePickerLine(row.value))
		}
		active := (!m.workspace || !m.generating) && i == m.selected && (m.focus == "options" || m.focus == "path" && m.folder.match >= 0)
		if m.page == "path" && row.id == "" {
			text = muted.Render(text)
		}
		text = ansi.Truncate(text, width-2, "…")
		if active {
			lines = append(lines, accent.Render("› ")+selected.Render(text))
		} else {
			lines = append(lines, "  "+text)
		}
	}
	return lines
}

// Cache only the visible option window. Repeated views keep the same offset;
// page changes reset it, and selection or size changes move it only as needed.
func (m *imagePicker) optionWindow(length, available int) (start, end int) {
	if m.optionPage != m.page || m.optionField != m.field {
		m.optionOffset, m.optionPage, m.optionField = 0, m.page, m.field
	}
	start = max(0, min(m.optionOffset, length-available))
	selected := max(0, min(m.selected, length-1))
	if selected < start {
		start = selected
	} else if selected >= start+available {
		start = selected - available + 1
	}
	m.optionOffset = start
	return start, min(length, start+available)
}

// Return the visible draft and terminal cursor column without adding text cells.
// Preserve complete clusters when the rune cursor sits inside a combined glyph.
func imagePickerEditLine(draft []rune, cursor, width int) (string, int) {
	text := imagePickerLine(string(draft))
	position := len(imagePickerLine(string(draft[:cursor])))
	start := len(text)
	for offset := 0; offset < len(text); {
		_, _, read := cliterminal.Sequence(text[offset:])
		if position < offset+read {
			start = offset
			break
		}
		offset += read
	}
	before := imagePickerPromptTail(text[:start], max(1, width-2))
	x := 0
	for rest := before; len(rest) > 0; {
		_, cells, read := cliterminal.Sequence(rest)
		x += cells
		rest = rest[read:]
	}
	return before + text[start:], x
}

// Fit complete clusters using the same width bound as the inline painter.
func imagePickerPromptTail(text string, width int) string {
	remaining := 0
	for rest := text; len(rest) > 0; {
		_, cells, read := cliterminal.Sequence(rest)
		remaining += cells
		rest = rest[read:]
	}
	if remaining <= width {
		return text
	}
	for remaining > width-1 {
		_, cells, read := cliterminal.Sequence(text)
		remaining -= cells
		text = text[read:]
	}
	return "…" + text
}

// Leave room for previous shell output while keeping the active controls short.
// Tiny terminals retain a visible resize hint and keyboard cancellation.
func (m *imagePicker) viewHeight() int {
	if m.height < 12 {
		return max(0, min(m.height, 2))
	}
	if m.workspace {
		limit := 19
		if m.width < 90 {
			limit = 20
		}
		return min(limit, max(9, m.height-4))
	}
	return min(21, m.height-3)
}

// Keep whole key/action pairs together, using the same visual language as Agents.
// Short terminals keep submission and cancellation visible in one row.
func (m *imagePicker) footer(width int, keyStyle, muted lipgloss.Style) []string {
	if m.workspace {
		return m.workspaceFooter(width, keyStyle, muted)
	}
	if m.generating {
		return []string{keyStyle.Render("[Ctrl+C]") + " " + muted.Render("Cancel generation"), muted.Render("Your prompt and settings stay here.")}
	}
	primary := [][2]string{{"↑↓", "Move"}, {"Enter", "Select"}, {"Ctrl+G", "Generate"}}
	secondary := [][2]string{{"Tab", "Switch"}, {"Ctrl+C", "Quit"}}
	enter := "Select"
	switch m.focus {
	case "prompt":
		enter = "Generate"
		primary = [][2]string{{"Enter", enter}, {"↓", "Settings"}, {"Alt+Enter", "New line"}}
		secondary = [][2]string{{"Tab", "Switch"}, {"Ctrl+C", "Quit"}, {"Ctrl+G", "Generate"}}
	case "command":
		enter = "Generate"
		primary = [][2]string{{"Enter", enter}, {"↑↓", "Scroll"}}
		if cliterminal.ShellQuoter(m.shell) != nil {
			primary = append(primary, [2]string{"Ctrl+P", "Print command"})
		}
	case "path":
		enter = "Use folder"
		primary = [][2]string{{"Enter", enter}, {"Tab", "Complete"}}
		secondary = [][2]string{{"Esc", "Back"}, {"Ctrl+C", "Quit"}}
	case "images":
		enter = "Expand images"
		primary = [][2]string{{"Enter", enter}, {"↑↓", "Select"}}
		secondary = [][2]string{{"Tab", "Switch"}, {"Ctrl+C", "Quit"}, {"Ctrl+G", "Generate"}}
	}
	if m.viewHeight() < 12 && !m.workspace {
		return []string{imagePickerShortcutLine([][2]string{{"Enter", enter}, {"Ctrl+C", "Quit"}}, width, keyStyle, muted)}
	}
	return []string{
		imagePickerShortcutLine(primary, width, keyStyle, muted),
		imagePickerShortcutLine(secondary, width, keyStyle, muted),
	}
}

func imagePickerShortcutLine(shortcuts [][2]string, width int, keyStyle, muted lipgloss.Style) string {
	var parts []string
	used := 0
	for _, shortcut := range shortcuts {
		key := "[" + shortcut[0] + "]"
		cells := ansi.StringWidth(key + " " + shortcut[1])
		gap := 0
		if len(parts) > 0 {
			gap = 2
		}
		if used+gap+cells > width {
			continue
		}
		parts = append(parts, keyStyle.Render(key)+" "+muted.Render(shortcut[1]))
		used += gap + cells
	}
	return strings.Join(parts, "  ")
}

func (m *imagePicker) commandPreview(width, height int) ([]string, int, int) {
	lines := m.commandLines(width)
	start := max(0, min(m.commandOffset, len(lines)-height))
	if m.focus != "command" {
		start = 0
		if m.workspace {
			// Keep the changing prompt visible when a long command needs scrolling.
			start = max(0, len(lines)-height)
		}
	}
	return lines[start:min(len(lines), start+height)], start, len(lines)
}

func (m *imagePicker) commandLines(width int) []string {
	command := cliterminal.FormatCommand(m.settings.args(), m.shell)
	if command == "" {
		command = imagePickerUnsupportedShell
	}
	var lines []string
	var line strings.Builder
	used, width := 0, max(1, width)
	breakAt, breakWidth := 0, 0
	for len(command) > 0 {
		sequence, cells, read := cliterminal.Sequence(command)
		command = command[read:]
		for used > 0 && used+cells > width {
			text := line.String()
			if breakAt == 0 {
				breakAt, breakWidth = len(text), used
			}
			// Keep whitespace in the preview so scrolling still exposes every
			// command byte. Only words wider than the viewport need a hard wrap.
			lines = append(lines, text[:breakAt])
			line.Reset()
			line.WriteString(text[breakAt:])
			used -= breakWidth
			breakAt, breakWidth = 0, 0
		}
		line.WriteString(sequence)
		used += cells
		if sequence == " " {
			breakAt, breakWidth = line.Len(), used
		}
	}
	return append(lines, line.String())
}

func (m *imagePicker) commandDimensions() (width, height int) {
	if m.workspace {
		width = max(1, m.width-8)
		height = 1
		if m.viewHeight() >= 12 {
			height = min(2, len(m.commandLines(width)))
		}
		return width, height
	}
	width = max(1, min(m.width-4, 100)-2)
	height = 1
	if m.viewHeight() >= 16 {
		height = 3
	}
	if m.viewHeight() >= 18 {
		height = 4
	}
	return width, height
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
