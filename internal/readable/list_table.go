package readable

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
)

// ListTable renders one loaded page, keeping values in protectedColumn complete.
// A false result requests labeled output when the columns cannot fit safely.
func ListTable(headers []string, rows [][]string, width, protectedColumn int) (string, bool) {
	return listTable(headers, rows, width, protectedColumn, true, nil)
}

// ListTableFullValues shows every field in full or requests labeled output.
func ListTableFullValues(headers []string, rows [][]string, width int) (string, bool) {
	return listTable(headers, rows, width, 0, false, nil)
}

// ListTableLayout retains only headers and column widths between printed pages.
type ListTableLayout struct {
	headers         []string
	widths          []int
	protectedColumn int
	fullValues      bool
}

// Render prints the header on the first compatible page. A false result asks
// the caller to print complete labeled records instead.
func (l *ListTableLayout) Render(headers []string, rows [][]string, width, protectedColumn int, fullValues bool) (string, bool) {
	if l.widths != nil && (!slices.Equal(l.headers, headers) || l.protectedColumn != protectedColumn || l.fullValues != fullValues) {
		return "", false
	}
	return listTable(headers, rows, width, protectedColumn, !fullValues, l)
}

const compactColumnWidth = 32

func listTable(headers []string, rows [][]string, width, protectedColumn int, truncate bool, layout *ListTableLayout) (string, bool) {
	if protectedColumn < 0 || protectedColumn >= len(headers) || width <= 0 {
		return "", false
	}
	if len(rows) == 0 {
		return "No results.\n", true
	}
	firstPage := layout == nil || layout.widths == nil
	labels := make([]string, len(headers))
	widths := make([]int, len(headers))
	minimum := make([]int, len(headers))
	for col, header := range headers {
		labels[col] = InlineText(header)
		minimum[col] = ansi.StringWidth(labels[col])
		widths[col] = minimum[col]
	}
	cells := make([][]string, len(rows))
	for index, row := range rows {
		if len(row) != len(headers) {
			return "", false
		}
		cells[index] = make([]string, len(row))
		for col, value := range row {
			cells[index][col] = InlineText(value)
			// A continued page measures only fields that must fit intact or
			// can grow safely at the end. Other fields have a fixed width.
			if !firstPage && truncate && col != protectedColumn && col < len(headers)-1 {
				continue
			}
			cellWidth := ansi.StringWidth(cells[index][col])
			if col != protectedColumn && truncate {
				// Keep wide terminals compact. Full values remain in explicit formats.
				cellWidth = min(cellWidth, max(minimum[col], compactColumnWidth))
			}
			widths[col] = max(widths[col], cellWidth)
		}
	}
	if firstPage {
		spare := fitInitialListColumns(widths, minimum, width, protectedColumn, truncate)
		if spare < 0 {
			return "", false
		}
		if layout != nil {
			layout.start(headers, widths, spare, protectedColumn, !truncate)
		}
	} else if !layout.fitPage(widths, minimum, width) {
		return "", false
	}
	if truncate {
		for _, row := range cells {
			for col := range row {
				if col != protectedColumn {
					row[col] = ansi.Truncate(row[col], widths[col], "…")
				}
			}
		}
	}
	view := table.New().Rows(cells...).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderColumn(false).Wrap(false).
		StyleFunc(func(_, col int) lipgloss.Style {
			style := lipgloss.NewStyle()
			if col < len(headers)-1 {
				style = style.PaddingRight(2)
			}
			if layout != nil {
				style = style.Width(widths[col] + style.GetHorizontalFrameSize())
			}
			return style
		})
	if firstPage {
		view.Headers(labels...)
	}
	// Lip Gloss aligns cells; remove only terminal padding at line endings.
	lines := strings.Split(view.String(), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	return strings.Join(lines, "\n") + "\n", true
}

// Fit compact columns into the terminal, protecting the complete ID or every
// value for a full-value projection. Return the space available for later pages.
func fitInitialListColumns(widths, minimum []int, width, protectedColumn int, truncate bool) int {
	minimum[protectedColumn] = widths[protectedColumn]
	total, required := 2*(len(widths)-1), 2*(len(widths)-1)
	for col := range widths {
		total += widths[col]
		required += minimum[col]
	}
	if required > width || !truncate && total > width {
		return -1
	}
	for total > width {
		widest := -1
		for col := range widths {
			if widths[col] > minimum[col] && (widest < 0 || widths[col] > widths[widest]) {
				widest = col
			}
		}
		widths[widest]--
		total--
	}
	return width - total
}

func (l *ListTableLayout) start(headers []string, widths []int, spare, protectedColumn int, fullValues bool) {
	// Allocate spare cells to the protected ID first, then to the remaining
	// columns in display order. No future page is read.
	grow := func(col int) {
		added := min(spare, max(0, compactColumnWidth-widths[col]))
		widths[col] += added
		spare -= added
	}
	grow(protectedColumn)
	for col := range widths {
		grow(col)
	}
	l.headers, l.widths = slices.Clone(headers), widths
	l.protectedColumn, l.fullValues = protectedColumn, fullValues
}

// The final column can use available space without moving any column starts.
func (l *ListTableLayout) fitPage(widths, minimum []int, width int) bool {
	last := len(widths) - 1
	remaining := width - 2*last
	for col, needed := range widths {
		capacity := l.widths[col]
		if col == last {
			if remaining < minimum[col] {
				return false
			}
			capacity = min(remaining, max(capacity, needed))
		}
		if (l.fullValues || col == l.protectedColumn) && needed > capacity {
			return false
		}
		widths[col] = capacity
		remaining -= capacity
	}
	return true
}
