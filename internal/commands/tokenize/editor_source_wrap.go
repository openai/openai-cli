package tokenize

import (
	"iter"

	tea "charm.land/bubbletea/v2"
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
	if p.cursor == m.boundaries[cursor] && p.width == width && p.count == count && p.revision == m.revision {
		return p.start
	}
	return studioSourceStart(cursor, width, count)
}

func (m *tokenizerEditor) wrappedSource(first int, span tokenizerSourceRange, width int) iter.Seq[studioSourceCell] {
	return func(yield func(studioSourceCell) bool) {
		row, column := 0, 0
		for i := first; i < len(m.boundaries); i++ {
			display, hardBreak := "", false
			last := i+1 == len(m.boundaries)
			if !last {
				raw := m.text[m.boundaries[i]:m.boundaries[i+1]]
				display, hardBreak = tokenizerSourceCell(raw, span.contains(i))
			}
			if display == "" && i == span.first && m.focus == tokenizerFocusText {
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

func (m *tokenizerEditor) studioSourceLines(s tokenizerEditorStyles, width, count int) ([]string, *tea.Cursor) {
	if m.text == "" {
		return m.editorLines(s, width, count)
	}
	span := m.sourceRange()
	first := m.studioFirst(span.first, width, count)
	var rows []string
	var line tokenizerSourceRow
	firstRow, lastRow := -1, -1
	var caret *tea.Cursor
	for cell := range m.wrappedSource(first, span, width) {
		if cell.row != len(rows) {
			rows = append(rows, line.render(s.sourceSelected))
			line = tokenizerSourceRow{}
		}
		selected := span.contains(cell.index)
		if cell.index == span.first {
			firstRow, lastRow = cell.row, cell.row
			if m.focus == tokenizerFocusText {
				caret = tea.NewCursor(cell.column, cell.row)
			}
		}
		if selected {
			// Keep the start visible even when one token spans more than a page.
			lastRow = min(cell.row, firstRow+count-1)
		}
		if firstRow >= 0 && cell.row >= max(count, lastRow+1) {
			break
		}
		line.appendCell(cell.display, selected)
	}
	rows = append(rows, line.render(s.sourceSelected))
	visible := make([]string, count)
	start := max(0, lastRow-count+1)
	copy(visible, rows[start:])
	if caret != nil {
		caret.Y -= start
	}
	return visible, caret
}

// Page through the same visual rows that the source pane displays.
func (m *tokenizerEditor) moveSourcePage(direction int) {
	count := m.sourceRows()
	if !m.studioLayout() {
		m.moveLine(direction * count)
		return
	}
	_, indent := m.sourceLabel()
	span := m.sourceRange()
	width, cursor := m.sourceWidth()-indent, span.first
	first := m.studioFirst(cursor, width, count)
	var starts []int
	row, column := 0, 0
	for cell := range m.wrappedSource(first, span, width) {
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
		for cell := range m.wrappedSource(first, span, width) {
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
	for cell := range m.wrappedSource(first, span, width) {
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
	for cell := range m.wrappedSource(0, m.sourceRange(), m.sourceWidth()-indent) {
		if cell.row >= count {
			return true
		}
	}
	return false
}
