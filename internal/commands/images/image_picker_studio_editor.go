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
	lines, row, col := m.studioDraft(g.draftWidth())
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
		return "  " + m.workspaceText(label, imageSecondary)
	}
	return m.workspaceText("› "+label, imageActive)
}

func (m *imagePicker) studioSection(label string, active bool, width int) string {
	prefix := "  "
	if active {
		prefix = "› "
	}
	heading := m.workspaceText(prefix+label, imageActive)
	palette := m.palette()
	rule := cliterminal.SectionDivider(max(0, width-ansi.StringWidth(heading)-2),
		m.color && m.profile == colorprofile.TrueColor, palette.fill, palette.border, palette.secondary)
	if m.profile != colorprofile.TrueColor {
		rule = m.workspaceImageBorder(rule)
	}
	return heading + " " + rule
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
	if m.focus == "command" || m.focus == "images" && m.studioEditorFocus == "command" {
		rows = append(rows, m.studioHeading("Command", m.focus == "command" && !m.generating))
		w, h := m.commandDimensions()
		lines, _, _ := m.commandPreview(w, h)
		for _, line := range lines {
			rows = append(rows, "  "+line)
		}
	} else {
		active := m.focus == "prompt" && !m.generating
		rows = append(rows, m.studioHeading("Prompt", active))
		draft, row, col := m.studioDraft(g.draftWidth()) // Leave a real cell for the caret at line end.
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
			view.Cursor = tea.NewCursor(g.editorX+2+col, g.bodyY+1+row-first)
			view.Cursor.Shape = tea.CursorBar
		}
		for i := range rows {
			rows[i] = m.studioSurfaceRow(rows[i], width)
		}
		rows = append(rows, "")
		rows = append(rows, m.studioSection(m.heading(), m.focus == "path" && !m.generating, width))
		if m.page == "path" {
			path, x := imagePickerEditLine(m.folder.draft, m.folder.cursor, width-2)
			if len(m.folder.draft) == 0 {
				path = m.workspaceText("Enter a folder path…", imageSecondary)
			}
			if m.focus == "path" && !m.generating {
				view.Cursor = tea.NewCursor(g.editorX+2+x, g.bodyY+len(rows))
				view.Cursor.Shape = tea.CursorBar
			}
			rows = append(rows, "  "+cliterminal.FitPrompt(path, width-2))
		}
		options := m.rows()
		available := g.editorRows - len(rows)
		if m.page == "settings" {
			available -= 2 // Output heading and separation stay outside the selectable rows.
		}
		begin, end := m.optionWindow(len(options), max(1, available))
		for i := begin; i < end; i++ {
			switch options[i].id {
			case "size":
				options[i].label = "Size"
				options[i].value = strings.ReplaceAll(strings.Join(strings.Fields(options[i].value), " "), " x ", "×")
			case "folder":
				if m.settings.outputDir == "" {
					options[i].value = strings.TrimSuffix(options[i].value, " (Default)")
				}
				// Keep the destination visible without changing the stored path.
				value := imagePickerLine(options[i].value)
				space := width - imagePickerOptionLabelWidth - 3
				if cells := ansi.StringWidth(value); cells > space {
					tail := ansi.TruncateLeft(value, cells-2*space/3, "")
					head := ansi.Truncate(value, space-ansi.StringWidth(tail)-1, "")
					if at := strings.LastIndexAny(head, `/\`); at >= 0 {
						head = head[:at+1]
					}
					if at := strings.IndexAny(tail, `/\`); at >= 0 {
						tail = tail[at:]
					}
					value = head + "…" + tail
				}
				options[i].value = value
			}
		}
		lines := m.optionLines(options, begin, end, width, m.studioSelection(), m.foreground(m.palette().accent), m.foreground(m.palette().secondary))
		for i, line := range lines {
			if m.page == "settings" && options[begin+i].id == "folder" {
				rows = append(rows, "", m.studioSection("Output", false, width))
			}
			rows = append(rows, line)
		}
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
			return m.workspaceText("›", imageActive) + " " + m.studioSelection().Bold(m.color).Render(text)
		}
		if index == 1 {
			return "  " + m.workspaceText(text, imageSecondary)
		}
		return "  " + text
	}
	line := label(generate, 0) + "  " + m.workspaceText("Ctrl+G", imageActive) + "  " + label(command, 1)
	if disabled {
		line = label(generate, 0) + strings.Repeat(" ", len("  Ctrl+G  ")) + label(command, 1)
	}
	return ansi.Truncate(line, width, "…")
}

func (m *imagePicker) studioHints(g imageStudioGeometry, multiple, scrollable bool) string {
	var keys [][2]string
	exit := [2]string{"Ctrl+C", "Exit"}
	if m.generating {
		exit[1] = "Cancel"
	}
	switch {
	case m.focus == "images":
		keys = append(keys, [2]string{"Tab", "Pane"})
		if multiple {
			keys = append(keys, [2]string{"←→", "Browse"})
		}
		if scrollable {
			keys = append(keys, [2]string{"↑↓", "Scroll"})
		}
		if m.hasImages {
			// The enlarged viewer advertises opening the file outside the terminal.
			keys = append(keys, [2]string{"Enter", "Zoom"})
		}
	case m.generating:
		keys = append(keys, [2]string{"Tab", "Pane"})
	case m.folder.busy == "submit" || m.folder.busy == "select":
		keys = append(keys, [2]string{"Esc", "Cancel"})
	case m.focus == "prompt":
		keys = append(keys, [2]string{"Tab", "Pane"}, [2]string{"↑↓", "Move"}, [2]string{"Alt+Enter", "Newline"})
	case m.focus == "actions":
		keys = append(keys, [2]string{"Tab", "Pane"}, [2]string{"←→", "Move"}, [2]string{"Enter", "Select"}, [2]string{"Esc", "Back"})
	case m.focus == "path":
		keys = append(keys, [2]string{"Tab", "Complete"}, [2]string{"Esc", "Back"})
	case m.focus == "command":
		keys = append(keys, [2]string{"Tab", "Pane"}, [2]string{"Esc", "Back"})
		if cliterminal.ShellQuoter(m.shell) != nil {
			keys = append(keys, [2]string{"Ctrl+P", "Print"})
		}
		if len(m.commandLines(g.editorWidth-2)) > g.editorRows-1 {
			keys = append(keys, [2]string{"↑↓", "Scroll"})
		}
	default:
		keys = append(keys, [2]string{"Tab", "Pane"}, [2]string{"↑↓", "Move"}, [2]string{"Enter", "Select"}, [2]string{"Esc", "Back"})
	}
	return m.workspaceKeys(append(keys, exit), g.frameWidth-8)
}

func (m *imagePicker) studioKey(key string, g imageStudioGeometry) (bool, tea.Cmd) {
	if (key == "tab" || key == "shift+tab") && !m.galleryModal() {
		m.studioSwitchPane()
		return true, nil
	}
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
		case "down":
			m.focus = "prompt"
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

// Keep the editor's selection, caret and command scroll when crossing panes.
func (m *imagePicker) studioSwitchPane() {
	if m.focus == "images" {
		m.focus = m.studioEditorFocus
		if m.focus == "" {
			m.focus = "prompt"
		}
		return
	}
	m.studioFocusImages()
}

func (m *imagePicker) studioFocusImages() {
	if m.focus != "images" {
		m.studioEditorFocus = m.focus
		m.focus = "images"
	}
}
