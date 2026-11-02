package images

import (
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

const studioDraftRows = 3

// Keep source indexes alongside visual rows. Wrapping never edits the submitted prompt.
type studioDraftLine struct{ start, end int }

func studioDraftCluster(s string) (bytes, runes, cells int) {
	if s[0] >= ' ' && s[0] <= '~' && (len(s) == 1 || s[1] < utf8.RuneSelf) {
		return 1, 1, 1
	}
	glyph, width := ansi.FirstGraphemeCluster(s, ansi.WcWidth)
	visible := glyph
	if strings.IndexFunc(glyph, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		visible = imagePickerLine(glyph)
	}
	return len(glyph), utf8.RuneCountInString(glyph), max(width, ansi.StringWidth(visible))
}

func (m *imagePicker) studioDraft(width int) (lines []studioDraftLine, row, col int) {
	start, pos, used := 0, 0, 0
	for line := range strings.SplitSeq(string(m.draft), "\n") {
		for len(line) != 0 {
			n, runes, cells := studioDraftCluster(line)
			if used > 0 && used+cells > width {
				lines = append(lines, studioDraftLine{start, pos})
				start, used = pos, 0
			}
			if pos <= m.cursor && m.cursor < pos+runes {
				row, col = len(lines), used
			}
			pos, used, line = pos+runes, used+cells, line[n:]
		}
		if m.cursor == pos {
			row, col = len(lines), used
		}
		lines = append(lines, studioDraftLine{start, pos})
		pos++
		start, used = pos, 0
	}
	return lines, row, col
}

func (m *imagePicker) studioMoveRow(g imageStudioGeometry, delta int) {
	lines, row, col := m.studioDraft(g.editorWidth - 3)
	to := row + delta
	if to < 0 {
		m.focus, m.studioAction = "actions", 0
		return
	}
	if to >= len(lines) {
		m.focus, m.selected = "options", 0
		return
	}
	line := lines[to]
	m.cursor = line.start
	for s, used := string(m.draft[line.start:line.end]), 0; len(s) > 0; {
		n, r, cells := studioDraftCluster(s)
		if used+cells > col || (m.cursor+r == line.end && to+1 < len(lines) && lines[to+1].start == line.end) {
			break
		}
		s, used, m.cursor = s[n:], used+cells, m.cursor+r
	}
}

func (m *imagePicker) studioHeading(label string, active bool) string {
	if !active {
		return "  " + lipgloss.NewStyle().Bold(m.color).Render(label)
	}
	return m.workspaceText("›", imageActive) + " " + m.studioSelection().Bold(m.color).Render(label)
}

func (m *imagePicker) studioSelection() lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.color && m.profile >= colorprofile.ANSI {
		p := m.palette()
		return s.Foreground(m.profile.Convert(lipgloss.Color(p.selected))).Background(m.profile.Convert(lipgloss.Color(p.fill)))
	}
	return s
}

// Return only the editor column. The workspace positions it alongside the image viewport.
func (m *imagePicker) studioForm(g imageStudioGeometry) tea.View {
	var view tea.View
	width := g.editorWidth
	var rows []string
	if m.focus == "command" {
		rows = append(rows, m.studioHeading("Command", !m.generating))
		w, h := m.commandDimensions()
		lines, _, _ := m.commandPreview(w, h)
		for _, line := range lines {
			rows = append(rows, "  "+line)
		}
	} else {
		active := m.focus == "prompt" && !m.generating
		rows = append(rows, m.studioHeading("Prompt", active))
		draft, row, col := m.studioDraft(width - 3) // Leave a real cell for the caret at line end.
		visible := min(studioDraftRows, len(draft))
		first := max(0, min(row-1, len(draft)-visible))
		for i, bounds := range draft[first : first+visible] {
			line := imagePickerLine(string(m.draft[bounds.start:bounds.end]))
			if i == 0 && len(m.draft) == 0 {
				line = m.workspaceText("Describe your image…", imageSecondary)
			}
			rows = append(rows, "  "+line)
		}
		if active {
			view.Cursor = tea.NewCursor(4+2+col, imageStudioBodyY+1+row-first)
			view.Cursor.Shape = tea.CursorBar
		}
		rows = append(rows, m.studioHeading(m.heading(), m.focus == "path" && !m.generating))
		if m.page == "path" {
			path, x := imagePickerEditLine(m.folder.draft, m.folder.cursor, width-2)
			if len(m.folder.draft) == 0 {
				path = m.workspaceText("Enter a folder path…", imageSecondary)
			}
			if m.focus == "path" && !m.generating {
				view.Cursor = tea.NewCursor(4+2+x, imageStudioBodyY+len(rows))
				view.Cursor.Shape = tea.CursorBar
			}
			rows = append(rows, "  "+cliterminal.FitPrompt(path, width-2))
		}
		options := m.rows()
		begin, end := m.optionWindow(len(options), max(1, g.editorRows-len(rows)))
		if m.settings.outputDir == "" {
			for i := begin; i < end; i++ {
				if options[i].id == "folder" {
					options[i].value = strings.TrimSuffix(options[i].value, " (Default)")
				}
			}
		}
		rows = append(rows, m.optionLines(options, begin, end, width, m.studioSelection(), m.foreground(m.palette().accent), m.foreground(m.palette().secondary))...)
	}
	view.Content = strings.Join(rows, "\n")
	return view
}

func (m *imagePicker) studioActions(width int) string {
	if m.focus == "path" {
		// The folder editor owns the keyboard until the user accepts or cancels.
		return ""
	}
	generate, command := "Generate", "View command"
	disabled := m.generating || m.folder.busy == "submit" || m.folder.busy == "select"
	label := func(text string, index int) string {
		if disabled {
			return "  " + m.workspaceText(text, imageSecondary)
		}
		if m.focus == "actions" && m.studioAction == index {
			return m.studioHeading(text, true)
		}
		return "  " + text
	}
	line := label(generate, 0) + "  " + m.workspaceText("Ctrl+G", imageActive) + "  " + label(command, 1)
	if disabled {
		line = label(generate, 0) + strings.Repeat(" ", len("  Ctrl+G  ")) + label(command, 1)
	}
	return ansi.Truncate(line, width, "…")
}

func (m *imagePicker) studioHints(g imageStudioGeometry) string {
	keys := [][2]string{{"Ctrl+C", "Exit"}}
	switch {
	case m.generating:
		keys = [][2]string{{"Ctrl+C", "Cancel"}}
		if m.hasImages {
			if m.focus == "images" {
				keys = append(keys, [2]string{"Esc", "Prompt"}, [2]string{"←→", "Images"}, [2]string{"↑↓", "Scroll"}, [2]string{"O", "Open"})
			} else {
				keys = append(keys, [2]string{"Tab", "Images"})
			}
		}
	case m.folder.busy == "submit" || m.folder.busy == "select":
		keys = append(keys, [2]string{"Esc", "Cancel"})
	case m.focus == "prompt":
		keys = append(keys, [2]string{"↑", "Actions"}, [2]string{"Tab", "Settings"})
		if m.hasImages {
			keys = append(keys, [2]string{"Shift+Tab", "Images"})
		}
		keys = append(keys, [2]string{"Alt+Enter", "New line"})
	case m.focus == "actions":
		keys = append(keys, [2]string{"←→", "Actions"}, [2]string{"Enter", "Select"}, [2]string{"↓", "Prompt"})
	case m.focus == "images":
		keys = append(keys, [2]string{"Esc", "Prompt"}, [2]string{"←→", "Images"}, [2]string{"↑↓", "Scroll"}, [2]string{"Enter", "Expand"}, [2]string{"O", "Open"})
	case m.focus == "path":
		keys = append(keys, [2]string{"Tab", "Complete"}, [2]string{"Esc", "Back"})
	case m.focus == "command":
		keys = append(keys, [2]string{"Esc", "Prompt"})
		if len(m.commandLines(g.editorWidth-2)) > g.editorRows-1 {
			keys = append(keys, [2]string{"↑↓", "Scroll"})
		}
	default:
		keys = append(keys, [2]string{"↑↓", "Choose"}, [2]string{"Enter", "Select"}, [2]string{"Esc", "Prompt"})
	}
	return m.workspaceKeys(keys, g.frameWidth-8)
}

func (m *imagePicker) studioKey(key string, g imageStudioGeometry) (bool, tea.Cmd) {
	switch m.focus {
	case "command":
		// A repeated Enter after opening the inspector must never submit a request.
		return key == "enter", nil
	case "prompt":
		switch key {
		case "up":
			m.studioMoveRow(g, -1)
		case "down":
			m.studioMoveRow(g, 1)
		default:
			return false, nil
		}
	case "actions":
		switch key {
		case "left", "right":
			m.studioAction = 1 - m.studioAction
		case "down", "shift+tab":
			m.focus = "prompt"
		case "tab":
			m.focus, m.selected = "options", 0
		case "enter":
			if m.studioAction == 1 {
				m.focus, m.commandOffset = "command", 0
			} else {
				m.focus = "prompt"
				return true, m.activate(imagePickerRow{id: "generate"})
			}
		case "ctrl+g", "ctrl+p":
			return false, nil
		}
	default:
		return false, nil
	}
	return true, nil
}
