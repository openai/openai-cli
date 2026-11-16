package vectorstores

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminal"
)

func (m *workspace) framed() bool     { return m.width >= 50 && m.height >= 16 }
func (m *workspace) wideLayout() bool { return m.width >= 96 && m.height >= 16 }
func (m *workspace) bodyWidth() int {
	if m.fullScreen {
		if m.framed() {
			return max(1, m.width-5)
		}
		return max(1, m.width-3)
	}
	if m.wideLayout() {
		return min(140, m.width-5)
	}
	return max(1, min(100, m.width-6))
}

func (m *workspace) paneWidths() (int, int) {
	available := m.bodyWidth() - 3
	left := available * 45 / 100
	return left, available - left
}

func (m *workspace) appendPanes(lines, left, right []string, footer string) []string {
	leftWidth, rightWidth := m.paneWidths()
	seam := " " + terminal.PaneDivider(leftWidth+4, m.border(), m.decLineDrawing) + " "
	height := max(len(left), len(right))
	if m.fullScreen {
		height = max(height, m.listBudget(lines, footer, 0))
	}
	for i := range height {
		var first, second string
		if i < len(left) {
			first = ansi.Truncate(left[i], leftWidth, "…")
		}
		if i < len(right) {
			second = ansi.Truncate(right[i], rightWidth, "…")
		}
		lines = append(lines, first+strings.Repeat(" ", max(0, leftWidth-ansi.StringWidth(first)))+seam+second)
	}
	return lines
}

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

func (m *workspace) border() lipgloss.Style {
	s := lipgloss.NewStyle()
	if !m.color {
		return s
	}
	if m.dark {
		return s.Foreground(lipgloss.Color("#51566B"))
	}
	return s.Foreground(lipgloss.Color("#BAC4D8"))
}
func (m *workspace) divider() string {
	if m.dark {
		return terminal.SectionDivider(m.bodyWidth(), m.color, "#343D58", "#51566B", "#A4ACC2")
	}
	return terminal.SectionDivider(m.bodyWidth(), m.color, "#E9EFFE", "#BAC4D8", "#657087")
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
	return m.rowWidth(text, selected, m.bodyWidth())
}

func (m *workspace) rowWidth(text string, selected bool, width int) string {
	text = ansi.Truncate(text, max(1, width-2), "…")
	if !selected {
		return "  " + text
	}
	selectedStyle := m.selection()
	return m.focusMarker() + selectedStyle.Bold(m.color).Render(text) +
		selectedStyle.Render(strings.Repeat(" ", max(0, width-2-ansi.StringWidth(text))))
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
	return items[selected] + "  " + m.muted().Render(fmt.Sprintf("%d/%d", selected+1, len(items)))
}

func (m *workspace) editorLines(label string) []string {
	field := m.editorField()
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

func (m *workspace) editorField() string {
	editor := m.editor
	styles := textarea.Styles{}
	styles.Focused.Placeholder, styles.Blurred.Placeholder = m.muted(), m.muted()
	editor.SetStyles(styles)
	// Escape directional marks in the view while preserving the original input.
	return readable.EscapeDirectionalControls(editor.View())
}

func (m *workspace) reflow() {
	m.layoutEditor()
	if m.page == "passage" || m.page == "document" {
		m.layoutPassage()
	}
}

func (m *workspace) layoutEditor() {
	label := managementForms[m.page]
	switch m.page {
	case "create":
		label = "Store name"
	case "upload":
		label = "Local file path"
	}
	available := m.bodyWidth()
	width := available - ansi.StringWidth(label) - 4
	if width < 12 {
		width = max(1, available-2)
	}
	if m.page == "home" || m.page == "upload" {
		if m.wideLayout() {
			available, _ = m.paneWidths()
		}
		width = max(1, available-2) // The query has its own row after the focus marker.
	} else if m.page == "filter" || m.page == "browse" {
		if m.wideLayout() {
			available, _ = m.paneWidths()
		}
		label = storeFinderLabel
		if m.page == "browse" {
			label = "Find"
		}
		width = max(1, available-len(label)-4)
	}
	m.editor.DynamicHeight = m.page == "home"
	m.editor.SetWidth(width)
	if !m.editor.DynamicHeight {
		m.editor.SetHeight(1)
	}
	// Reflow the real editor so its cursor and viewport use the rendered width.
	m.editor, _ = m.editor.Update(struct{}{})
}

func (m *workspace) listRows(items []string, selected, count int, active bool, width int) []string {
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
		rows = append(rows, m.rowWidth(items[i], active && selected == i, width))
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
		if m.fullScreen {
			width := max(0, m.width-1)
			hint := ansi.Truncate("Ctrl+G compact", width, "")
			if m.height <= 1 {
				return tea.NewView(hint)
			}
			return tea.NewView(ansi.Truncate("Resize to 40 × 12.", width, "") + strings.Repeat("\n", m.height-1) + hint)
		}
		return tea.NewView("Resize to 40 × 12.\nCtrl+C exits.")
	}
	lines := []string{m.header(), m.divider()}
	footer := "↑↓ choose · Tab actions · Enter open · Esc back"
	if m.focus > 0 && m.page != "stores" {
		footer = "Tab next · Enter select · ↑↓ return · Esc back"
	}
	if m.page == "home" {
		footer = m.searchFooter()
	} else if m.page == "stores" || m.page == "filter" {
		footer = m.storeFooter()
	}
	if m.busy {
		if m.stage == indexingStage {
			file := m.pendingName
			if file == "" {
				file = m.pendingFile
			}
			if m.page == "file" && m.file != nil {
				file = m.file.ID
			}
			checks := "Waiting for first status check…"
			if m.statusChecks > 0 {
				checks = fmt.Sprintf("Status checks  %d", m.statusChecks)
			}
			lines = append(lines,
				m.muted().Render("File  ")+cleanLine(file),
				m.statusStyle("completed").Render("✓ Uploaded   ✓ Attached"),
				m.accent().Render(m.stage),
				m.muted().Render(checks),
				m.muted().Render("Searchable after indexing."),
			)
			return tea.NewView(m.fit(lines, "Esc stop waiting · Ctrl+C exit"))
		}
		lines = append(lines, m.accent().Render(m.stage))
		if m.page == "home" && m.stage == "Searching files…" {
			lines = append(lines, m.searchQueryLine())
		} else if m.store != nil {
			lines = append(lines, m.muted().Render("Store  ")+cleanLine(m.store.Name))
		}
		if m.page == "upload" && m.pendingName != "" {
			lines = append(lines, m.muted().Render("File  ")+cleanLine(m.pendingName))
		} else if m.page == "upload" && m.pendingFile != "" {
			lines = append(lines, m.muted().Render("Uploaded file  ")+cleanLine(m.pendingFile))
		}
		return tea.NewView(m.fit(lines, "Esc stops local work · Ctrl+C exits"))
	}
	switch m.page {
	case "stores", "filter":
		lines = m.storeLines(lines, footer)
	case "create":
		lines = append(lines, m.editorLines("Store name")...)
		lines = append(lines, m.divider())
		lines = append(lines, m.actions([]string{"Create store", "Cancel"}, 1))
		lines = append(lines, m.muted().Render("Uses your current API project. No expiry is set."))
		footer = "Tab actions · Enter continue · Esc cancel"
		if m.focus > 0 {
			footer = "Tab next · Enter select · ↑ name · Esc cancel"
		}
	case "home":
		lines = m.searchLines(lines, footer)
	case "upload":
		lines, footer = m.uploadLines(lines)
	case "files", "batchfiles":
		lines, footer = m.fileListLines(lines)
	case "browse":
		lines, footer = m.browserLines(lines)
	case "passage":
		result := m.results[m.resultSelected]
		name := result.Filename
		if name == "" {
			name = result.FileID
		}
		lines = append(lines, m.heading(cleanLine(name)), m.muted().Render(fmt.Sprintf("Relevance score: %.3f", result.Score)))
		wrapped := m.passageLines
		count := m.passageRows()
		offset := min(m.offset, max(0, len(wrapped)-count))
		lines = append(lines, wrapped[offset:min(len(wrapped), offset+count)]...)
		footer = searchPassageFooter
	case "document":
		lines = append(lines, m.heading(cleanLine(m.documentTitle)))
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
		lines = append(append(append([]string{}, lines[:2]...), note...), lines[2:]...)
	}
	return tea.NewView(m.fit(lines, footer))
}

func (m *workspace) feedbackLines() []string {
	message, _ := m.storeFeedback()
	if message == "" {
		return nil
	}
	width := m.bodyWidth()
	limit := 2
	if strings.Contains(message, "\n") {
		// The formatter puts recovery before diagnostics. Leave room for both on small screens.
		limit = min(6, max(2, m.height-13))
	} else if m.height < 16 {
		return []string{ansi.Truncate(cleanLine(message), width, "…")}
	}
	var rows []string
	for _, line := range strings.Split(message, "\n") {
		if line != "" {
			rows = append(rows, strings.Split(ansi.Wrap(cleanLine(line), width, ""), "\n")...)
		}
		if len(rows) >= limit {
			return rows[:limit]
		}
	}
	return rows
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
	if m.fullScreen && len(lines) < limit {
		lines = append(lines, make([]string, limit-len(lines))...)
	}
	var frame []string
	const indent = "  "
	if m.framed() {
		border := m.border()
		rule := strings.Repeat("─", width+2)
		side := border.Render("│")
		frame = append(frame, border.Render("╭"+rule+"╮"))
		for _, line := range lines {
			line = ansi.Truncate(line, width, "…")
			frame = append(frame, side+" "+line+strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))+" "+side)
		}
		frame = append(frame, border.Render("╰"+rule+"╯"))
	} else {
		for _, line := range lines {
			frame = append(frame, indent+ansi.Truncate(line, width, "…"))
		}
	}
	for _, line := range m.footerLines(footer) {
		frame = append(frame, indent+line)
	}
	return strings.Join(frame, "\n")
}

const (
	passageFooter       = "↑↓ scroll · PgUp/PgDn page · Esc back · q quit"
	searchPassageFooter = "↑↓ scroll · PgUp/PgDn page · Esc results · q quit"
)

func (m *workspace) contentRows(footer string) int {
	rows := m.height - len(m.footerLines(footer))
	if !m.fullScreen {
		rows-- // Leave room for the shell in the inline view.
	}
	if m.framed() {
		rows -= 2
	}
	return max(1, rows)
}

func (m *workspace) listBudget(lines []string, footer string, trailing int) int {
	return max(1, m.contentRows(footer)-len(lines)-len(m.feedbackLines())-trailing)
}

func (m *workspace) footerLines(footer string) []string {
	var lines []string
	line := ""
	width := m.bodyWidth()
	if !m.framed() {
		width = min(width+3, m.width-3) // The footer needs only its indent and the terminal's final column.
	}
	footer = strings.TrimSuffix(footer, " · q quit")
	if !strings.Contains(footer, "Ctrl+C") && !strings.Contains(footer, "Esc exit") {
		footer += " · Ctrl+C exit"
	}
	separator := m.muted().Render(" · ")
	keyStyle := m.accent()
	if m.page == "home" || m.page == "stores" || m.page == "filter" {
		keyStyle = keyStyle.Bold(m.color)
	}
	for _, hint := range strings.Split(footer, " · ") {
		key, description, _ := strings.Cut(hint, " ")
		item := keyStyle.Render(key) + m.muted().Render(" "+description)
		if line != "" && ansi.StringWidth(line)+3+ansi.StringWidth(item) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += separator
		}
		line += item
	}
	if m.fullScreen || m.width >= 40 && m.height >= 12 {
		label := " expand"
		if m.fullScreen {
			label = " compact"
		}
		hint := keyStyle.Render("Ctrl+G") + m.muted().Render(label)
		gap := width - ansi.StringWidth(line) - ansi.StringWidth(hint)
		if gap < 1 {
			lines = append(lines, line)
			return append(lines, strings.Repeat(" ", max(0, width-ansi.StringWidth(hint)))+hint)
		}
		line += strings.Repeat(" ", gap) + hint
	}
	return append(lines, line)
}

func (m *workspace) header() string {
	active := lipgloss.NewStyle().Bold(m.color)
	title := active.Render("Vector stores")
	if m.store != nil && m.page != "stores" && m.page != "filter" && m.page != "create" {
		name := m.store.Name
		if name == "" {
			name = m.store.ID
		}
		title = m.muted().Render("Vector stores / ") + active.Render(cleanLine(name))
	}
	count := ""
	switch m.page {
	case "stores", "filter":
		if !m.storesLoaded && len(m.stores) == 0 {
			break
		}
		if m.storeFinderQuery() == "" {
			count = fmt.Sprintf("%d loaded", len(m.stores))
		} else {
			matches := len(m.filteredStores())
			count = fmt.Sprintf("%d matches", matches)
			if matches == 1 {
				count = "1 match"
			}
			if m.storeNext != "" {
				count += fmt.Sprintf(" · %d loaded", len(m.stores))
			}
		}
	case "files", "batchfiles":
		if m.filesLoaded || len(m.files) > 0 {
			count = fmt.Sprintf("%d loaded", len(m.files))
		}
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
