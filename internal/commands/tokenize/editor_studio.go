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
	state := s.muted.Render("○ Empty")
	count := m.countStatus(s)
	switch {
	case m.updating:
		state = s.accent.Render("● Updating")
		count = s.muted.Render("Tokens")
		if m.tab == 2 {
			unit := "bytes"
			if len(m.text) == 1 {
				unit = "byte"
			}
			count = s.muted.Render(fmt.Sprintf("%d %s", len(m.text), unit))
		}
	case m.failed:
		state = s.failure.Render("● Failed")
	case len(m.tokens) > 0:
		state = s.ready.Render("● Ready")
	}
	header := "  " + count
	if m.focus == tokenizerFocusResults && len(m.tokens) == 0 {
		header = s.heading(ansi.Strip(count), true)
	}
	// Reserve the longest status label so the light stays in the same column.
	rows := []string{header + strings.Repeat(" ", max(1, width-10-ansi.StringWidth(header))) + state}
	if len(m.tokens) == 0 {
		return rows
	}
	rows = append(rows, trimEmptyRows(m.resultLines(s, width))...)
	rows = append(rows, m.tokenSizeChart(s, width)...)

	piece, start, end := m.fragment(m.selected)
	position := fmt.Sprintf("Token %d of %d", m.selected+1, len(m.tokens))
	valid := utf8.ValidString(piece)
	if !valid {
		position += " · partial UTF-8"
	}
	heading := s.heading(position, m.focus == tokenizerFocusResults)
	available := width - ansi.StringWidth(heading) - 2
	// Very long token numbers can leave too little space for a meaningful fragment.
	if available < 8 {
		rows = append(rows, heading)
		heading, available = "", width-2
	}
	value := tokenizerFragmentLabel(piece, available-2)
	if valid {
		value = "“" + value + "”"
	}
	rows = append(rows, heading+"  "+value)
	id := s.muted.Render("ID ") + strconv.FormatUint(uint64(m.tokens[m.selected].ID), 10)
	offset := s.muted.Render("Byte offset ") + strconv.Itoa(start)
	length := s.muted.Render("Length ") + strconv.Itoa(end-start) + " B"
	meta := "  " + id + "  " + offset
	if ansi.StringWidth(meta+"  "+length) > width {
		return append(rows, meta, "  "+length)
	}
	return append(rows, meta+"  "+length)
}

// Round lengths to eighths of the page maximum, with one level for any positive length.
func (m *tokenizerEditor) tokenSizeChart(s tokenizerEditorStyles, width int) []string {
	if len(m.tokens) == 0 {
		return nil
	}
	// Each bar has one cell and a neighboring gap. Reserve the final gap for a bracket.
	capacity := max(1, (width-3)/2)
	start := m.selected / capacity * capacity
	end := min(start+capacity, len(m.tokens))
	lengths := make([]int, end-start)
	peak, digitWidth := 0, 0
	for i := range lengths {
		_, begin, limit := m.fragment(start + i)
		lengths[i] = limit - begin
		peak = max(peak, lengths[i])
		digitWidth += len(strconv.Itoa(lengths[i]))
	}

	const label = "Bytes per token"
	scale := fmt.Sprintf("%d B max", peak)
	if len(lengths) < len(m.tokens) {
		span := strconv.Itoa(start + 1)
		if end > start+1 {
			span += "–" + strconv.Itoa(end)
		}
		scale = span + " · " + scale
	}
	plotWidth := 2*len(lengths) + 1
	available := width - 2 - len(label) - 2 - ansi.StringWidth(scale)
	inline := plotWidth <= available
	if !inline {
		available = width - 2
	}
	showValues := plotWidth+digitWidth <= available

	selected := m.selected - start
	gaps := []byte(strings.Repeat(" ", len(lengths)+1))
	if !m.color {
		gaps[selected], gaps[selected+1] = '[', ']'
	}
	var bars strings.Builder
	levels := []rune(" ▁▂▃▄▅▆▇█")
	for i, length := range lengths {
		level := max(1, (length*8+peak/2)/peak)
		bar := string(levels[level])
		if showValues {
			bar += strconv.Itoa(length)
		}
		style := s.muted
		if i == selected {
			style = s.accent
		}
		bars.WriteByte(gaps[i])
		bars.WriteString(style.Render(bar))
	}
	bars.WriteByte(gaps[len(lengths)])
	if inline {
		return []string{"  " + s.muted.Render(label) + " " + bars.String() + " " + s.muted.Render(scale)}
	}
	return []string{"  " + s.muted.Render(label+"  "+scale), "  " + bars.String()}
}
