package listview

import (
	"context"
	"errors"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/tidwall/gjson"
)

// listViewer retains loaded records separately from the forward-only fetcher.
// Only the selected page needs rendering at the current width and a wrapped
// display copy. Cached content remains available for partial-error output.
type listViewer struct {
	pages      []listViewerPage
	index      int
	viewport   viewport.Model
	lineStarts []int
}

type listViewerPage struct {
	items   []gjson.Result
	content string
	width   int
}

func newListViewer() *listViewer {
	v := &listViewer{viewport: viewport.New()}
	v.viewport.FillHeight = false
	return v
}

func (v *listViewer) resize(ctx context.Context, render func([]gjson.Result, int) (string, error), width, height int) error {
	width = max(1, width)
	offset := v.viewport.YOffset()
	anchor := 0
	if len(v.lineStarts) != 0 {
		anchor = sort.SearchInts(v.lineStarts, v.viewport.YOffset()+1) - 1
	}
	if len(v.pages) != 0 && v.viewport.Width() != width {
		if err := v.wrap(ctx, render, width); err != nil {
			return err
		}
		// Keep the current line visible when the renderer retains its layout.
		offset = v.lineStarts[min(anchor, len(v.lineStarts)-1)]
	}
	v.viewport.SetWidth(width)
	v.viewport.SetHeight(min(max(1, height-1), v.viewport.TotalLineCount()))
	v.viewport.SetYOffset(offset)
	return nil
}

func (v *listViewer) wrap(ctx context.Context, render func([]gjson.Result, int) (string, error), width int) error {
	page := &v.pages[v.index]
	if page.width != width {
		content, err := render(page.items, width)
		if err != nil {
			return err
		}
		page.content, page.width = content, width
	}
	v.lineStarts = nil
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(page.content, "\n"), "\n") {
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
	if err := v.wrap(m.opts.Context, m.opts.RenderPage, max(1, m.window.Width)); err != nil {
		m.err = errors.Join(m.err, err)
		m.pending = v.takePages()
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

func (m *Model) receiveViewerPage(items []gjson.Result, content string) tea.Cmd {
	v := m.viewer
	if content != "" {
		v.pages = append(v.pages, listViewerPage{
			items: append([]gjson.Result(nil), items...), content: content, width: max(1, m.window.Width),
		})
	}
	if m.err != nil {
		m.pending = v.takePages()
		return m.quit()
	}
	if content != "" {
		return m.selectViewerPage(len(v.pages)-1, false)
	}
	return nil
}

func (v *listViewer) takePages() []string {
	pages := make([]string, len(v.pages))
	for i, page := range v.pages {
		pages[i] = page.content
	}
	v.pages = nil
	return pages
}

func (m *Model) viewerKey(msg tea.KeyPressMsg) tea.Cmd {
	v := m.viewer
	if m.loading || len(v.pages) == 0 {
		return nil
	}
	keys := v.viewport.KeyMap
	forward, back := key.Matches(msg, keys.PageDown), key.Matches(msg, keys.PageUp)
	// Line keys can cross a loaded boundary. Only a page key can request more data.
	if m.local || v.index+1 < len(v.pages) {
		forward = forward || key.Matches(msg, keys.Down, keys.HalfPageDown)
	}
	back = back || key.Matches(msg, keys.Up, keys.HalfPageUp)
	switch {
	case forward && v.viewport.AtBottom():
		if v.index+1 < len(v.pages) {
			return m.selectViewerPage(v.index+1, false)
		} else if m.more {
			return m.load()
		}
	case back && v.viewport.AtTop() && v.index > 0:
		return m.selectViewerPage(v.index-1, true)
	}
	var cmd tea.Cmd
	v.viewport, cmd = v.viewport.Update(msg)
	return cmd
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
