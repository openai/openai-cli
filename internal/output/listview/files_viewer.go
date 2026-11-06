package listview

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/tidwall/gjson"
)

// A position identifies a file and, in labeled output, a wrapped line in it.
type filePosition struct{ item, line int }

type fileRange struct {
	from, end filePosition
	labeled   bool
}

type fileScreen struct {
	fileRange
	rows    []filePosition
	content string
	full    bool
}

type filesViewer struct {
	labelItem, labelWidth int
	wrappedLabels         []string     // at most one file, including very long unfamiliar fields
	wasLabeled            map[int]bool // last displayed layout, for one-row movement across a table/label boundary
	firstScreenEnd        filePosition // the first response may be smaller than a full screen
	items                 []gjson.Result
	pageEnds              []int
	screen                fileScreen
	history               []fileRange
	from                  filePosition
	endLimit              filePosition // exclusive boundary when restoring a previous screen
	rowLimit              int          // retain the current screen height when moving one row
	restoreLabels         bool         // restore a labeled screen exactly when paging back
	fill                  bool         // only a navigation key can request more records
}

func (m *Model) receiveFiles(items []gjson.Result) tea.Cmd {
	f := m.files
	if len(items) != 0 {
		f.items = append(f.items, items...)
		f.pageEnds = append(f.pageEnds, len(f.items))
	}
	return m.refreshFiles()
}

func (m *Model) resizeFiles(widthChanged bool) tea.Cmd {
	f := m.files
	f.history = nil
	f.rowLimit, f.endLimit, f.fill, f.restoreLabels = 0, filePosition{}, false, false
	// Anchor reflow to the file. A wrapped line can move when the width changes.
	if widthChanged {
		f.from.line = 0
		f.wasLabeled = nil
	}
	return m.refreshFiles()
}

func (m *Model) refreshFiles() tea.Cmd {
	f := m.files
	if m.err == nil && f.from.item < len(f.items) {
		var next fileScreen
		next, m.err = m.renderFiles()
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
		// navigation only renders the visible files, regardless of API page size.
		start := 0
		for _, end := range f.pageEnds {
			content, err := m.opts.RenderPage(f.items[start:end], max(1, m.window.Width))
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

func (m *Model) renderFiles() (fileScreen, error) {
	f := m.files
	width, height := max(1, m.window.Width), max(1, m.window.Height-1)
	s := fileScreen{fileRange: fileRange{from: f.from}}
	// A regular screen has one pinned table header and one footer. A two-row
	// terminal can still display one complete file row above the footer.
	pinned := height > 1
	capacity := max(1, height-1)
	if f.rowLimit > 0 {
		capacity = min(capacity, f.rowLimit)
	}
	end := min(len(f.items), f.from.item+capacity)
	if f.endLimit != (filePosition{}) {
		end = min(end, f.endLimit.item)
	}
	if !f.restoreLabels && f.from.line == 0 && f.endLimit.line == 0 && end > f.from.item {
		table, fits, err := m.fileTable(f.from.item, end, width)
		if err != nil {
			return s, err
		}
		if fits {
			if !pinned {
				_, table, _ = strings.Cut(table, "\n")
			}
			s.content = strings.TrimSuffix(table, "\n")
			s.end = filePosition{item: end}
			for i := f.from.item; i < end; i++ {
				s.rows = append(s.rows, filePosition{item: i})
			}
			s.full = len(s.rows) == capacity || s.end == f.endLimit
			return s, nil
		}
	}
	// A narrow terminal or an unfamiliar record uses the original labels.
	// Retain wrapped line positions so even a file taller than the screen
	// is completely reachable.
	if f.rowLimit > 0 {
		height = min(height, f.rowLimit)
	}
	s.labeled = true
	var body []string
	pos := f.from
	bounded := f.endLimit != (filePosition{})
	for pos.item < len(f.items) && len(body) < height && (!bounded || pos != f.endLimit) {
		lines, err := m.fileLabelLines(pos.item, width)
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
			pos = filePosition{item: pos.item + 1}
		}
	}
	s.content, s.end = strings.Join(body, "\n"), pos
	s.full = len(body) == height || pos == f.endLimit
	return s, nil
}

func (m *Model) fileLabelLines(item, width int) ([]string, error) {
	f := m.files
	if err := m.opts.Context.Err(); err != nil {
		return nil, err
	}
	if f.labelItem == item && f.labelWidth == width && f.wrappedLabels != nil {
		return f.wrappedLabels, nil
	}
	text, err := m.opts.RenderFileLabels(f.items[item])
	if err != nil {
		return nil, err
	}
	text = strings.ReplaceAll(strings.TrimSuffix(text, "\n"), "\t", "    ")
	f.wrappedLabels = strings.Split(ansi.Hardwrap(text, width, true), "\n")
	f.labelItem, f.labelWidth = item, width
	return f.wrappedLabels, m.opts.Context.Err()
}

func (m *Model) filesKey(msg tea.KeyPressMsg) tea.Cmd {
	f := m.files
	s := &f.screen
	if m.loading || len(s.rows) == 0 {
		return nil
	}
	keys := m.viewer.viewport.KeyMap
	var forward, page bool
	step := 1
	switch {
	case key.Matches(msg, keys.PageDown):
		forward, page = true, true
	case key.Matches(msg, keys.PageUp):
		page = true
	case key.Matches(msg, keys.Down):
		forward = true
	case key.Matches(msg, keys.Up):
	case key.Matches(msg, keys.HalfPageDown):
		forward, step = true, max(1, len(s.rows)/2)
	case key.Matches(msg, keys.HalfPageUp):
		step = max(1, len(s.rows)/2)
	default:
		return nil
	}
	if forward {
		return m.advanceFiles(page, step)
	}
	return m.backFiles(page, step)
}

func (m *Model) advanceFiles(page bool, step int) tea.Cmd {
	f, s := m.files, &m.files.screen
	if s.end.item == len(f.items) && !m.more {
		return nil
	}
	f.endLimit, f.fill, f.restoreLabels = filePosition{}, true, false
	if page {
		f.history = append(f.history, s.fileRange)
		f.from, f.rowLimit = s.end, 0
	} else {
		f.history = nil
		f.from, f.rowLimit = s.end, len(s.rows)
		if step < len(s.rows) {
			f.from = s.rows[step]
		}
	}
	return m.refreshFiles()
}

func (m *Model) backFiles(page bool, step int) tea.Cmd {
	f, s := m.files, &m.files.screen
	if s.from == (filePosition{}) {
		return nil
	}
	f.endLimit, f.fill, f.rowLimit, f.restoreLabels = filePosition{}, false, 0, false
	if page && len(f.history) != 0 {
		last := f.history[len(f.history)-1]
		f.history = f.history[:len(f.history)-1]
		f.from, f.endLimit, f.restoreLabels = last.from, last.end, last.labeled
		return m.refreshFiles()
	}
	f.history = nil
	if page {
		step = max(1, m.window.Height-1)
		if !s.labeled {
			step = max(1, step-1)
		}
		f.endLimit = s.from
	} else {
		f.rowLimit = len(s.rows)
	}
	for ; step > 0 && f.from != (filePosition{}); step-- {
		if f.from.line > 0 {
			f.from.line--
			continue
		}
		f.from.item--
		width := max(1, m.window.Width)
		// A table-eligible file can have appeared in a labeled screen beside
		// an unfamiliar record. Step back into its actual previous layout.
		labeled, seen := f.wasLabeled[f.from.item]
		if !seen {
			_, fits, err := m.fileTable(f.from.item, f.from.item+1, width)
			if err != nil {
				m.err = err
				return m.refreshFiles()
			}
			labeled = !fits
		}
		if !labeled {
			continue
		}
		lines, err := m.fileLabelLines(f.from.item, width)
		if err != nil {
			m.err = err
			return m.refreshFiles()
		}
		f.from.line = len(lines) - 1
	}
	if page && step > 0 && s.from != f.firstScreenEnd {
		// Clamp near the top, except when returning to a short initial screen.
		f.endLimit = filePosition{}
	}
	return m.refreshFiles()
}

func (m *Model) fileTable(from, end, width int) (string, bool, error) {
	return RenderTable(m.opts.Context, "(resource) files > (method) list", m.files.items[from:end], width, false, m.opts.Style)
}

func (m *Model) filesView() string {
	f := m.files
	width := max(1, m.window.Width)
	position := "Files"
	if len(f.screen.rows) != 0 {
		first := f.screen.from.item + 1
		last := f.screen.rows[len(f.screen.rows)-1].item + 1
		position = fmt.Sprintf("File %d", first)
		if first != last {
			position = fmt.Sprintf("Files %d–%d", first, last)
		}
	} else if m.loaded {
		position = "Files 0"
	}
	controls := "↑↓: row   Space: next screen   b: back   q: quit"
	medium, short := "Space: next  b: back  q: quit", "↑↓ Space b q"
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
		position = strings.TrimPrefix(strings.TrimPrefix(position, "Files "), "File ")
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
		return f.screen.content + "\n" + footer
	}
	if m.loaded && !m.loading {
		return "No results.\n" + footer
	}
	return footer
}
