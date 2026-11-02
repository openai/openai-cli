package tokenize

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const studioGutter = 3 // A divider cell with a space on each side.

func (m *tokenizerEditor) sourceWidth() int {
	return (m.viewWidth() - studioGutter) * 45 / 100
}

func (m *tokenizerEditor) resultWidth() int {
	if m.studioLayout() {
		return m.viewWidth() - m.sourceWidth() - studioGutter
	}
	return m.viewWidth()
}

func (m *tokenizerEditor) studioView(s tokenizerEditorStyles) string {
	leftWidth, rightWidth := m.sourceWidth(), m.resultWidth()
	left := m.textRows(s, leftWidth)
	left = append(left, "", "  "+s.muted.Bold(m.color).Render("Settings"))
	left = append(left, m.settingsRows(s, leftWidth)...)
	if m.note != "" {
		left = append(left, "")
		left = append(left, m.noteRows(s, leftWidth)...)
	}
	right := m.studioResults(s, rightWidth)
	rows := make([]string, m.bodyHeight())
	for i := range rows {
		line := ""
		if i < len(left) {
			line = left[i]
		}
		// Pin the seam when a terminal draws a visible emoji narrower than its measured cells.
		rows[i] = line + strings.Repeat(" ", max(0, leftWidth-tokenizerSourceWidth(line))+1) +
			ansi.CursorHorizontalAbsolute(leftWidth+4) + s.border.Render("│") + " "
		if i < len(right) {
			rows[i] += right[i]
		}
	}
	return m.panel(s, "Tokenizer", s.muted.Render("Token studio"), rows, []string{s.shortcuts(m.footer())})
}

func (m *tokenizerEditor) studioResults(s tokenizerEditorStyles, width int) []string {
	state := s.muted.Render("○ Empty")
	switch {
	case m.updating:
		state = s.accent.Render("● Updating")
	case m.failed:
		state = s.failure.Render("● Failed")
	case len(m.tokens) > 0:
		state = s.ready.Render("● Ready")
	}
	title := s.muted.Bold(m.color).Render("Tokens")
	if m.focus == tokenizerFocusResults {
		title = s.accent.Bold(m.color).Render("Tokens")
	}
	// Keep the status light in the same cell for every state.
	rows := []string{"  " + title + strings.Repeat(" ", width-2-ansi.StringWidth(title)-10) + state,
		"  " + m.countStatus(s), ""}
	rows = append(rows, m.resultLines(s, width)...)
	rows = append(rows, "")
	rows = append(rows, m.tokenSizeChart(s, width)...)
	rows = append(rows, "")
	if len(m.tokens) == 0 {
		return append(rows, s.heading("Selected token", m.focus == tokenizerFocusResults))
	}
	piece, start, end := m.fragment(m.selected)
	position := fmt.Sprintf("Token %d of %d", m.selected+1, len(m.tokens))
	if !utf8.ValidString(piece) {
		position += " · partial UTF-8"
	}
	rows = append(rows, s.heading(position, m.focus == tokenizerFocusResults))
	value := tokenizerFragmentLabel(piece, width-17)
	if utf8.ValidString(piece) {
		value = "“" + value + "”"
	}
	return append(rows, "  "+s.muted.Render("Text         ")+value,
		"  "+s.muted.Render("Token ID     ")+strconv.FormatUint(uint64(m.tokens[m.selected].ID), 10),
		"  "+s.muted.Render("Byte offset  ")+strconv.Itoa(start)+"    "+s.muted.Render("Byte length  ")+strconv.Itoa(end-start))
}

// Each bar is one token's exact byte length, scaled to the largest token on this page.
// The selected token and chart pages stay aligned in every display mode.
func (m *tokenizerEditor) tokenSizeChart(s tokenizerEditorStyles, width int) []string {
	const height = 3
	width -= 4 // Match the body gutter on both sides.
	title := s.muted.Render("Bytes per token")
	if len(m.tokens) == 0 {
		return []string{"  " + title, "", "", "", "  " + s.border.Render(strings.Repeat("─", width)), ""}
	}
	capacity := (width + 1) / 2
	start := m.selected / capacity * capacity
	end := min(start+capacity, len(m.tokens))
	lengths := make([]int, end-start)
	peak := 0
	for i := range lengths {
		_, begin, limit := m.fragment(start + i)
		lengths[i] = limit - begin
		peak = max(peak, lengths[i])
	}
	maxLabel := fmt.Sprintf("%d B max", peak)
	rows := []string{"  " + title + strings.Repeat(" ", width-ansi.StringWidth(title)-len(maxLabel)) + s.muted.Render(maxLabel)}
	levels := []rune(" ▁▂▃▄▅▆▇█")
	plot := make([]strings.Builder, height)
	previous, marker := -1, 0
	for i, length := range lengths {
		x := i * (width - 1) / max(1, len(lengths)-1)
		style := s.muted
		if start+i == m.selected {
			style, marker = s.accent, x
		}
		units := max(1, (length*height*8+peak/2)/peak)
		for row := range plot {
			plot[row].WriteString(strings.Repeat(" ", x-previous-1))
			level := min(8, max(0, units-(height-row-1)*8))
			plot[row].WriteString(style.Render(string(levels[level])))
		}
		previous = x
	}
	for i := range plot {
		rows = append(rows, "  "+plot[i].String())
	}
	rail := s.accent.Render(strings.Repeat("━", marker)+"●") + s.border.Render(strings.Repeat("─", width-marker-1))
	first, last := strconv.Itoa(start+1), strconv.Itoa(end)
	axis := first
	if start+1 < end {
		axis += strings.Repeat(" ", width-len(first)-len(last)) + last
	}
	return append(rows, "  "+rail, "  "+s.muted.Render(axis))
}
