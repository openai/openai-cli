package listview

import (
	"fmt"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func filesViewerFixture(t *testing.T, width, height, pageSize, total int) (*Model, *int) {
	t.Helper()
	calls, count := 0, 0
	opts := Options{Context: t.Context(), Stdout: io.Discard, Singular: "File", Plural: "Files",
		RenderTable: func(items []gjson.Result, width int) (string, bool, error) {
			return RenderTable(t.Context(), "(resource) files > (method) list", items, width, false, Style{})
		},
		RenderRecordLabels: func(item gjson.Result, _ bool) (string, error) {
			id := item.Get("id").Str
			return id + "\nlabel A\nlabel B\nlabel C\n", nil
		},
	}
	return New(opts, func() (Page, error) {
		calls++
		page := Page{}
		for i := 0; i < pageSize && count < total; i++ {
			count++
			page.Items = append(page.Items, gjson.Parse(fmt.Sprintf(
				`{"id":"f%03d","object":"file","filename":"f.txt","purpose":"batch","bytes":128,"status":"processed"}`, count)))
		}
		page.More = count < total
		return page, nil
	}, func() {}, width, height), &calls
}

func finishFilesMove(m *Model, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if _, ended := msg.(tea.QuitMsg); ended && m.records.mode == recordOpening {
			cmd = m.Init()
		} else {
			_, cmd = m.Update(msg)
		}
	}
}

func moveFiles(m *Model, code rune) {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	finishFilesMove(m, cmd)
}

func TestFilesScreenNavigationCrossesAPIPages(t *testing.T) {
	m, calls := filesViewerFixture(t, 120, 30, 30, 65)
	finishFilesMove(m, m.Init())
	check := func(first, last, wantCalls int) {
		t.Helper()
		lines := strings.Split(m.View().Content, "\n")
		require.Contains(t, lines[0], "FILENAME")
		require.Len(t, lines, last-first+3)
		for i := first; i <= last; i++ {
			require.True(t, strings.HasPrefix(lines[i-first+1], fmt.Sprintf("f%03d ", i)))
		}
		require.Contains(t, lines[len(lines)-1], fmt.Sprintf("Files %d–%d", first, last))
		require.Equal(t, wantCalls, *calls)
	}
	require.False(t, m.View().AltScreen)
	check(1, 5, 1)
	moveFiles(m, tea.KeyDown)
	check(2, 6, 1)
	moveFiles(m, tea.KeyDown)
	check(3, 7, 1)
	moveFiles(m, tea.KeyUp)
	check(2, 6, 1)
	moveFiles(m, 'b')
	check(1, 5, 1)
	moveFiles(m, ' ')
	require.True(t, m.View().AltScreen)
	check(6, 33, 2)
	moveFiles(m, ' ')
	check(34, 61, 3)
	moveFiles(m, ' ')
	check(62, 65, 3)
	require.Contains(t, m.View().Content, "End")
	moveFiles(m, 'b')
	check(34, 61, 3)
	moveFiles(m, 'b')
	check(6, 33, 3)
	moveFiles(m, 'b')
	check(1, 5, 3)
}

func TestFilesShortFirstScreenCanBeRestored(t *testing.T) {
	m, calls := filesViewerFixture(t, 80, 8, 2, 10)
	finishFilesMove(m, m.Init())
	require.Contains(t, m.View().Content, "Files 1–2")
	require.Equal(t, 1, *calls)
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "Files 3–8")
	require.Equal(t, 4, *calls)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	moveFiles(m, 'b')
	require.Contains(t, m.View().Content, "Files 1–2")
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "Files 3–8")
	require.Equal(t, 4, *calls)
}

func TestFilesWrapsAndPaginatesLabeledOutput(t *testing.T) {
	m, calls := filesViewerFixture(t, 12, 4, 2, 2)
	finishFilesMove(m, m.Init())
	view := m.View().Content
	require.True(t, strings.HasPrefix(view, "f001\nlabel A\nlabel B\n"), view)
	require.Contains(t, view, "q")
	moveFiles(m, ' ')
	view = m.View().Content
	require.True(t, strings.HasPrefix(view, "label C\nf002\nlabel A\n"), view)
	moveFiles(m, 'b')
	require.True(t, strings.HasPrefix(m.View().Content, "f001\nlabel A\nlabel B\n"))
	moveFiles(m, ' ')
	moveFiles(m, ' ')
	require.True(t, strings.HasPrefix(m.View().Content, "label B\nlabel C\n"))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 4})
	require.Contains(t, m.View().Content, "FILENAME")
	require.Contains(t, m.View().Content, "f002")
	require.Equal(t, 1, *calls)
	for _, width := range []int{1, 3, 8, 20} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 1})
		require.Contains(t, m.View().Content, "q")
		require.LessOrEqual(t, ansi.StringWidth(m.View().Content), width)
	}
}

func TestFilesArrowUpReturnsToPreviousLabeledLine(t *testing.T) {
	m, _ := filesViewerFixture(t, 80, 6, 12, 12)
	msg := m.Init()().(listPageMessage)
	// The second file fits by itself but first appears beside an unknown file
	// in labeled output. Its last line precedes the next table.
	msg.page.Items[0] = gjson.Parse(`{"id":"f001","object":"file","future_field":"keep"}`)
	m.opts.RenderRecordLabels = func(item gjson.Result, _ bool) (string, error) {
		id := item.Get("id").Str
		parts := []string{id + " first", id + " middle", id + " last"}
		if id == "f001" {
			parts = append([]string{"one", "two", "three", "four"}, parts...)
		}
		return strings.Join(parts, "\n") + "\n", nil
	}
	m.Update(msg)
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "f002 last")
	moveFiles(m, ' ')
	require.True(t, strings.HasPrefix(m.View().Content, "ID "))
	moveFiles(m, tea.KeyUp)
	require.True(t, strings.HasPrefix(m.View().Content, "f002 last\n"))
}

func TestWrappedRecordContinuesFromTheCompactPreview(t *testing.T) {
	m, _ := filesViewerFixture(t, 12, 9, 1, 1)
	const text = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789--end-of-the-label"
	m.opts.RenderRecordLabels = func(gjson.Result, bool) (string, error) { return text, nil }
	finishFilesMove(m, m.Init())
	first := strings.ReplaceAll(m.records.screen.content, "\n", "")
	require.True(t, strings.HasPrefix(text, first))
	require.Less(t, len(first), len(text))
	moveFiles(m, ' ')
	require.True(t, m.View().AltScreen)
	require.Equal(t, text, first+strings.ReplaceAll(m.records.screen.content, "\n", ""))
}

func TestFilesArrowsReuseWrappedRecord(t *testing.T) {
	m, _ := filesViewerFixture(t, 12, 4, 1, 1)
	renders := 0
	m.opts.RenderRecordLabels = func(gjson.Result, bool) (string, error) {
		renders++
		return strings.Repeat("long value\n", 50), nil
	}
	finishFilesMove(m, m.Init())
	for i := 0; i < 10; i++ {
		moveFiles(m, tea.KeyDown)
	}
	require.Equal(t, 1, renders)
	m.Update(tea.WindowSizeMsg{Width: 10, Height: 4})
	require.Equal(t, 2, renders)
}

func TestAmbiguousPageRetainsFullLabelsForEveryRecord(t *testing.T) {
	m, _ := filesViewerFixture(t, 80, 4, 4, 4)
	page := m.Init()().(listPageMessage)
	page.page.FullLabels = true
	page.page.Total = 4
	m.opts.Singular, m.opts.Plural = "Model", "Models"
	m.opts.RenderRecordLabels = func(item gjson.Result, full bool) (string, error) {
		require.True(t, full)
		return item.Get("id").Str + " original metadata\n", nil
	}
	m.opts.RenderTable = func([]gjson.Result, int) (string, bool, error) {
		t.Fatal("an ambiguous source page reached the compact table")
		return "", false, nil
	}
	m.Update(page)
	require.Contains(t, m.View().Content, "Models 1–3 of 4")
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "f004 original metadata")
	require.Contains(t, m.View().Content, "Model 4 of 4")
}
