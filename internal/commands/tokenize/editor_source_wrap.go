package tokenize

import (
	"iter"
	"strings"
)

type studioSourceCell struct {
	index, row, column int
	display            string
}

type studioSourcePage struct {
	start, cursor, width, count int
	revision                    uint64
}

// Anchor at a fixed grapheme boundary near the caret, even in a very long paragraph.
func studioSourceStart(cursor, width, count int) int {
	return max(0, cursor/width-count-1) * width
}

func (m *tokenizerEditor) studioFirst(cursor, width, count int) int {
	p := m.sourcePage
	if p.cursor == m.cursor && p.width == width && p.count == count && p.revision == m.revision {
		return p.start
	}
	return studioSourceStart(cursor, width, count)
}

func (m *tokenizerEditor) wrappedSource(first, cursor, width int) iter.Seq[studioSourceCell] {
	return func(yield func(studioSourceCell) bool) {
		row, column := 0, 0
		for i := first; i < len(m.boundaries); i++ {
			display, hardBreak := "", false
			last := i+1 == len(m.boundaries)
			if !last {
				raw := m.text[m.boundaries[i]:m.boundaries[i+1]]
				raw, hardBreak = strings.CutSuffix(raw, "\n")
				display = raw
				if raw != "" && (len(raw) != 1 || raw[0] < ' ' || raw[0] > '~') {
					display = tokenizerSourceDisplay(raw)
				}
			}
			if display == "" && i == cursor && m.focus == tokenizerFocusText {
				display = " "
			}
			cells := tokenizerSourceWidth(display)
			if last {
				// EOF has a caret position even before navigation reaches it.
				cells = 1
			}
			if cells > width {
				display, cells = "…", 1
			}
			if column+cells > width {
				row++
				column = 0
			}
			if !yield(studioSourceCell{i, row, column, display}) {
				return
			}
			column += cells
			if hardBreak {
				row++
				column = 0
			}
		}
	}
}

func (m *tokenizerEditor) studioSourceLines(s tokenizerEditorStyles, width, count int) []string {
	if m.text == "" {
		return m.editorLines(s, width, count)
	}
	cursor := m.cursorIndex()
	first := m.studioFirst(cursor, width, count)
	var rows []string
	var line strings.Builder
	caretRow := -1
	for cell := range m.wrappedSource(first, cursor, width) {
		if cell.row != len(rows) {
			rows = append(rows, line.String())
			line.Reset()
		}
		if caretRow >= 0 && cell.row >= max(count, caretRow+1) {
			break
		}
		if cell.index == cursor {
			caretRow = cell.row
			if m.focus == tokenizerFocusText {
				cell.display = tokenizerCaret(cell.display)
			}
		}
		line.WriteString(cell.display)
	}
	rows = append(rows, line.String())
	visible := make([]string, count)
	copy(visible, rows[max(0, caretRow-count+1):])
	return visible
}

// Page through the same visual rows that the source pane displays.
func (m *tokenizerEditor) moveSourcePage(direction int) {
	count := m.sourceRows()
	if !m.studioLayout() {
		m.moveLine(direction * count)
		return
	}
	_, indent := m.sourceLabel()
	width, cursor := m.sourceWidth()-indent, m.cursorIndex()
	first := m.studioFirst(cursor, width, count)
	var starts []int
	row, column := 0, 0
	for cell := range m.wrappedSource(first, cursor, width) {
		if cell.row == len(starts) {
			starts = append(starts, cell.index)
		}
		if cell.index == cursor {
			row, column = cell.row, cell.column
			break
		}
	}
	topRow := max(0, row-count+1)
	top := starts[topRow]
	row -= topRow
	first, topRow = top, 0
	if direction < 0 {
		// Reconstruct the preceding page from a bounded prefix. A partial
		// row may overlap the old top, but no grapheme can fall between pages.
		first = studioSourceStart(top, width, count)
		for cell := range m.wrappedSource(first, cursor, width) {
			if cell.index == top {
				topRow = cell.row
				if cell.column > 0 {
					topRow++
				}
				break
			}
		}
	}
	newTopRow := max(0, topRow+direction*count)
	targetRow := max(0, topRow+row+direction*count)
	next := cursor
	for cell := range m.wrappedSource(first, cursor, width) {
		if cell.row > targetRow {
			break
		}
		if cell.row == newTopRow && cell.column == 0 {
			top = cell.index
		}
		if cell.column <= column {
			next = cell.index
		}
	}
	m.cursor = m.boundaries[next]
	m.sourcePage = studioSourcePage{top, m.cursor, width, count, m.revision}
}

func (m *tokenizerEditor) sourceOverflows() bool {
	count := m.sourceRows()
	if len(m.lineStarts) > count {
		return true
	}
	if !m.studioLayout() {
		return false
	}
	_, indent := m.sourceLabel()
	for cell := range m.wrappedSource(0, m.cursorIndex(), m.sourceWidth()-indent) {
		if cell.row >= count {
			return true
		}
	}
	return false
}
