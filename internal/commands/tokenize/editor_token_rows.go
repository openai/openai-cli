package tokenize

// A vertical arrow sequence keeps its screen column and the row breaks it has crossed.
type tokenizerRowNavigation struct {
	column, width int
	previous      map[int]int // First token of each scrolled row → first token of the preceding row.
}

func (nav *tokenizerRowNavigation) remember(start, previous int) {
	if nav.previous == nil {
		nav.previous = make(map[int]int)
	}
	nav.previous[start] = previous
}

type tokenizerTokenCell struct {
	index, row, column, width int
}

// Painting, paging, and arrow navigation use these same cell positions and wrap points.
func (m *tokenizerEditor) walkResultSegments(start, width, rows int, visit func(tokenizerTokenCell, string)) int {
	row, used := 0, 2
	breaks := m.rowNavigation.previous
	if width != m.rowNavigation.width {
		breaks = nil
	}
	for index := start; index < len(m.tokens); index++ {
		label := m.tokenSegment(index, width)
		cells := tokenizerSourceWidth(label)
		gap := 0
		if used > 2 && m.tab != 0 {
			gap = 1
		}
		_, rowStart := breaks[index]
		if used > 2 && (rowStart || used+gap+cells > width) {
			row++
			used, gap = 2, 0
		}
		if row >= rows {
			return index
		}
		if visit != nil {
			visit(tokenizerTokenCell{index, row, used + gap, cells}, label)
		}
		used += gap + cells
	}
	return len(m.tokens)
}

func (m *tokenizerEditor) tokenCells(start, width, rows int) []tokenizerTokenCell {
	var cells []tokenizerTokenCell
	m.walkResultSegments(start, width, rows, func(cell tokenizerTokenCell, _ string) {
		cells = append(cells, cell)
	})
	return cells
}

// Use the same wrapped rows as the token strip. Keep the preferred column when a short row clamps the selection.
func (m *tokenizerEditor) moveTokenRow(down bool) {
	if len(m.tokens) == 0 {
		return
	}
	width, rows := m.resultWidth(), m.resultRows()
	nav := &m.rowNavigation
	if nav.width != width {
		*nav = tokenizerRowNavigation{width: width}
	}
	start := m.resultStart(width)
	cells := m.tokenCells(start, width, rows+1) // Include the row just below the viewport.
	current := cells[m.selected-start]
	if nav.column == 0 {
		nav.column = current.column + (current.width-1)/2
	}

	target := current.row - 1
	if down {
		target = current.row + 1
	}
	switch {
	case target < 0:
		if start == 0 {
			return
		}
		previous, seen := nav.previous[start]
		if !seen {
			previous = m.resultStartIncluding(start-1, width, 1)
			nav.remember(start, previous)
		}
		cells = m.tokenCells(previous, width, 1)
		start, target = previous, 0
	case target > cells[len(cells)-1].row:
		return
	case target == rows:
		next := m.walkResultSegments(start, width, 1, nil)
		nav.remember(next, start)
		start = next
	}

	nearest := max(width, nav.column) + 1
	for _, cell := range cells {
		if cell.row != target {
			continue
		}
		last := cell.column + cell.width - 1
		distance := max(cell.column-nav.column, nav.column-last, 0)
		if distance < nearest {
			m.selected, nearest = cell.index, distance
		}
	}
	m.tokenStart = start
}
