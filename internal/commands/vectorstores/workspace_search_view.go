package vectorstores

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

func (m *workspace) searchFooter() string {
	switch m.focus {
	case matchListFocus:
		return "↑↓ result · Enter read · Shift+Tab actions · Esc edit query"
	case 0:
		if len(m.results) > 0 {
			return "Enter search · Tab actions · Shift+Tab last results · Esc stores"
		}
		return "Enter search · Tab actions · Esc stores"
	default:
		if m.focus == matchListFocus-1 {
			if len(m.results) > 0 {
				return "↑↓ choose · Tab last results · Enter select · Esc stores"
			}
			return "↑↓ choose · Tab query · Enter select · Esc stores"
		}
		return "↑↓ choose · Tab next action · Enter select · Esc stores"
	}
}

func (m *workspace) searchQueryLine() string {
	return m.muted().Render("Query  ") + cleanLine(m.submittedQuery)
}

func (m *workspace) searchLines(lines []string, footer string) []string {
	width, previewWidth := m.bodyWidth(), m.bodyWidth()
	if m.wideLayout() {
		width, previewWidth = m.paneWidths()
	}
	if m.focus != matchListFocus {
		actions := []string{"Search", "Upload file", "Files", "Switch store", "Manage store"}
		var empty []string
		if m.searched && len(m.results) == 0 && m.editor.Value() == m.submittedQuery {
			if m.moreResults {
				empty = []string{m.heading("No matches on this page."), m.muted().Render("More matches exist. Refine your query.")}
			} else {
				empty = []string{m.heading("No matches returned."), m.muted().Render("Indexing state was not checked.")}
			}
		}
		label, marker := m.heading("Search your files"), "  "
		if m.editing() {
			label = m.accent().Bold(m.color).Render("Search your files")
			marker = m.accent().Render("› ")
		}
		// Reserve the label, Actions heading, and one selectable action.
		reserved := 3
		if !m.wideLayout() {
			reserved += len(empty)
		}
		if available := m.listBudget(lines, footer, reserved); m.editor.Height() > available {
			m.editor.SetHeight(available)
		}
		left := []string{label}
		for _, row := range strings.Split(m.editorField(), "\n") {
			left = append(left, marker+row)
			marker = "  "
		}
		if !m.wideLayout() {
			left = append(left, empty...)
		}
		rows := m.listBudget(lines, footer, len(left)+1)
		if rows > len(actions) {
			left = append(left, "")
		}
		first := max(0, m.focus-rows)
		end := min(len(actions), first+rows)
		heading := m.heading("Actions")
		if end-first < len(actions) {
			heading += m.muted().Render(fmt.Sprintf("  %d–%d of %d", first+1, end, len(actions)))
		}
		left = append(left, heading)
		for i := first; i < end; i++ {
			left = append(left, m.rowWidth(actions[i], m.focus == i+1, width))
		}
		if m.wideLayout() {
			right := empty
			if m.store != nil {
				if len(right) > 0 {
					right = append(right, "")
				}
				right = append(right, m.storeInspector(m.store)...)
			}
			return m.appendPanes(lines, left, right, footer)
		}
		return append(lines, left...)
	}

	columns, items := m.resultTable(width)
	if m.wideLayout() {
		left := []string{m.searchQueryLine(), m.tableHeader(columns)}
		count := m.listBudget(lines, footer, len(left))
		if m.moreResults {
			count--
		}
		left = append(left, m.tableRows(columns, items, m.resultSelected, count, true)...)
		if m.moreResults {
			left = append(left, m.muted().Render("More matches exist. Refine your query."))
		}
		selected := m.results[m.resultSelected]
		source := selected.Filename
		if source == "" {
			source = selected.FileID
		}
		right := []string{m.muted().Render("Source  ") + m.heading(cleanLine(source))}
		if selected.FileID != "" && selected.FileID != source {
			right = append(right, m.muted().Render(cleanLine(selected.FileID)))
		}
		previewRows := min(6, m.listBudget(lines, footer, len(right)))
		right = append(right, m.matchExcerpt(previewWidth, previewRows)...)
		return m.appendPanes(lines, left, right, footer)
	}

	lines = append(lines, m.searchQueryLine(), m.divider(), m.tableHeader(columns))
	preview := m.matchExcerpt(previewWidth, 3)
	trailing := len(preview) + 1
	if m.moreResults {
		trailing++
	}
	lines = append(lines, m.tableRows(columns, items, m.resultSelected, m.listBudget(lines, footer, trailing), true)...)
	lines = append(lines, m.divider())
	lines = append(lines, preview...)
	if m.moreResults {
		lines = append(lines, m.muted().Render("More matches exist. Refine your query."))
	}
	return lines
}

// Only the preview is bounded. Enter opens every chunk in the scrollable passage.
func (m *workspace) matchExcerpt(width, rows int) []string {
	for _, chunk := range m.results[m.resultSelected].Content {
		text := strings.TrimSpace(chunk.Text)
		if text == "" {
			continue
		}
		limit := width * rows * utf8.UTFMax
		if len(text) > limit {
			for !utf8.RuneStart(text[limit]) {
				limit--
			}
			text = text[:limit] + "…"
		}
		text = strings.Join(strings.Fields(readable.Text(text)), " ")
		lines := strings.Split(ansi.Wrap(text, width, ""), "\n")
		if len(lines) > rows {
			lines = lines[:rows]
			lines[rows-1] = ansi.Truncate(lines[rows-1], width-1, "") + "…"
		}
		return lines
	}
	return []string{m.muted().Render("No passage text returned.")}
}
