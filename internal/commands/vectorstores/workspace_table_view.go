package vectorstores

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Workspace tables share cell widths across their header and visible rows.
type workspaceColumn struct {
	label  string
	width  int
	right  bool
	status bool
}

const workspaceColumnGap = "   "

func (m *workspace) storeTable() ([]workspaceColumn, [][]string) {
	wide := m.bodyWidth() >= 80
	idWidth := 35
	if m.bodyWidth() < 50 {
		idWidth = (m.bodyWidth() - 2 - len(workspaceColumnGap)) / 2
	}
	columns := []workspaceColumn{{label: "Store"}, {label: "ID", width: idWidth}}
	if wide {
		columns = []workspaceColumn{{label: "Store"}, {label: "Status", width: 11, status: true}, {label: "Files", width: 5, right: true}}
		columns = append(columns, workspaceColumn{label: "ID", width: 35})
	}
	m.sizeTable(columns)
	var rows [][]string
	for _, store := range m.filteredStores() {
		name := store.Name
		if name == "" {
			name = "Untitled store"
		}
		status := string(store.Status)
		if status == "" {
			status = "unknown"
		}
		row := []string{name, store.ID}
		if wide {
			row = []string{name, status, fmt.Sprint(store.FileCounts.Total), store.ID}
		}
		rows = append(rows, row)
	}
	return columns, rows
}

func (m *workspace) fileTable() ([]workspaceColumn, [][]string) {
	columns := []workspaceColumn{{label: "File"}, {label: "Status", width: 11, status: true}}
	// Without filenames, the identity column already contains each file ID.
	showID := false
	for _, file := range m.files {
		showID = showID || m.names[file.ID] != ""
	}
	showID = showID && m.bodyWidth() >= 80
	if showID {
		columns = append(columns, workspaceColumn{label: "ID", width: 29})
	}
	m.sizeTable(columns)
	var rows [][]string
	for _, file := range m.files {
		name := m.names[file.ID]
		if name == "" {
			name = file.ID
			if showID {
				name = "Unknown filename"
			}
		}
		status := string(file.Status)
		if status == "" {
			status = "unknown"
		}
		rows = append(rows, []string{name, status, file.ID})
	}
	return columns, rows
}

func (m *workspace) resultTable() ([]workspaceColumn, [][]string) {
	columns := []workspaceColumn{{label: "File"}, {label: "Score", width: 7, right: true}}
	m.sizeTable(columns)
	rows := make([][]string, 0, len(m.results))
	for _, result := range m.results {
		name := result.Filename
		if name == "" {
			name = result.FileID
		}
		rows = append(rows, []string{name, fmt.Sprintf("%.3f", result.Score)})
	}
	return columns, rows
}

func (m *workspace) sourceTable() ([]workspaceColumn, [][]string) {
	idWidth := 29
	if m.bodyWidth() < 50 {
		idWidth = (m.bodyWidth() - 2 - len(workspaceColumnGap)) / 2
	}
	columns := []workspaceColumn{{label: "File"}, {label: "ID", width: idWidth}}
	m.sizeTable(columns)
	rows := make([][]string, 0, len(m.sourceFiles))
	for _, file := range m.sourceFiles {
		mark := "[ ] "
		if m.selectedSources[file.ID] {
			mark = "[x] "
		}
		name := file.Filename
		if name == "" {
			name = "Unknown filename"
		}
		rows = append(rows, []string{mark + name, file.ID})
	}
	return columns, rows
}

func (m *workspace) sizeTable(columns []workspaceColumn) {
	width := m.bodyWidth() - 2 - len(workspaceColumnGap)*(len(columns)-1)
	for _, column := range columns[1:] {
		width -= column.width
	}
	columns[0].width = max(1, width)
}

func tableCell(text string, column workspaceColumn) string {
	text = ansi.Truncate(cleanLine(text), column.width, "…")
	padding := strings.Repeat(" ", max(0, column.width-ansi.StringWidth(text)))
	if column.right {
		return padding + text
	}
	return text + padding
}

func (m *workspace) tableHeader(columns []workspaceColumn) string {
	cells := make([]string, len(columns))
	for i, column := range columns {
		cells[i] = tableCell(column.label, column)
	}
	return m.muted().Render("  " + strings.Join(cells, workspaceColumnGap))
}

func (m *workspace) tableRow(columns []workspaceColumn, values []string, selected bool) string {
	base := lipgloss.NewStyle()
	marker := "  "
	if selected {
		base, marker = m.selection(), m.focusMarker()
	}
	cells := make([]string, len(columns))
	for i, column := range columns {
		value := values[i]
		style := base
		if column.status {
			style = m.statusStyle(value).Inherit(base)
		} else if selected && i == 0 {
			style = style.Bold(m.color)
		} else if !selected && i > 0 {
			style = m.muted()
		}
		if column.status {
			text := ansi.Truncate(cleanLine(value), column.width, "…")
			cells[i] = style.Render(text) + base.Render(strings.Repeat(" ", max(0, column.width-ansi.StringWidth(text))))
		} else {
			cells[i] = style.Render(tableCell(value, column))
		}
	}
	return marker + strings.Join(cells, base.Render(workspaceColumnGap))
}

func (m *workspace) tableRows(columns []workspaceColumn, items [][]string, selected, count int, active bool) []string {
	if len(items) == 0 {
		return []string{m.muted().Render("  No items yet.")}
	}
	count = max(1, count)
	if len(items) > count && count > 1 {
		count--
	}
	first := max(0, min(selected-count+1, len(items)-count))
	rows := make([]string, 0, count+1)
	for i := first; i < min(len(items), first+count); i++ {
		rows = append(rows, m.tableRow(columns, items[i], active && selected == i))
	}
	if len(items) > count && count > 1 {
		rows = append(rows, m.muted().Render(fmt.Sprintf("  %d–%d of %d", first+1, min(len(items), first+count), len(items))))
	}
	return rows
}
