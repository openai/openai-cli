package listview

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/tidwall/gjson"
)

// A position identifies a record and, in labeled output, a wrapped line in it.
type recordPosition struct{ item, line int }

type recordRange struct {
	from, end recordPosition
	labeled   bool
}

type recordScreen struct {
	recordRange
	rows    []recordPosition
	content string
	full    bool
}

type recordPage struct {
	end        int
	fullLabels bool
}

type recordMode uint8

const (
	recordPreview recordMode = iota
	recordOpening
	recordBrowser
)

type recordViewer struct {
	mode                  recordMode
	emptyMessage          string
	labelItem, labelWidth int
	wrappedLabels         []string       // at most one record, including very long unfamiliar fields
	wasLabeled            map[int]bool   // last displayed layout, for one-row movement across a table/label boundary
	firstScreenEnd        recordPosition // the first response may be smaller than a full screen
	items                 []gjson.Result
	pages                 []recordPage
	screen                recordScreen
	history               []recordRange
	from                  recordPosition
	endLimit              recordPosition // exclusive boundary when restoring a previous screen
	rowLimit              int            // retain the current screen height when moving one row
	restoreLabels         bool           // restore a labeled screen exactly when paging back
	fill                  bool           // only a navigation key can request more records
}

func (m *Model) receiveRecords(page Page) tea.Cmd {
	f := &m.records
	first := len(f.pages) == 0
	if page.Notice != "" {
		if len(page.Items) == 0 {
			f.emptyMessage = page.Notice
		} else {
			// A preserved response envelope is one response, even if its data
			// field contains many items. Its record count is unknown.
			m.opts.Singular, m.opts.Plural = "Response", "Responses"
			m.opts.Hint = page.Notice
		}
	}
	if len(page.Items) != 0 {
		f.items = append(f.items, page.Items...)
		f.pages = append(f.pages, recordPage{end: len(f.items), fullLabels: page.FullLabels})
	}
	cmd := m.refreshRecords()
	if first && cmd == nil && m.err == nil && !m.more && f.screen.end.item == len(f.items) {
		// A complete result that fits in the preview belongs in scrollback.
		// Finish writes it once after Tea releases the terminal.
		body := f.screen.content
		if len(f.items) == 0 {
			body = f.emptyMessage
			if body == "" {
				body = "No results."
			}
		}
		if len(f.items) != 0 && (page.Notice != "" || m.showRecordHint()) {
			body += "\n" + m.opts.Style.Note(m.opts.Hint)
		}
		m.pending = []string{body + "\n"}
		return m.quit()
	}
	return cmd
}

// fullRecordLabels preserves the source page's fallback even when an individual
// record could otherwise fit in a smaller table.
func (v *recordViewer) fullRecordLabels(from, end int) bool {
	page := sort.Search(len(v.pages), func(i int) bool { return v.pages[i].end > from })
	for ; page < len(v.pages); page++ {
		if v.pages[page].fullLabels {
			return true
		}
		if v.pages[page].end >= end {
			break
		}
	}
	return false
}

func (m *Model) resizeRecords(widthChanged bool) tea.Cmd {
	f := &m.records
	f.history = nil
	f.rowLimit, f.endLimit, f.fill, f.restoreLabels = 0, recordPosition{}, false, false
	// Anchor reflow to the record. A wrapped line can move when the width changes.
	if widthChanged {
		f.from.line = 0
		f.wasLabeled = nil
	}
	return m.refreshRecords()
}

func (m *Model) refreshRecords() tea.Cmd {
	f := &m.records
	if m.err == nil && f.from.item < len(f.items) {
		var next recordScreen
		next, m.err = m.renderRecords()
		if m.err == nil {
			if len(f.screen.rows) == 0 {
				f.firstScreenEnd = next.end
			}
			f.screen = next
			if f.wasLabeled == nil {
				f.wasLabeled = make(map[int]bool)
			}
			for _, row := range next.rows {
				f.wasLabeled[row.item] = next.labeled
			}
		}
	}
	if m.err != nil {
		// Print each original API page once, after terminal restoration. Successful
		// navigation only renders the visible records, regardless of API page size.
		start := 0
		for _, page := range f.pages {
			end := page.end
			received := Page{Items: f.items[start:end], FullLabels: page.fullLabels}
			content, err := printablePage(m.opts, received, max(1, m.window.Width))
			if err != nil {
				m.err = errors.Join(m.err, err)
				break
			}
			m.pending = append(m.pending, content)
			start = end
		}
		return m.quit()
	}
	// Keep the old screen visible while waiting for the first new row. Only
	// continue a requested move until its screen fits or the API reaches EOF.
	if !m.loading && m.more && f.fill && (f.from.item >= len(f.items) ||
		(!f.screen.full && f.screen.end.item == len(f.items))) {
		return m.load()
	}
	if !m.loading {
		f.fill = false
		if f.from.item >= len(f.items) {
			f.from = f.screen.from
			if n := len(f.history); n > 0 && f.history[n-1].from == f.from {
				f.history = f.history[:n-1]
			}
		}
	}
	return nil
}

func (m *Model) renderRecords() (recordScreen, error) {
	f := &m.records
	width, height := m.recordWidth(), m.recordHeight()
	s := recordScreen{recordRange: recordRange{from: f.from}}
	// A regular screen has one pinned table header and one footer. A two-row
	// terminal can still display one complete record row above the footer.
	pinned := height > 1
	capacity := max(1, height-1)
	if f.rowLimit > 0 {
		capacity = min(capacity, f.rowLimit)
	}
	end := min(len(f.items), f.from.item+capacity)
	if f.endLimit != (recordPosition{}) {
		end = min(end, f.endLimit.item)
	}
	if !f.restoreLabels && f.from.line == 0 && f.endLimit.line == 0 && end > f.from.item {
		table, fits, err := m.recordTable(f.from.item, end, width)
		if err != nil {
			return s, err
		}
		if fits {
			if !pinned {
				_, table, _ = strings.Cut(table, "\n")
			}
			s.content = strings.TrimSuffix(table, "\n")
			s.end = recordPosition{item: end}
			for i := f.from.item; i < end; i++ {
				s.rows = append(s.rows, recordPosition{item: i})
			}
			s.full = len(s.rows) == capacity || s.end == f.endLimit
			return s, nil
		}
	}
	// A narrow terminal or an unfamiliar record uses the original labels.
	// Retain wrapped line positions so even a record taller than the screen
	// is completely reachable.
	if f.rowLimit > 0 {
		height = min(height, f.rowLimit)
	}
	s.labeled = true
	var body []string
	pos := f.from
	bounded := f.endLimit != (recordPosition{})
	for pos.item < len(f.items) && len(body) < height && (!bounded || pos != f.endLimit) {
		lines, err := m.recordLabelLines(pos.item, width)
		if err != nil {
			return s, err
		}
		pos.line = min(pos.line, len(lines)-1)
		for pos.line < len(lines) && len(body) < height && (!bounded || pos != f.endLimit) {
			body = append(body, lines[pos.line])
			s.rows = append(s.rows, pos)
			pos.line++
		}
		if pos.line == len(lines) {
			pos = recordPosition{item: pos.item + 1}
		}
	}
	s.content, s.end = strings.Join(body, "\n"), pos
	s.full = len(body) == height || pos == f.endLimit
	return s, nil
}

func (m *Model) recordLabelLines(item, width int) ([]string, error) {
	f := &m.records
	if err := m.opts.Context.Err(); err != nil {
		return nil, err
	}
	if f.labelItem == item && f.labelWidth == width && f.wrappedLabels != nil {
		return f.wrappedLabels, nil
	}
	text, err := m.opts.RenderRecordLabels(f.items[item], f.fullRecordLabels(item, item+1))
	if err != nil {
		return nil, err
	}
	text = strings.ReplaceAll(strings.TrimSuffix(text, "\n"), "\t", "    ")
	f.wrappedLabels = strings.Split(ansi.Hardwrap(text, width, true), "\n")
	f.labelItem, f.labelWidth = item, width
	return f.wrappedLabels, m.opts.Context.Err()
}

var recordKeys = viewport.DefaultKeyMap()

func (m *Model) recordKey(msg tea.KeyPressMsg) tea.Cmd {
	f := &m.records
	s := &f.screen
	if m.loading || len(s.rows) == 0 {
		return nil
	}
	var forward, page bool
	step := 1
	switch {
	case key.Matches(msg, recordKeys.PageDown):
		forward, page = true, true
	case key.Matches(msg, recordKeys.PageUp):
		page = true
	case key.Matches(msg, recordKeys.Down):
		forward = true
	case key.Matches(msg, recordKeys.Up):
	case key.Matches(msg, recordKeys.HalfPageDown):
		forward, step = true, max(1, len(s.rows)/2)
	case key.Matches(msg, recordKeys.HalfPageUp):
		step = max(1, len(s.rows)/2)
	default:
		return nil
	}
	if forward {
		return m.advanceRecords(page, step)
	}
	return m.backRecords(page, step)
}

func (m *Model) advanceRecords(page bool, step int) tea.Cmd {
	f, s := &m.records, &m.records.screen
	if s.end.item == len(f.items) && !m.more {
		return nil
	}
	if page && f.mode == recordPreview {
		// End the inline renderer before saving the main screen. Switching
		// buffers in the same render would restore a stale preview on quit.
		f.mode = recordOpening
		return tea.Quit
	}
	f.endLimit, f.fill, f.restoreLabels = recordPosition{}, true, false
	if page {
		f.history = append(f.history, s.recordRange)
		f.from, f.rowLimit = s.end, 0
	} else {
		f.history = nil
		f.from, f.rowLimit = s.end, len(s.rows)
		if step < len(s.rows) {
			f.from = s.rows[step]
		}
	}
	return m.refreshRecords()
}

func (m *Model) backRecords(page bool, step int) tea.Cmd {
	f, s := &m.records, &m.records.screen
	if s.from == (recordPosition{}) {
		return nil
	}
	f.endLimit, f.fill, f.rowLimit, f.restoreLabels = recordPosition{}, false, 0, false
	if page && len(f.history) != 0 {
		last := f.history[len(f.history)-1]
		f.history = f.history[:len(f.history)-1]
		f.from, f.endLimit, f.restoreLabels = last.from, last.end, last.labeled
		return m.refreshRecords()
	}
	f.history = nil
	if page {
		step = m.recordHeight()
		if !s.labeled {
			step = max(1, step-1)
		}
		f.endLimit = s.from
	} else {
		f.rowLimit = len(s.rows)
	}
	for ; step > 0 && f.from != (recordPosition{}); step-- {
		if f.from.line > 0 {
			f.from.line--
			continue
		}
		f.from.item--
		width := m.recordWidth()
		// A table-eligible record can have appeared in a labeled screen beside
		// an unfamiliar record. Step back into its actual previous layout.
		labeled, seen := f.wasLabeled[f.from.item]
		if !seen {
			_, fits, err := m.recordTable(f.from.item, f.from.item+1, width)
			if err != nil {
				m.err = err
				return m.refreshRecords()
			}
			labeled = !fits
		}
		if !labeled {
			continue
		}
		lines, err := m.recordLabelLines(f.from.item, width)
		if err != nil {
			m.err = err
			return m.refreshRecords()
		}
		f.from.line = len(lines) - 1
	}
	if page && step > 0 && s.from != f.firstScreenEnd {
		// Clamp near the top, except when returning to a short initial screen.
		f.endLimit = recordPosition{}
	}
	return m.refreshRecords()
}

func (m *Model) recordTable(from, end, width int) (string, bool, error) {
	if m.records.fullRecordLabels(from, end) {
		return "", false, nil
	}
	return m.opts.RenderTable(m.records.items[from:end], width)
}

// Keep optional detail guidance below the records, so it cannot add a paging step.
func (m *Model) showRecordHint() bool {
	return m.opts.Hint != "" && m.window.Height >= 4 && ansi.StringWidth(m.opts.Hint) <= m.recordWidth()
}

// Keep wrapped line offsets stable when the inline frame (which reserves
// one right-edge cell) hands the first unread line to the full-screen browser.
func (m *Model) recordWidth() int {
	return max(1, m.window.Width-1)
}

func (m *Model) recordHeight() int {
	height := m.window.Height - 1
	if m.showRecordHint() {
		height--
	}
	if m.records.mode != recordBrowser {
		// Reserve a header plus five data rows for tables. Labeled records
		// use those same six physical lines, even for a single long value.
		height = min(height, 6)
	}
	return max(1, height)
}

func (m *Model) recordView() string {
	f := &m.records
	width := m.recordWidth()
	position := m.opts.Plural
	if len(f.screen.rows) != 0 {
		first := f.screen.from.item + 1
		last := f.screen.rows[len(f.screen.rows)-1].item + 1
		position = fmt.Sprintf("%s %d", m.opts.Singular, first)
		if first != last {
			position = fmt.Sprintf("%s %d–%d", m.opts.Plural, first, last)
		}
		if m.total > 0 {
			count := fmt.Sprintf(" of %d", m.total)
			if ansi.StringWidth(position+count+"  q") <= width {
				position += count
			}
		}
	} else if m.loaded {
		position = m.opts.Plural + " 0"
	}
	controls := "↑↓: row   Space: next screen   b: back   q: quit"
	medium, short := "Space: next  b: back  q: quit", "↑↓ Space b q"
	if f.mode == recordPreview {
		controls, medium, short = "↑↓: row   Space: browse   b: back   q: quit", "Space: browse  b: back  q: quit", "Space: browse  q"
	}
	if m.loading || !m.loaded {
		controls, medium, short = "Loading…   q: quit", "Loading…  q: quit", "Loading… q"
	} else if !m.more && f.screen.end.item == len(f.items) {
		controls, medium, short = "End   ↑↓: row   b: back   q: quit", "End  b: back  q: quit", "End ↑↓ b q"
	}
	padding := "    "
	if ansi.StringWidth(position+padding+controls) > width {
		controls, padding = medium, "  "
	}
	if ansi.StringWidth(position+padding+controls) > width {
		controls = short
	}
	if ansi.StringWidth(position+padding+controls) > width {
		position = strings.TrimPrefix(strings.TrimPrefix(position, m.opts.Plural+" "), m.opts.Singular+" ")
	}
	if ansi.StringWidth(position+padding+controls) > width {
		controls = "q"
	}
	footer := "q"
	if width > 2 {
		position = ansi.Truncate(position, width-ansi.StringWidth(padding+controls), "")
		footer = strings.TrimSpace(position + padding + m.opts.Style.controls(controls))
	}
	if m.window.Height <= 1 {
		return footer
	}
	if f.screen.content != "" {
		if m.showRecordHint() {
			footer = m.opts.Style.Note(m.opts.Hint) + "\n" + footer
		}
		return f.screen.content + "\n" + footer
	}
	if m.loaded && !m.loading {
		message := f.emptyMessage
		if message == "" {
			message = "No results."
		}
		lines := strings.Split(ansi.Hardwrap(message, width, true), "\n")
		return strings.Join(lines[:min(len(lines), max(1, m.window.Height-1))], "\n") + "\n" + footer
	}
	return footer
}
