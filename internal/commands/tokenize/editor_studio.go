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
	left = append(left, m.settingsRows(s, leftWidth)...)
	left = append(left, m.noteRows(s, leftWidth)...)
	right := m.studioResults(s, rightWidth)
	rows := make([]string, min(m.bodyHeight(), max(len(left), len(right))))
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
	return m.panel(s, "Tokenizer", "", rows, []string{s.shortcuts(m.footer())})
}

func (m *tokenizerEditor) studioResults(s tokenizerEditorStyles, width int) []string {
	count := m.countStatus(s)
	if count == "" {
		count = s.muted.Render("Tokens")
	}
	header := "  " + count
	if m.focus == tokenizerFocusResults && len(m.tokens) == 0 {
		header = s.heading(ansi.Strip(count), true)
	}
	rows := []string{header}
	if len(m.tokens) == 0 {
		return rows
	}
	startToken := m.resultStart(width)
	strip, endToken := m.tokenStrip(s, width, startToken)
	rows = append(rows, trimEmptyRows(strip)...)
	rows = append(rows, m.tokenByteCounts(s, width, startToken, endToken))

	piece, start, end := m.fragment(m.selected)
	position := fmt.Sprintf("Token %d of %d", m.selected+1, len(m.tokens))
	valid := utf8.ValidString(piece)
	if !valid {
		position += " (partial UTF-8)"
	}
	heading := s.heading(position, m.focus == tokenizerFocusResults)
	available := width - ansi.StringWidth(heading) - 2
	spaces := tokenizerSpaceLabel(piece)
	// Very long token numbers can leave too little space for a meaningful fragment.
	if available < max(8, len(spaces)) {
		rows = append(rows, heading)
		heading, available = "", width-2
	}
	var value string
	if spaces != "" {
		value = s.tokenSelected.Render(spaces)
	} else {
		value = tokenizerFragmentLabel(piece, available-2)
		if valid {
			value = "“" + value + "”"
		}
	}
	rows = append(rows, heading+"  "+value)
	switch m.tab {
	case 1:
		id := strconv.FormatUint(uint64(m.tokens[m.selected].ID), 10)
		rows = append(rows, "  "+s.muted.Render("This encoding uses ID ")+id)
	case 2:
		location := "  " + s.muted.Render("Starts after ") + tokenizerByteSize(start)
		size := s.muted.Render("Uses ") + tokenizerByteSize(end-start)
		if ansi.StringWidth(location+"  "+size) > width {
			return append(rows, location, "  "+size)
		}
		rows = append(rows, location+"  "+size)
	}
	return rows
}

// Show exact lengths for a contiguous part of the visible token strip.
func (m *tokenizerEditor) tokenByteCounts(s tokenizerEditorStyles, width, first, end int) string {
	const label = "Bytes per token"
	values := make([]string, end-first)
	cells := 0
	for i := range values {
		_, startByte, endByte := m.fragment(first + i)
		values[i] = strconv.Itoa(endByte - startByte)
		cells += len(values[i]) + 2 // Each count has one space on either side.
	}

	// Keep the inspected token in view. Trim the more distant edge when the row is full.
	available := width - len(label) - 3 // Two inset cells and a space after the label.
	for len(values) > 1 {
		needed := cells
		if first > 0 {
			needed += 2 // Leading ellipsis and space.
		}
		if end < len(m.tokens) {
			needed += 2 // Trailing space and ellipsis.
		}
		if needed <= available {
			break
		}
		if m.selected-first >= end-1-m.selected {
			cells -= len(values[0]) + 2
			values = values[1:]
			first++
		} else {
			cells -= len(values[len(values)-1]) + 2
			values = values[:len(values)-1]
			end--
		}
	}

	var row strings.Builder
	row.WriteString("  " + s.muted.Render(label) + " ")
	if first > 0 {
		row.WriteString(s.muted.Render("… "))
	}
	for i, value := range values {
		index := first + i
		style := s.byteCounts[index%len(s.byteCounts)]
		if index == m.selected {
			style = s.tokenSelected
		}
		row.WriteString(" " + style.Render(value) + " ")
	}
	if end < len(m.tokens) {
		row.WriteString(s.muted.Render(" …"))
	}
	return row.String()
}
