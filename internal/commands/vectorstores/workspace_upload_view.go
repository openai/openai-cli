package vectorstores

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type uploadMenuItem struct{ label, hint string }

// Keep complete characters and the filename when a path exceeds the available cells.
func pathTail(path string, width int) string {
	if width <= 0 {
		return ""
	}
	cells := ansi.StringWidth(path)
	if cells <= width {
		return path
	}
	for len(path) > 0 && cells > width-1 {
		char, size := ansi.FirstGraphemeCluster(path, ansi.GraphemeWidth)
		path, cells = path[len(char):], cells-size
	}
	return "…" + path
}

func (m *workspace) uploadPathField() string {
	value := m.editor.Value()
	if value == "" {
		return m.editorField()
	}
	width := m.editor.Width()
	path := cleanLine(value)
	if !m.editing() {
		return pathTail(path, width)
	}
	if m.editor.LineCount() > 1 {
		return m.editorField()
	}

	// Scroll horizontally around the editor's logical cursor. A wrapped textarea
	// can leave only its end cursor on screen when a path exactly fills a row.
	start, char := len(path), " "
	runes := []rune(value)
	if col := m.editor.Column(); col < len(runes) {
		position := len(cleanLine(string(runes[:col])))
		for i := 0; i < len(path); {
			cluster, _ := ansi.FirstGraphemeCluster(path[i:], ansi.GraphemeWidth)
			if position < i+len(cluster) {
				start, char = i, cluster
				break
			}
			i += len(cluster)
		}
	}
	after := path[min(len(path), start+len(char)):]
	room := width - ansi.StringWidth(char)
	before := pathTail(path[:start], room-min(ansi.StringWidth(after), room/3))
	after = ansi.Truncate(after, room-ansi.StringWidth(before), "…")
	return before + lipgloss.NewStyle().Reverse(true).Render(char) + after
}

// The menu stays in keyboard order in both the split and stacked layouts.
func (m *workspace) uploadMenu(items []uploadMenuItem, width, rows int) []string {
	rows = max(1, min(rows, len(items)))
	first := max(0, m.focus-rows)
	heading := m.heading("Actions")
	if rows < len(items) {
		heading += m.muted().Render(fmt.Sprintf("  %d–%d of %d", first+1, first+rows, len(items)))
	}
	lines := []string{heading}
	labelWidth := 0
	for _, item := range items {
		labelWidth = max(labelWidth, ansi.StringWidth(item.label)+2)
	}
	for i := first; i < first+rows; i++ {
		active := m.focus == i+1
		if m.wideLayout() && width-labelWidth >= 18 {
			line := m.rowWidth(items[i].label, active, labelWidth)
			gap := strings.Repeat(" ", labelWidth-ansi.StringWidth(line)+2)
			lines = append(lines, line+gap+m.muted().Render(ansi.Truncate(items[i].hint, width-labelWidth-2, "…")))
		} else {
			lines = append(lines, m.rowWidth(items[i].label, active, width))
		}
	}
	return lines
}

func (m *workspace) uploadLines(lines []string) ([]string, string) {
	footer := "Tab actions · Enter review upload · Esc back"
	if m.focus > 0 {
		footer = "↑↓ move · Tab next · Enter select · Esc back"
	}
	items := []uploadMenuItem{
		{"Browse files", "Choose from this computer"},
		{"Upload", "Add the file to this store"},
		{"Back", "Return to search"},
		{"Options", "Attributes and chunking"},
	}
	if m.uploadFromFiles {
		items[2].hint = "Return to attached files"
	}
	left := []string{m.heading("Local file path")}
	marker := "  "
	if m.focus == 0 {
		marker = m.accent().Render("› ")
	}
	if m.pendingFile == "" {
		left = append(left, marker+m.uploadPathField())
	} else {
		left[0] = m.heading("Uploaded file")
		if m.pendingName != "" {
			left = append(left, marker+cleanLine(m.pendingName))
			marker = "  "
		}
		status := "Attachment not confirmed."
		items[0] = uploadMenuItem{"Choose another file", "Keep this source in storage"}
		items[1] = uploadMenuItem{"Attach uploaded file", "Check this store, then attach if needed"}
		items[3].hint = "Finish this upload first"
		if m.pendingAttached {
			status = "Attached. Check indexing to continue."
			items[1] = uploadMenuItem{"Resume indexing", "Check the attachment"}
		}
		left = append(left, m.muted().Render(status))
		left = append(left, marker+m.muted().Render(cleanLine(m.pendingFile)))
		if m.focus == 2 {
			footer = "Enter check attachment · ↑↓ actions · Esc back"
			if m.pendingAttached {
				footer = "Enter check indexing · ↑↓ actions · Esc back"
			}
		}
	}
	width := m.bodyWidth()
	if m.wideLayout() {
		_, width = m.paneWidths()
		right := m.uploadMenu(items, width, len(items))
		return m.appendPanes(lines, left, right, footer), footer
	}
	// Keep the selected action visible even when an error wraps in a short terminal.
	room := m.listBudget(lines, footer, 2)
	lines = append(lines, left[:min(len(left), room)]...)
	lines = append(lines, m.uploadMenu(items, width, m.listBudget(lines, footer, 1))...)
	return lines, footer
}

func (m *workspace) browserLines(lines []string) ([]string, string) {
	footer := "↑↓ choose · / find · Tab actions · Enter open folder"
	if m.focus > 0 {
		footer = "↑↓ move · / find · Tab next · Enter select"
	} else if len(m.entries) == 0 {
		footer = "/ find · Tab actions"
	} else if !m.entries[m.selected].directory {
		footer = "↑↓ choose · / find · Tab actions · Enter choose file"
	}
	if m.findingFile {
		footer = strings.Replace(footer, "/ find · ", "", 1)
	}
	if m.findingFile || m.browserFilter != "" {
		footer += " · Esc clear"
	} else {
		footer += " · Esc back"
	}
	width, actionWidth := m.bodyWidth(), m.bodyWidth()
	if m.wideLayout() {
		width, actionWidth = m.paneWidths()
	}
	dir := cleanLine(m.directory)
	if n := ansi.StringWidth(dir) - width; n > 0 {
		dir = ansi.TruncateLeft(dir, n+1, "…")
	}
	left := []string{m.heading("Local files"), m.muted().Render(dir)}
	if m.height >= 16 || m.findingFile || m.browserFilter != "" {
		value := cleanLine(m.browserFilter)
		if value == "" {
			value = "Name in this folder"
		}
		field := m.muted().Render(value)
		if m.findingFile {
			field = m.editorField()
		}
		left = append(left, m.accent().Render("/")+" "+m.heading("Find")+"  "+field)
	}
	items := []uploadMenuItem{{"Parent folder", "Go up one folder"}, {"Back", "Return to upload"}}
	actionRows := len(items)
	room := m.listBudget(lines, footer, len(left))
	if !m.wideLayout() {
		actionRows = min(actionRows, max(1, room-2)) // Reserve one entry and the Actions heading.
		room -= actionRows + 1
	}
	actions := m.uploadMenu(items, actionWidth, actionRows)
	if len(m.entries) == 0 {
		message := "This folder is empty."
		if m.browserFilter != "" {
			message = "No names match in this folder."
		} else if len(m.directoryEntries) > 0 {
			message = "Only hidden entries. Use / then ."
		} else if m.lastErr != nil && m.note != "" {
			message = "No folder entries loaded."
		}
		left = append(left, m.muted().Render("  "+message))
	} else {
		names := make([]string, len(m.entries))
		for i, entry := range m.entries {
			names[i] = cleanLine(entry.name)
			if entry.directory {
				names[i] += "/"
			}
		}
		left = append(left, m.listRows(names, m.selected, room, m.focus == 0, width)...)
	}
	if m.wideLayout() {
		return m.appendPanes(lines, left, actions, footer), footer
	}
	return append(append(lines, left...), actions...), footer
}
