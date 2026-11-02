package tokenize

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
)

func (m *tokenizerEditor) framed() bool { return m.width >= 50 && m.height >= 16 }

// Keep the first row for its field label, even on an empty source line.
func trimEmptyRows(rows []string) []string {
	for len(rows) > 1 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

func (m *tokenizerEditor) studioLayout() bool { return m.width >= 96 && m.height >= 16 }

// The inline painter reserves the final terminal column and row for wrapping.
func (m *tokenizerEditor) viewHeight() int {
	if m.studioLayout() {
		return min(34, m.height-1)
	}
	if m.modal == tokenizerModalNone && m.width >= 68 {
		return min(20, m.height-1)
	}
	return min(18, max(0, m.height-1))
}
func (m *tokenizerEditor) viewWidth() int {
	if m.studioLayout() {
		return min(140, m.width-5)
	}
	if m.framed() {
		return max(1, min(100, m.width-5))
	}
	return max(1, min(100, m.width-4))
}

// Reserve the title and footer. Dialogs also separate the title from their rows.
func (m *tokenizerEditor) bodyHeight() int {
	rows := m.viewHeight() - 2
	if m.modal != tokenizerModalNone {
		rows--
	}
	if m.framed() {
		rows -= 2
	}
	return max(0, rows)
}

func (s tokenizerEditorStyles) shortcuts(hints string) string {
	var pairs []string
	for _, hint := range strings.Split(hints, " · ") {
		key, action, _ := strings.Cut(hint, " ")
		pair := s.accent.Render(key)
		if action != "" {
			pair += " " + s.muted.Render(action)
		}
		pairs = append(pairs, pair)
	}
	return strings.Join(pairs, "  ")
}

func (m *tokenizerEditor) panel(s tokenizerEditorStyles, title, status string, body, footer []string) string {
	width := m.viewWidth()
	header := title
	if m.modal == tokenizerModalNone {
		prefix, view := "", s.accent.Render("v")+" View  "
		if m.focus == tokenizerFocusText {
			prefix = s.accent.Render("Tab") + s.muted.Render(" then ")
			view = "" // The View row already shows the direct arrow shortcut.
		}
		help := s.accent.Render("?") + " Help"
		actions := prefix + view + s.accent.Render("m") + " Model  "
		if len(m.tokens) > 0 {
			actions += s.accent.Render("d") + " Details  "
		}
		actions += help
		available := width - ansi.StringWidth(title)
		if status != "" {
			available -= ansi.StringWidth(status) + 2
		}
		if ansi.StringWidth(actions)+2 > available {
			actions = prefix + help
		}
		gap := available - ansi.StringWidth(actions)
		if gap < 2 {
			// Keep Help on the header and put the count on the next row.
			gap = width - ansi.StringWidth(title+actions)
		}
		header += strings.Repeat(" ", gap) + actions
	}
	overflow := ""
	if status != "" {
		statusWidth := ansi.StringWidth(status)
		if gap := width - ansi.StringWidth(header) - statusWidth; gap > 0 {
			header += strings.Repeat(" ", gap) + status
		} else {
			overflow = status
		}
	}
	lines := []string{header}
	limit := m.bodyHeight() + 1 - len(footer)
	showRule := m.modal != tokenizerModalNone
	if showRule {
		suffix := ""
		if overflow != "" {
			suffix = " " + overflow + " " + s.border.Render("─")
		}
		ruleWidth := width - ansi.StringWidth(suffix)
		lines = append(lines, terminal.SectionDivider(ruleWidth, m.color, s.fill, s.edge, s.secondary)+suffix)
	} else if overflow != "" {
		lines = append(lines, strings.Repeat(" ", max(0, width-ansi.StringWidth(overflow)))+overflow)
		limit--
	}
	lines = append(lines, body[:min(max(0, limit), len(body))]...)
	lines = append(lines, footer...)
	if !m.framed() {
		for i, line := range lines {
			lines[i] = "  " + tokenizerClip(line, width)
		}
		return strings.Join(lines, "\n")
	}
	edge := s.border.Render("│")
	rule := strings.Repeat("─", width+2)
	frame := []string{s.border.Render("╭" + rule + "╮")}
	for i, line := range lines {
		if i == 1 && showRule {
			frame = append(frame, s.border.Render("├─")+line+s.border.Render("─┤"))
			continue
		}
		line = tokenizerClip(line, width)
		padding := strings.Repeat(" ", max(0, width-tokenizerSourceWidth(line)))
		// Pin the right edge even when the terminal draws a ZWJ cluster narrower than the painter.
		frame = append(frame, edge+" "+line+padding+" "+ansi.CursorHorizontalAbsolute(width+4)+edge)
	}
	frame = append(frame, s.border.Render("╰"+rule+"╯"))
	return strings.Join(frame, "\n")
}
