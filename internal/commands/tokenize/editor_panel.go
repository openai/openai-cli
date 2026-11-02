package tokenize

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
)

func (m *tokenizerEditor) framed() bool { return m.width >= 50 && m.height >= 16 }

func (m *tokenizerEditor) studioLayout() bool { return m.width >= 110 && m.height >= 28 }

func (m *tokenizerEditor) showActions() bool {
	return m.modal == tokenizerModalNone && m.width >= 68 && m.height >= 20
}

// The inline painter reserves the final terminal column and row for wrapping.
func (m *tokenizerEditor) viewHeight() int {
	if m.studioLayout() {
		return min(34, m.height-1)
	}
	if m.showActions() {
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

// Body rows exclude the header, divider, and one footer row.
func (m *tokenizerEditor) bodyHeight() int {
	rows := m.viewHeight() - 3
	if m.framed() {
		rows -= 2
	}
	if m.showActions() {
		rows-- // The action strip precedes the divider.
	}
	if m.studioLayout() && m.modal == tokenizerModalNone {
		rows -= 2 // Space above the action strip and below the divider.
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
	ruleWidth, ruleEnd := width, ""
	if status != "" {
		statusWidth := ansi.StringWidth(status)
		if gap := width - ansi.StringWidth(title) - statusWidth; gap > 0 {
			header += strings.Repeat(" ", gap) + status
		} else {
			// Keep both counts in compact Bytes view without consuming a body row.
			ruleWidth, ruleEnd = max(0, width-statusWidth-1), " "+status
		}
	}
	lines := []string{header}
	if m.showActions() {
		if m.studioLayout() {
			lines = append(lines, "")
		}
		details := "Token details " + s.accent.Render("F4")
		if len(m.tokens) == 0 {
			details = s.muted.Render("Token details F4")
		}
		lines = append(lines, "Choose view "+s.accent.Render("F2")+"    Choose model "+s.accent.Render("F3")+
			"    "+details+"    Help "+s.accent.Render("F1"))
	}
	lines = append(lines, terminal.SectionDivider(ruleWidth, m.color, s.fill, s.edge, s.secondary)+ruleEnd)
	if m.studioLayout() && m.modal == tokenizerModalNone {
		lines = append(lines, "")
	}
	headerRows := len(lines)
	limit := max(0, m.bodyHeight()+1-len(footer))
	lines = append(lines, body[:min(limit, len(body))]...)
	for len(lines) < headerRows+limit {
		lines = append(lines, "")
	}
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
	for _, line := range lines {
		line = tokenizerClip(line, width)
		padding := strings.Repeat(" ", max(0, width-tokenizerSourceWidth(line)))
		// Pin the right edge even when the terminal draws a ZWJ cluster narrower than the painter.
		frame = append(frame, edge+" "+line+padding+" "+ansi.CursorHorizontalAbsolute(width+4)+edge)
	}
	frame = append(frame, s.border.Render("╰"+rule+"╯"))
	return strings.Join(frame, "\n")
}
