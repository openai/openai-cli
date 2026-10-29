package batches

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Requests retain only the visible catalog page and the selected match position.
// Catalog scans run outside the terminal event loop and stop when replaced.
type batchBrowserRequestState struct {
	ctx          context.Context
	catalog      *batchBrowserCatalog
	cancel       context.CancelFunc
	sequence     uint64
	page         batchBrowserRecordPage
	selected     int64
	loading      bool
	loaded       bool
	err          error
	expanded     bool
	autoExpanded bool
	offerCSV     bool
	preview      batchBrowserInlinePreview
}

func (s *batchBrowserRequestState) stop() {
	s.preview.stop()
	if s.cancel != nil {
		s.cancel()
	}
	s.sequence++
}

func (m *batchBrowserScreen) requestKeys(shortcut func(string, string) string) []string {
	var keys []string
	if m.requestState.autoExpanded && m.requestState.expanded {
		keys = []string{shortcut("Enter", "collapse"), shortcut("→", "full record"), shortcut("Ctrl+V", "view all")}
	} else if m.requestState.expanded {
		keys = []string{shortcut("↑↓", "select"), shortcut("→", "full record"), shortcut("Ctrl+V", "view all")}
	} else {
		keys = []string{shortcut("↑↓", "select"), shortcut("Enter", "expand"), shortcut("→", "full record"), shortcut("Ctrl+V", "view all")}
	}
	if m.requestState.offerCSV {
		keys = append(keys, shortcut("Ctrl+O", "original CSV"))
	}
	if m.requestState.expanded && !m.requestState.autoExpanded {
		return append(keys, shortcut("Esc", "collapse"))
	}
	return append(keys, shortcut("Esc", "back"))
}

type batchBrowserRequestsReady struct {
	screen   *batchBrowserScreen
	sequence uint64
	page     batchBrowserRecordPage
	end      bool
	err      error
}

func newBatchBrowserRequests(ctx context.Context, catalog *batchBrowserCatalog, title, batchID string) *batchBrowserScreen {
	m := newBatchBrowserScreen("requests", title, batchID, nil)
	m.input.Placeholder = "Find ID or text"
	onlyError := catalog.selection == "error" && catalog.store.recordCount == 1
	m.requestState = &batchBrowserRequestState{ctx: ctx, catalog: catalog, expanded: onlyError, autoExpanded: onlyError}
	return m
}

func (m *batchBrowserScreen) requestRows() int {
	width, height := max(1, m.width), max(1, m.height)
	if width >= 50 && height >= 12 {
		width, height = width-2, height-2
	}
	heading, footer := 3, 1
	if height >= 10 {
		heading += 2 // Divider and column headings.
		footer = 2   // Blank separator and first shortcut line.
		used := 2
		for _, key := range m.requestKeys(func(key, label string) string { return key + " " + label }) {
			gap := 0
			if used > 2 {
				gap = 2
			}
			if used+gap+ansi.StringWidth(key) > width {
				footer++
				used, gap = 2, 0
			}
			used += gap + ansi.StringWidth(key)
		}
	}
	return max(1, height-heading-footer)
}

func (m *batchBrowserScreen) requestFirst() int64 {
	s := m.requestState
	first := s.page.First
	if s.selected < first || s.selected >= first+int64(m.requestRows()) {
		first = s.selected
	}
	return first
}

func (m *batchBrowserScreen) loadRequests(first int64, end bool) tea.Cmd {
	s := m.requestState
	s.stop()
	sequence := s.sequence
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel, s.loading, s.err = cancel, true, nil
	catalog, query, rows := s.catalog, m.input.Value(), m.requestRows()
	return func() tea.Msg {
		page, err := catalog.page(ctx, query, first, rows)
		return batchBrowserRequestsReady{m, sequence, page, end, err}
	}
}

func (m *batchBrowserScreen) updateRequests(message tea.Msg) (tea.Model, tea.Cmd) {
	s := m.requestState
	switch msg := message.(type) {
	case batchBrowserPreviewReady:
		if msg.screen == m && msg.sequence == s.preview.sequence && !m.closed {
			s.preview.lines = msg.lines
			s.preview.loading = false
			if msg.err != nil {
				s.err, m.closed = msg.err, true
				return m, tea.Quit
			}
		}
		return m, nil
	case batchBrowserRequestsReady:
		if msg.screen != m || msg.sequence != s.sequence || m.closed {
			return m, nil
		}
		s.loading = false
		if msg.err != nil {
			s.err, m.closed = msg.err, true
			return m, tea.Quit
		}
		s.page = msg.page
		s.loaded = true
		if msg.end {
			s.selected = max(0, s.page.Total-1)
		}
		s.selected = max(s.page.First, min(s.selected, s.page.First+int64(len(s.page.Rows))-1))
		if len(s.page.Rows) == 0 {
			s.selected, m.searchFocused = 0, true
		}
		return m, m.loadRequestPreview()
	case tea.WindowSizeMsg:
		rows := m.requestRows()
		m.width, m.height = msg.Width, msg.Height
		if rows == m.requestRows() && s.loaded && !s.loading {
			return m, nil
		}
		return m, m.loadRequests(m.requestFirst(), false)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+o":
			if s.offerCSV {
				s.stop()
				m.closed, m.chosen = true, "original-csv"
				return m, tea.Quit
			}
			return m, nil
		case "esc", "ctrl+c", "ctrl+v":
			if msg.String() == "esc" && s.expanded && !s.autoExpanded {
				s.expanded = false
				s.preview.stop()
				return m, nil
			}
			s.stop()
			m.closed, m.chosen = true, "back"
			if msg.String() == "ctrl+c" {
				m.chosen = ""
			} else if msg.String() == "ctrl+v" {
				m.chosen = "all"
			}
			return m, tea.Quit
		case "right":
			if m.searchFocused {
				break // Right edits the search query.
			}
			fallthrough
		case "enter":
			if m.width < 30 || m.height < 7 || s.loading {
				return m, nil
			}
			index := s.selected - s.page.First
			if index >= 0 && index < int64(len(s.page.Rows)) {
				if msg.String() == "enter" {
					m.searchFocused = false
					s.autoExpanded = false
					s.expanded = !s.expanded
					if !s.expanded || m.requestPreviewRows() > 0 {
						return m, m.loadRequestPreview()
					}
					s.expanded = false
				}
				s.stop()
				m.closed, m.chosen = true, "record:"+strconv.FormatInt(s.page.Rows[index].ID, 10)
				return m, tea.Quit
			}
			return m, nil
		case "up", "shift+tab":
			if m.searchFocused || s.selected == 0 {
				m.searchFocused = true
				s.expanded = false
				s.preview.stop()
				return m, nil
			}
			if !s.loading {
				return m, m.selectRequest(s.selected - 1)
			}
			return m, nil
		case "down", "tab":
			if m.searchFocused {
				if !s.loading && len(s.page.Rows) > 0 {
					m.searchFocused = false
				}
				return m, nil
			}
			if !s.loading {
				return m, m.selectRequest(s.selected + 1)
			}
			return m, nil
		case "pgup", "pgdown":
			if !s.loading {
				delta := int64(m.requestRows() - m.requestPreviewRows())
				if msg.String() == "pgup" {
					delta = -delta
				}
				m.searchFocused = false
				return m, m.selectRequest(s.selected + delta)
			}
			return m, nil
		case "home":
			if !m.searchFocused && !s.loading {
				return m, m.selectRequest(0)
			}
		case "end":
			if !m.searchFocused && !s.loading {
				return m, m.loadRequests(math.MaxInt64, true)
			}
		}
	}
	previous, position := m.input.Value(), m.input.Position()
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	if m.input.Value() != previous || m.input.Position() != position {
		m.searchFocused = true
		s.expanded = false
		s.preview.stop()
	}
	if m.input.Value() != previous {
		s.selected = 0
		s.loaded = false
		s.page = batchBrowserRecordPage{}
		return m, m.loadRequests(0, false)
	}
	return m, command
}

func (m *batchBrowserScreen) selectRequest(selected int64) tea.Cmd {
	s := m.requestState
	selected = max(0, min(selected, s.page.Total-1))
	if selected == s.selected {
		return nil
	}
	s.selected = selected
	if selected < s.page.First || selected >= s.page.First+int64(len(s.page.Rows)) {
		return m.loadRequests(selected, false)
	}
	return m.loadRequestPreview()
}

func (m *batchBrowserScreen) requestLines(height, width int, footer []string, rule func() string, muted, accent, highlight lipgloss.Style, semantic func(lipgloss.Style, string) lipgloss.Style, colors bool) ([]string, *tea.Cursor) {
	s := m.requestState
	clip := func(text string, width int) string { return ansi.Truncate(batchInputLabel(text), max(1, width), "…") }
	heading := lipgloss.NewStyle().Bold(colors)
	lines := []string{"  " + heading.Render(clip(m.title, width)), "  " + muted.Render(clip(m.subtitle, width))}
	count := fmt.Sprintf("%d requests", s.page.Total)
	if s.page.Total == 1 {
		count = "1 request"
	}
	if m.input.Value() != "" {
		count = fmt.Sprintf("%d matches", s.page.Total)
		if s.page.Total == 1 {
			count = "1 match"
		}
	}
	if s.loading {
		count = "Searching…"
	}
	searchWidth := width - ansi.StringWidth(count) - 2
	if searchWidth < 18 {
		searchWidth, count = width, ""
	}
	query, x := m.searchView(searchWidth - 8)
	if m.input.Value() == "" {
		query = muted.Render(query)
	}
	search := "Search  " + query
	if count != "" {
		search += strings.Repeat(" ", max(0, width-ansi.StringWidth(search)-ansi.StringWidth(count))) + muted.Bold(colors).Render(count)
	}
	lines = append(lines, "  "+search)
	var cursor *tea.Cursor
	if m.searchFocused {
		cursor = tea.NewCursor(10+x, 2)
		cursor.Shape, cursor.Blink = tea.CursorBar, true
	}
	if height < 10 {
		shortcuts := "↑↓ Enter →full Ctrl+V Esc"
		if s.offerCSV {
			shortcuts = "↑↓ Enter → Ctrl+V ^O CSV Esc"
		}
		footer = []string{"  " + accent.Render(shortcuts)}
	}
	numberWidth := 1
	for _, row := range s.page.Rows {
		numberWidth = max(numberWidth, len(strconv.FormatInt(row.ID, 10)))
	}
	idWidth := min(32, max(10, (width-numberWidth-4)/2))
	previewWidth := max(0, width-numberWidth-idWidth-4)
	table := width >= 45
	cell := func(text string, size int) string {
		text = clip(text, size)
		return text + strings.Repeat(" ", max(0, size-ansi.StringWidth(text)))
	}
	if height >= 10 {
		lines = append(lines, rule())
		labels := cell("#", numberWidth) + "  Request"
		if table {
			previewLabel := "Result"
			if s.catalog.selection == "input" {
				previewLabel = "Input"
			} else if s.catalog.selection == "error" {
				previewLabel = "Error"
			}
			labels = cell("#", numberWidth) + "  " + cell("Request", idWidth) + "  " + previewLabel
		}
		lines = append(lines, "  "+muted.Render(labels))
	}
	budget := max(1, height-len(lines)-len(footer))
	if s.loading {
		lines = append(lines, "  "+muted.Render("Searching requests…"))
	} else if len(s.page.Rows) == 0 {
		notice := "No requests in this file."
		if m.input.Value() != "" {
			notice = "No matching requests."
		}
		lines = append(lines, "  "+muted.Render(clip(notice, width)))
	} else {
		budget = max(1, budget-m.requestPreviewRows())
		offset := int(s.selected - s.page.First)
		first := max(0, offset-budget+1)
		for i := first; i < min(len(s.page.Rows), first+budget); i++ {
			row := s.page.Rows[i]
			selected := !m.searchFocused && s.selected == s.page.First+int64(i)
			prefix := cell(strconv.FormatInt(row.ID, 10), numberWidth) + "  "
			name, preview := clip(row.Label, width-ansi.StringWidth(prefix)), ""
			if table {
				name = cell(row.Label, idWidth)
				preview = "  " + clip(row.Preview, previewWidth)
			}
			if selected {
				text := prefix + name + preview
				rowWidth := width
				if m.width >= 50 && m.height >= 12 {
					rowWidth++ // Retain selection through the frame's right content gutter.
				}
				padding := strings.Repeat(" ", max(0, rowWidth-ansi.StringWidth(text)))
				lines = append(lines, highlight.Render("> "+prefix)+highlight.Bold(colors).Render(name)+highlight.Render(preview+padding))
				if s.expanded {
					shownWidth := 0
					if table {
						shownWidth = previewWidth
					}
					lines = append(lines, m.requestPreviewLines(row, width, shownWidth, muted, semantic)...)
				}
			} else {
				lines = append(lines, "  "+muted.Render(prefix)+accent.Render(name)+preview)
			}
		}
	}
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	return append(lines, footer...), cursor
}
