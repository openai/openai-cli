package vectorstores

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

func (m *workspace) bodyWidth() int { return max(1, min(100, m.width-6)) }
func cleanLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(readable.Text(s), "\n", `\n`), "\t", `\t`)
}

func (m *workspace) accent() lipgloss.Style {
	s := lipgloss.NewStyle()
	if !m.color {
		return s
	}
	color := "#3159BC"
	if m.dark {
		color = "#8AA8FF"
	}
	return s.Foreground(lipgloss.Color(color))
}

func (m *workspace) heading(text string) string {
	return lipgloss.NewStyle().Bold(m.color).Render(text)
}

func (m *workspace) divider() string {
	width := m.bodyWidth()
	if !m.color {
		return strings.Repeat("─", width)
	}
	// Blend the existing border and secondary-text colors symmetrically.
	// Nine bands keep the fade quiet and avoid a style escape for every cell.
	edge, center := [3]int{186, 196, 216}, [3]int{101, 112, 135}
	if m.dark {
		edge, center = [3]int{81, 86, 107}, [3]int{164, 172, 194}
	}
	var result strings.Builder
	for first := 0; first < width; {
		level := func(i int) int { return (16*min(i, width-1-i) + width/2) / max(1, width-1) }
		band, end := level(first), first+1
		for end < width && level(end) == band {
			end++
		}
		var rgb [3]int
		for i := range rgb {
			rgb[i] = edge[i] + (center[i]-edge[i])*band/8
		}
		color := fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
		result.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(strings.Repeat("─", end-first)))
		first = end
	}
	return result.String()
}

func (m *workspace) selection() lipgloss.Style {
	s := lipgloss.NewStyle()
	if !m.color {
		return s
	}
	fill, text := "#E9EFFE", "#20283B"
	if m.dark {
		fill, text = "#343D58", "#F2F4FA"
	}
	return s.Foreground(lipgloss.Color(text)).Background(lipgloss.Color(fill))
}

func (m *workspace) focusMarker() string {
	return m.accent().Inherit(m.selection()).Render("› ")
}

func (m *workspace) statusStyle(text string) lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.color {
		light, dark := "", ""
		switch text {
		case "completed":
			light, dark = "#236B43", "#8DC9A0"
		case "in_progress", "cancelling", "cancelled", "expired":
			light, dark = "#885B14", "#E2BC79"
		case "failed":
			light, dark = "#B23A3A", "#E59A9A"
		}
		if m.dark {
			light = dark
		}
		if light != "" {
			s = s.Foreground(lipgloss.Color(light))
		}
	}
	return s
}

func (m *workspace) status(text string) string {
	return m.statusStyle(text).Render(cleanLine(text))
}

func (m *workspace) muted() lipgloss.Style {
	s := lipgloss.NewStyle()
	if !m.color {
		return s
	}
	color := "#657087"
	if m.dark {
		color = "#A4ACC2"
	}
	return s.Foreground(lipgloss.Color(color))
}

func (m *workspace) row(text string, selected bool) string {
	text = ansi.Truncate(text, max(1, m.bodyWidth()-2), "…")
	if !selected {
		return "  " + text
	}
	selectedStyle := m.selection()
	return m.focusMarker() + selectedStyle.Bold(m.color).Render(text) +
		selectedStyle.Render(strings.Repeat(" ", max(0, m.bodyWidth()-2-ansi.StringWidth(text))))
}

func (m *workspace) actions(labels []string, first int) string {
	var items []string
	for i, label := range labels {
		text := "  " + label
		if m.focus == first+i {
			text = m.focusMarker() + m.selection().Bold(m.color).Render(label)
		}
		items = append(items, text)
	}
	result := strings.Join(items, "  ")
	if ansi.StringWidth(result) <= m.bodyWidth() {
		return result
	}
	selected := max(0, min(len(items)-1, m.focus-first))
	return items[selected] + "  " + m.muted().Render("· Tab for more")
}

func (m *workspace) editorLines(label string) []string {
	editor := m.editor
	styles := textarea.Styles{}
	styles.Focused.Placeholder, styles.Blurred.Placeholder = m.muted(), m.muted()
	editor.SetStyles(styles)
	// The textarea filters terminal controls, but permits directional marks.
	// Escape them in its rendered output so editing and submission keep the original value.
	field := readable.EscapeDirectionalControls(editor.View())
	caption := "  " + label
	if m.editing() {
		caption = m.accent().Render("› " + label)
	}
	// Short labels share the input row. Long labels retain room for editable text.
	if m.bodyWidth()-ansi.StringWidth(caption)-2 >= 12 {
		return []string{caption + "  " + field}
	}
	return []string{caption, "  " + field}
}

func (m *workspace) layoutEditor() {
	label := managementForms[m.page]
	switch m.page {
	case "home":
		label = "Search your files"
	case "create":
		label = "Store name"
	case "upload":
		label = "Local file path"
	}
	width := m.bodyWidth() - ansi.StringWidth(label) - 4
	if width < 12 {
		width = max(1, m.bodyWidth()-2)
	}
	m.editor.SetWidth(width)
	m.editor.SetHeight(1)
	// Reflow the real editor so its cursor and viewport use the rendered width.
	m.editor, _ = m.editor.Update(struct{}{})
}

func (m *workspace) listRows(items []string, selected, count int, active bool) []string {
	if len(items) == 0 {
		return []string{m.muted().Render("  No items yet.")}
	}
	count = max(1, count)
	if len(items) > count && count > 1 {
		count-- // Keep the range indicator inside the list's row budget.
	}
	first := max(0, min(selected-count+1, len(items)-count))
	rows := make([]string, 0, count+1)
	for i := first; i < min(len(items), first+count); i++ {
		rows = append(rows, m.row(items[i], active && selected == i))
	}
	if len(items) > count && count > 1 {
		rows = append(rows, m.muted().Render(fmt.Sprintf("  %d–%d of %d", first+1, min(len(items), first+count), len(items))))
	}
	return rows
}

func (m *workspace) View() tea.View {
	if m.quit {
		return tea.NewView("")
	}
	if m.width < 40 || m.height < 12 {
		if m.width >= 24 && m.height >= 6 {
			return tea.NewView(m.fit([]string{"Resize to 40 × 12."}, "Ctrl+C exits"))
		}
		return tea.NewView("Resize to 40 × 12.\nCtrl+C exits.")
	}
	lines := []string{m.header()}
	footer := "↑↓ choose · Tab actions · Enter open · Esc back"
	if m.focus > 0 && m.page != "stores" {
		footer = "←→ action · Enter select · ↑↓ return · Esc back"
	}
	if m.page == "home" {
		footer = m.searchFooter()
	} else if m.page == "upload" {
		if m.focus == 0 {
			footer = "↓ actions · Enter continue · Esc back"
		}
	} else if m.page == "stores" {
		footer = "↑↓ choose · Enter select · Esc exit"
	}
	if m.busy {
		lines = append(lines, m.divider(), m.accent().Render(m.stage))
		if m.page == "home" && m.stage == "Searching files…" {
			lines = append(lines, m.searchQueryLine())
		} else if m.store != nil {
			lines = append(lines, m.muted().Render("Store  ")+cleanLine(m.store.Name))
		}
		if m.page == "file" && m.stage == "Indexing file…" && m.file != nil {
			lines = append(lines, m.muted().Render("File  ")+cleanLine(m.file.ID))
		} else if m.pendingFile != "" {
			lines = append(lines, m.muted().Render("Uploaded file  ")+cleanLine(m.pendingFile))
		}
		if m.stage == "Indexing file…" {
			lines = append(lines, m.muted().Render("The file will become searchable when indexing completes."))
		}
		return tea.NewView(m.fit(lines, "Esc stops local work · Ctrl+C exits"))
	}
	switch m.page {
	case "stores":
		columns, items := m.storeTable()
		choices := m.storeChoices()
		trailing := 1 + len(choices)
		if m.storeFilter != "" {
			trailing++
		}
		lines = append(lines, m.divider(), m.tableHeader(columns))
		lines = append(lines, m.tableRows(columns, items, m.selected, m.listBudget(lines, footer, trailing), m.focus == 0)...)
		lines = append(lines, m.divider())
		labels := []string{"", "Create store", "Refresh stores", "Load more stores", "Find store by name or ID"}
		for _, focus := range choices {
			lines = append(lines, m.row(labels[focus], m.focus == focus))
		}
		if m.storeFilter != "" {
			lines = append(lines, m.muted().Render("Filter: "+cleanLine(m.storeFilter)+" · loaded stores only"))
		}
	case "create":
		lines = append(lines, m.editorLines("Store name")...)
		lines = append(lines, m.divider())
		lines = append(lines, m.actions([]string{"Create store", "Cancel"}, 1))
		lines = append(lines, m.muted().Render("Uses your current API project. No expiry is set."))
		footer = "↓ actions · Enter continue · Esc cancel"
		if m.focus > 0 {
			footer = "←→ action · Enter select · ↑ name · Esc cancel"
		}
	case "home":
		lines = m.searchLines(lines, footer)
	case "upload":
		lines = append(lines, m.editorLines("Local file path")...)
		lines = append(lines, m.divider())
		action, browse := "Upload", "Browse files"
		if m.pendingFile != "" {
			action, browse = "Attach uploaded file", "Choose another file"
		}
		if m.pendingAttached {
			action = "Resume indexing"
		}
		lines = append(lines, m.actions([]string{browse, action, "Back", "Options"}, 1))
		if m.pendingFile != "" {
			lines = append(lines, m.muted().Render("Uploaded file  ")+cleanLine(m.pendingFile))
		}
		lines = append(lines, m.muted().Render("Uploads only when you select Upload."))
	case "files", "batchfiles":
		lines = append(lines, m.actions([]string{"Refresh", "Load more", "Back"}, 1), m.divider())
		columns, items := m.fileTable()
		lines = append(lines, m.tableHeader(columns))
		lines = append(lines, m.tableRows(columns, items, m.selected, m.listBudget(lines, footer, 2), m.focus == 0)...)
		lines = append(lines, m.divider())
		lines = append(lines, m.muted().Render("Enter opens file actions."))
	case "browse":
		lines = append(lines, m.muted().Render(cleanLine(m.directory)), m.actions([]string{"Parent folder", "Back"}, 1), m.divider())
		items := make([]string, 0, len(m.entries))
		for _, entry := range m.entries {
			name := cleanLine(entry.name)
			if entry.directory {
				name += "/"
			}
			items = append(items, name)
		}
		lines = append(lines, m.listRows(items, m.selected, m.listBudget(lines, footer, 0), m.focus == 0)...)
	case "passage":
		result := m.results[m.resultSelected]
		name := result.Filename
		if name == "" {
			name = result.FileID
		}
		lines = append(lines, m.heading(cleanLine(name)), m.muted().Render(fmt.Sprintf("Relevance score: %.3f", result.Score)))
		lines = append(lines, m.divider())
		wrapped := m.passageLines
		count := m.passageRows()
		offset := min(m.offset, max(0, len(wrapped)-count))
		lines = append(lines, wrapped[offset:min(len(wrapped), offset+count)]...)
		footer = searchPassageFooter
	case "document":
		lines = append(lines, m.heading(cleanLine(m.documentTitle)), m.divider())
		count := m.passageRows()
		offset := min(m.offset, max(0, len(m.passageLines)-count))
		lines = append(lines, m.passageLines[offset:min(len(m.passageLines), offset+count)]...)
		footer = passageFooter
	default:
		body, hint := m.managementLines()
		lines = append(lines, body...)
		if hint != "" {
			footer = hint
		}
	}
	if m.note != "" {
		note := m.feedbackLines()
		for i := range note {
			style := m.muted()
			if i == 0 && m.lastErr != nil {
				style = m.statusStyle("failed")
			}
			note[i] = style.Render(note[i])
		}
		lines = append(append(append([]string{}, lines[0]), note...), lines[1:]...)
	}
	return tea.NewView(m.fit(lines, footer))
}

func (m *workspace) feedbackLines() []string {
	if m.note == "" {
		return nil
	}
	note := strings.Split(ansi.Hardwrap(cleanLine(m.note), m.bodyWidth(), true), "\n")
	if m.height < 16 {
		return []string{ansi.Truncate(cleanLine(m.note), m.bodyWidth(), "…")}
	}
	if len(note) > 2 {
		note = append(note[:1], ansi.Truncate(strings.Join(note[1:], " "), m.bodyWidth(), "…"))
	}
	return note
}

func (m *workspace) passageRows() int {
	if m.page == "document" {
		return max(1, m.contentRows(passageFooter)-3-len(m.feedbackLines()))
	}
	return max(1, m.contentRows(searchPassageFooter)-4-len(m.feedbackLines()))
}

func (m *workspace) fit(lines []string, footer string) string {
	width := m.bodyWidth()
	limit := m.contentRows(footer)
	// Small terminals keep controls visible before decorative spacing.
	if len(lines) > limit {
		divider := m.divider()
		for i := len(lines) - 1; len(lines) > limit && i >= 0; i-- {
			if lines[i] == "" || lines[i] == divider {
				lines = append(lines[:i:i], lines[i+1:]...)
			}
		}
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}
	border := m.muted()
	accent := m.accent()
	frame := []string{" " + accent.Render("┌──") + border.Render(strings.Repeat("─", width)+"┐")}
	side := border.Render("│")
	left := accent.Render("│")
	for _, line := range lines {
		line = ansi.Truncate(line, width, "…")
		frame = append(frame, " "+left+" "+line+strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))+" "+side)
		left = side
	}
	for _, line := range m.footerLines(footer) {
		frame = append(frame, " "+side+" "+line+strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))+" "+side)
	}
	frame = append(frame, " "+border.Render("└"+strings.Repeat("─", width+2)+"┘"))
	return strings.Join(frame, "\n")
}

const (
	passageFooter       = "↑↓ scroll · PgUp/PgDn page · Esc back · q quit"
	searchPassageFooter = "↑↓ scroll · PgUp/PgDn page · Esc results · q quit"
)

func (m *workspace) contentRows(footer string) int {
	return max(1, m.height-3-len(m.footerLines(footer)))
}

func (m *workspace) listBudget(lines []string, footer string, trailing int) int {
	return max(1, m.contentRows(footer)-len(lines)-len(m.feedbackLines())-trailing)
}

func (m *workspace) footerLines(footer string) []string {
	var lines []string
	line := ""
	for _, hint := range strings.Split(footer, " · ") {
		key, description, _ := strings.Cut(hint, " ")
		item := m.accent().Render(key) + m.muted().Render(" "+description)
		if line != "" && ansi.StringWidth(line)+2+ansi.StringWidth(item) > m.bodyWidth() {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += "  "
		}
		line += item
	}
	return append(lines, line)
}

func (m *workspace) header() string {
	active := m.accent().Bold(m.color)
	title := active.Render("Vector stores")
	if m.store != nil && m.page != "stores" && m.page != "create" {
		name := m.store.Name
		if name == "" {
			name = m.store.ID
		}
		title = m.muted().Render("Vector stores / ") + active.Render(cleanLine(name))
	}
	count := ""
	switch m.page {
	case "stores":
		count = fmt.Sprintf("%d loaded", len(m.stores))
		if m.storeFilter != "" {
			count = fmt.Sprintf("%d matches", len(m.filteredStores()))
		}
	case "files", "batchfiles":
		count = fmt.Sprintf("%d loaded", len(m.files))
	case "sources":
		selected := 0
		for _, active := range m.selectedSources {
			if active {
				selected++
			}
		}
		count = fmt.Sprintf("%d selected", selected)
	case "home":
		if m.focus == matchListFocus {
			count = fmt.Sprintf("%d matches", len(m.results))
			if m.moreResults {
				count = fmt.Sprintf("%d shown", len(m.results))
			}
		}
	}
	if count == "1 matches" {
		count = "1 match"
	}
	if count == "" {
		return ansi.Truncate(title, m.bodyWidth(), "…")
	}
	width := max(1, m.bodyWidth()-ansi.StringWidth(count)-2)
	title = ansi.Truncate(title, width, "…")
	return title + strings.Repeat(" ", max(2, m.bodyWidth()-ansi.StringWidth(title)-ansi.StringWidth(count))) + m.muted().Render(count)
}

func (m *workspace) layoutPassage() {
	if m.page == "document" && len(m.documentFields) > 0 {
		m.passageLines = m.detailLines(m.documentFields)
		return
	}
	text := strings.ReplaceAll(readable.Text(m.passage), "\t", "    ")
	m.passageLines = strings.Split(ansi.Hardwrap(text, m.bodyWidth(), true), "\n")
}
