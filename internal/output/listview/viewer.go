package listview

import (
	"context"
	"errors"
	"sort"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// listViewer retains rendered pages separately from the forward-only fetcher.
// Only the selected page needs a wrapped display copy. Original lines remain
// intact for reflow and partial-error output.
type listViewer struct {
	pages      []string
	index      int
	viewport   viewport.Model
	lineStarts []int
}

func newListViewer() *listViewer {
	v := &listViewer{viewport: viewport.New()}
	v.viewport.FillHeight = false
	return v
}

func (v *listViewer) resize(ctx context.Context, width, height int) error {
	width = max(1, width)
	offset := v.viewport.YOffset()
	anchor := 0
	if len(v.lineStarts) != 0 {
		anchor = sort.SearchInts(v.lineStarts, v.viewport.YOffset()+1) - 1
	}
	if len(v.pages) != 0 && v.viewport.Width() != width {
		if err := v.wrap(ctx, width); err != nil {
			return err
		}
		// Reflow keeps the current logical line visible without fetching data.
		offset = v.lineStarts[min(anchor, len(v.lineStarts)-1)]
	}
	v.viewport.SetWidth(width)
	v.viewport.SetHeight(min(max(1, height-1), v.viewport.TotalLineCount()))
	v.viewport.SetYOffset(offset)
	return nil
}

func (v *listViewer) wrap(ctx context.Context, width int) error {
	v.lineStarts = nil
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(v.pages[v.index], "\n"), "\n") {
		if err := ctx.Err(); err != nil {
			return err
		}
		v.lineStarts = append(v.lineStarts, len(lines))
		// Match the viewport's four-space tab expansion before counting rows.
		// Keep the original page intact for reflow and partial-error output.
		line = strings.ReplaceAll(line, "\t", "    ")
		lines = append(lines, strings.Split(ansi.Hardwrap(line, width, true), "\n")...)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	v.viewport.SetContentLines(lines)
	return nil
}

func (m *Model) selectViewerPage(index int, bottom bool) tea.Cmd {
	v := m.viewer
	v.index = index
	if err := v.wrap(m.opts.Context, max(1, m.window.Width)); err != nil {
		m.err = errors.Join(m.err, err)
		return m.quit()
	}
	v.viewport.SetWidth(max(1, m.window.Width))
	v.viewport.SetHeight(min(max(1, m.window.Height-1), v.viewport.TotalLineCount()))
	if bottom {
		v.viewport.GotoBottom()
	} else {
		v.viewport.GotoTop()
	}
	return nil
}

func (m *Model) receiveViewerPage(content string) tea.Cmd {
	v := m.viewer
	if content != "" {
		v.pages = append(v.pages, content)
	}
	if m.err != nil {
		m.pending = v.pages
		v.pages = nil
		return m.quit()
	}
	if content != "" {
		return m.selectViewerPage(len(v.pages)-1, false)
	}
	return nil
}

func (m *Model) viewerKey(key string) tea.Cmd {
	v := m.viewer
	if m.loading || len(v.pages) == 0 {
		return nil
	}
	switch key {
	case "space":
		if !v.viewport.AtBottom() {
			v.viewport.PageDown()
		} else if v.index+1 < len(v.pages) {
			return m.selectViewerPage(v.index+1, false)
		} else if m.more {
			return m.load()
		}
	case "b":
		if !v.viewport.AtTop() {
			v.viewport.PageUp()
		} else if v.index > 0 {
			return m.selectViewerPage(v.index-1, true)
		}
	}
	return nil
}

func (m *Model) viewerView() string {
	v := m.viewer
	width := max(1, m.window.Width)
	footer := "Space: more   b: back   q: quit"
	if !m.more && v.index == len(v.pages)-1 && v.viewport.AtBottom() {
		footer = "End   b: back   q: quit"
	}
	if m.loading {
		footer = "Loading…   q: quit"
	}
	if ansi.StringWidth(footer) > width {
		footer = strings.ReplaceAll(footer, "   ", " ")
	}
	if ansi.StringWidth(footer) > width {
		footer = "Space/b/q"
		if m.loading {
			footer = "Loading… q"
		}
	}
	footer = m.opts.Style.controls(ansi.Truncate(footer, width, ""))
	if len(v.pages) == 0 || m.window.Height <= 1 {
		return footer
	}
	return v.viewport.View() + "\n" + footer
}
