package readable

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
)

// ListTable renders one loaded page, keeping values in protectedColumn complete.
// A false result requests labeled output when the columns cannot fit safely.
func ListTable(headers []string, rows [][]string, width, protectedColumn int) (string, bool) {
	return listTable(headers, rows, width, protectedColumn, true)
}

// ListTableFullValues shows every field in full or requests labeled output.
func ListTableFullValues(headers []string, rows [][]string, width int) (string, bool) {
	return listTable(headers, rows, width, 0, false)
}

func listTable(headers []string, rows [][]string, width, protectedColumn int, truncate bool) (string, bool) {
	if protectedColumn < 0 || protectedColumn >= len(headers) || width <= 0 {
		return "", false
	}
	if len(rows) == 0 {
		return "No results.\n", true
	}
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
			cellWidth := ansi.StringWidth(cells[index][col])
			if col != protectedColumn && truncate {
				// Keep wide terminals compact. Full values remain in explicit formats.
				cellWidth = min(cellWidth, max(minimum[col], 32))
			}
			widths[col] = max(widths[col], cellWidth)
		}
	}
	minimum[protectedColumn] = widths[protectedColumn]
	total, required := 2*(len(headers)-1), 2*(len(headers)-1)
	for col := range widths {
		total += widths[col]
		required += minimum[col]
	}
	if required > width || !truncate && total > width {
		return "", false
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
	for _, row := range cells {
		for col := range row {
			if col != protectedColumn {
				row[col] = ansi.Truncate(row[col], widths[col], "…")
			}
		}
	}
	view := table.New().Headers(labels...).Rows(cells...).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderColumn(false).Wrap(false).
		StyleFunc(func(_, col int) lipgloss.Style {
			style := lipgloss.NewStyle()
			if col < len(headers)-1 {
				style = style.PaddingRight(2)
			}
			return style
		})
	// Lip Gloss aligns cells; remove only terminal padding at line endings.
	lines := strings.Split(view.String(), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	return strings.Join(lines, "\n") + "\n", true
}
