package vectorstores

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

func (m *workspace) searchFooter() string {
	switch m.focus {
	case matchListFocus:
		return "↑↓ result · Enter read · Esc edit query"
	case 0:
		if len(m.results) > 0 {
			return "Enter search · Shift+Tab last results · ↓ actions · Esc stores"
		}
		return "Enter search · ↓ actions · Esc stores"
	default:
		return "←→ action · Enter select · ↑ search · Esc stores"
	}
}

func (m *workspace) searchQueryLine() string {
	return m.muted().Render("Query  ") + cleanLine(m.submittedQuery)
}

func (m *workspace) searchLines(lines []string, footer string) []string {
	if m.focus != matchListFocus {
		lines = append(lines, m.editorLines("Search your files")...)
		if m.searched && len(m.results) == 0 && m.editor.Value() == m.submittedQuery {
			if m.moreResults {
				lines = append(lines, m.heading("No matches on this page."), m.muted().Render("More matches exist. Refine your query."))
			} else {
				lines = append(lines, m.heading("No matches returned."), m.muted().Render("Indexing state was not checked."))
			}
		}
		lines = append(lines, m.divider())
		return append(lines, m.actions([]string{"Search", "Upload file", "Files", "Switch store", "Manage store"}, 1))
	}

	columns, items := m.resultTable()
	lines = append(lines, m.searchQueryLine(), m.divider(), m.tableHeader(columns))
	preview := m.matchExcerpt()
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
func (m *workspace) matchExcerpt() []string {
	const rows = 3
	width := m.bodyWidth()
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
