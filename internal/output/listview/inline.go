package listview

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/terminal"
)

// Inline output owns a table layout and unread lines of the current API page.
type inlineList struct {
	table printedTable
	lines []string
}

// The full-screen program has restored the terminal. Commit its cache once in
// fetch order, irrespective of Back navigation, and release the viewer history.
func (m *Model) printLoaded() error {
	f := m.records
	m.records = recordViewer{mode: recordInline}
	opts := m.opts
	opts.PrintOnly = true
	out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
	if len(f.pages) == 0 {
		return m.inline.table.writePage(out, opts, Page{Notice: f.emptyMessage, More: m.more}, m.window.Width, m.err)
	}
	start := 0
	for i, page := range f.pages {
		if err := opts.Context.Err(); err != nil {
			return err
		}
		end := page.end
		received := Page{Items: f.items[start:end], FullLabels: page.fullLabels, More: i+1 < len(f.pages) || m.more}
		if err := m.inline.table.writePage(out, opts, received, m.window.Width, m.err); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func (m *Model) receiveInline(page Page) tea.Cmd {
	var content strings.Builder
	opts := m.opts
	opts.PrintOnly = true
	err := m.inline.table.writePage(&content, opts, page, m.window.Width, m.err)
	m.err = errors.Join(m.err, err)
	if m.err != nil {
		// Finish preserves received rows after restoration, including a partial final page.
		m.pending = []string{content.String()}
		return m.quit()
	}
	if content.Len() == 0 {
		if !m.more {
			return m.quit()
		}
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(content.String(), "\n"), "\n")
	// A final detail hint belongs with the last record, never on its own Space.
	if n := len(lines); n > 1 && (lines[n-1] == opts.Hint || lines[n-1] == page.Notice) && lines[n-1] != "" {
		lines[n-2] += "\n" + lines[n-1]
		lines = lines[:n-1]
	}
	m.inline.lines = lines
	return m.advanceInline()
}

// Each Space consumes cached output or starts one request. Only Run writes
// committed output, after the prompt releases the normal terminal screen.
func (m *Model) advanceInline() tea.Cmd {
	lines := m.inline.lines
	if len(lines) == 0 {
		if m.more {
			return m.load()
		}
		return m.quit()
	}
	budget, width := max(1, m.window.Height-1), max(1, m.window.Width)
	end, rows, hasText := 0, 0, false
	for end < len(lines) {
		// Preserve complete logical lines, including IDs wider than a terminal.
		line := lines[end]
		height := inlineRows(line, width, budget-rows)
		if hasText && rows+height > budget {
			break
		}
		hasText = hasText || strings.TrimSpace(line) != ""
		rows += height
		end++
	}
	m.pending = []string{strings.Join(lines[:end], "\n") + "\n"}
	m.inline.lines = nil
	if end < len(lines) {
		m.inline.lines = lines[end:]
	}
	return tea.Quit
}

// Count only through the viewport. A long unbroken value still prints in full.
func inlineRows(line string, width, limit int) int {
	rows, col := 1, 0
	for line != "" && rows <= limit {
		cluster, cells, n := terminal.Sequence(line)
		line = line[n:]
		switch cluster {
		case "\n":
			rows, col = rows+1, 0
		case "\t":
			col = min(width-1, (col/8+1)*8)
		default:
			if cells > 0 && col+cells > width {
				rows, col = rows+1, 0
			}
			col += cells
		}
	}
	return rows
}

func (m *Model) inlinePrompt() string {
	prompt := "Space: more   q: quit"
	if m.loading {
		prompt = "Loading…   q: quit"
	}
	width := m.recordWidth()
	if ansi.StringWidth(prompt) > width {
		prompt = "Space q"
		if m.loading || width < len(prompt) {
			prompt = "q"
		}
	}
	return m.opts.Style.controls(prompt)
}
