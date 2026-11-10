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
	opts := testOptions(t.Context())
	opts.Stdout, opts.Singular, opts.Plural = io.Discard, "File", "Files"
	opts.RenderTable = func(items []gjson.Result, width int) (string, bool, error) {
		return RenderTable(t.Context(), "(resource) files > (method) list", items, width, false, Style{})
	}
	opts.RenderRecordLabels = func(item gjson.Result, _ bool) (string, error) {
		return item.Get("id").Str + "\nlabel A\nlabel B\nlabel C\n", nil
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
		if _, ended := msg.(tea.QuitMsg); ended {
			if m.records.mode != recordOpening {
				return
			}
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

func TestFilesScreenNavigationReadsCachedRowsBeforeFetching(t *testing.T) {
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
	check(1, 28, 1)
	moveFiles(m, tea.KeyDown)
	check(2, 29, 1)
	moveFiles(m, tea.KeyDown)
	check(3, 30, 1)
	moveFiles(m, tea.KeyUp)
	check(2, 29, 1)
	moveFiles(m, 'b')
	check(1, 28, 1)
	moveFiles(m, ' ')
	require.True(t, m.View().AltScreen)
	check(29, 30, 1)
	moveFiles(m, ' ')
	check(31, 58, 2)
	moveFiles(m, ' ')
	check(59, 60, 2)
	moveFiles(m, ' ')
	check(61, 65, 3)
	require.Contains(t, m.View().Content, "End")
	moveFiles(m, 'b')
	check(59, 60, 3)
	moveFiles(m, 'b')
	check(31, 58, 3)
	moveFiles(m, 'b')
	check(29, 30, 3)
	moveFiles(m, 'b')
	check(1, 28, 3)
}

func TestInlinePreviewReturnsCompleteResultsThatFitTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		hint string
		rows int
	}{{"", 38}, {"Details: --format json", 37}} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("hint=%q/overflow=%d", tc.hint, extra), func(t *testing.T) {
				count := tc.rows + extra
				m, calls := filesViewerFixture(t, 120, 40, count, count)
				m.opts.Hint = tc.hint
				finishFilesMove(m, m.Init())
				require.Equal(t, 1, *calls)
				require.False(t, m.View().AltScreen)
				require.Len(t, m.records.screen.rows, tc.rows)
				if extra == 0 {
					require.True(t, m.quitting)
					require.Len(t, m.pending, 1)
					require.Contains(t, m.pending[0], fmt.Sprintf("f%03d", count))
				} else {
					require.False(t, m.quitting)
					require.Len(t, strings.Split(m.View().Content, "\n"), 40)
					moveFiles(m, ' ')
					require.Contains(t, m.View().Content, fmt.Sprintf("f%03d", count))
					require.Equal(t, 1, *calls)
				}
			})
		}
	}
}

func TestPagerShowsVisibleRangeAcrossFetchBackResizeAndEOF(t *testing.T) {
	for _, count := range []struct{ total, page int }{{}, {1000, 1}, {55, 3}} {
		t.Run(fmt.Sprintf("total=%d/page=%d", count.total, count.page), func(t *testing.T) {
			fixture, calls := filesViewerFixture(t, 100, 27, 25, 55)
			opts := fixture.opts
			opts.Pager = true
			m := New(opts, func() (Page, error) {
				page, err := fixture.fetch()
				if *calls == count.page {
					page.Total = count.total
				}
				return page, err
			}, fixture.cancel, 100, 27)
			t.Cleanup(func() { require.NoError(t, m.Finish(nil, nil)) })
			check := func(span, control string, requests int) {
				t.Helper()
				if count.total != 0 && *calls >= count.page {
					span += fmt.Sprintf("/%d", count.total)
				}
				lines := strings.Split(m.View().Content, "\n")
				require.Equal(t, span, strings.Fields(lines[len(lines)-1])[0])
				require.Contains(t, lines[len(lines)-1], control)
				require.Equal(t, requests, *calls)
			}
			finishFilesMove(m, m.Init())
			check("1–25", "Space: more", 1)
			moveFiles(m, ' ')
			check("26–50", "Space: more", 2)
			moveFiles(m, 'b')
			check("1–25", "i: inline", 2)
			moveFiles(m, ' ')
			check("26–50", "i: inline", 2)
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 4})
			check("26–27", "q: quit", 2)
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 27})
			moveFiles(m, ' ')
			check("51–55", "End", 3)
			moveFiles(m, 'b')
			check("26–50", "Space: more", 3)
			require.NotNil(t, viewerKey(m, 'i'))
			require.NoError(t, m.printLoaded())
			require.Equal(t, count.total, m.total)
			require.Equal(t, 3, *calls)
		})
	}
}

func TestPagerCountsWrappedRecordsOnceAndKeepsWholeOrdinals(t *testing.T) {
	fixture, calls := filesViewerFixture(t, 12, 3, 1, 3)
	opts := fixture.opts
	opts.Pager = true
	m := New(opts, fixture.fetch, fixture.cancel, 12, 3)
	t.Cleanup(func() { require.NoError(t, m.Finish(nil, nil)) })
	page := m.Init()().(listPageMessage)
	page.page.Total = 1000
	m.Update(page)
	require.Contains(t, m.View().Content, "1/1000  q")
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "1/1000  q")
	require.Equal(t, 1, *calls)
	m.Update(tea.WindowSizeMsg{Width: 7, Height: 3})
	require.Contains(t, m.View().Content, "1  q")
	require.NotContains(t, m.View().Content, "/")
	m.Update(tea.WindowSizeMsg{Width: 2, Height: 3})
	require.Equal(t, "q", strings.Split(m.View().Content, "\n")[2])
}

func TestFilesShortFirstScreenCanBeRestored(t *testing.T) {
	m, calls := filesViewerFixture(t, 80, 8, 2, 10)
	finishFilesMove(m, m.Init())
	require.Contains(t, m.View().Content, "Files 1–2")
	require.Equal(t, 1, *calls)
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "Files 3–4")
	require.Equal(t, 2, *calls)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	moveFiles(m, 'b')
	require.Contains(t, m.View().Content, "Files 1–2")
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "Files 3–4")
	require.Equal(t, 2, *calls)
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "Files 5–6")
	require.Equal(t, 3, *calls)
	moveFiles(m, 'b')
	moveFiles(m, 'b')
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "Files 3–4")
	require.Equal(t, 3, *calls)
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

func TestFilesNavigationWaitsForVisibleRecords(t *testing.T) {
	for _, pager := range []bool{false, true} {
		for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 1}, {Width: 1, Height: 10}} {
			t.Run(fmt.Sprintf("pager=%t/%dx%d", pager, size.Width, size.Height), func(t *testing.T) {
				fixture, calls := filesViewerFixture(t, size.Width, size.Height, 1, 3)
				opts := fixture.opts
				opts.Pager, opts.Hint = pager, "Details: --format json"
				m := New(opts, fixture.fetch, fixture.cancel, size.Width, size.Height)
				t.Cleanup(func() { require.NoError(t, m.Finish(nil, nil)) })
				finishFilesMove(m, m.Init())
				for _, key := range []rune{' ', tea.KeyDown, 'd', 'b', tea.KeyUp} {
					require.Nil(t, viewerKey(m, key))
				}
				require.Equal(t, 1, *calls)
				require.Equal(t, pager, m.View().AltScreen)
				require.NotContains(t, m.View().Content, "Space")
				m.Update(tea.WindowSizeMsg{Width: 80, Height: 2})
				require.Contains(t, m.View().Content, "f001")
				moveFiles(m, ' ')
				require.Contains(t, m.View().Content, "f002")
				m.Update(size)
				moveFiles(m, 'b')
				moveFiles(m, ' ')
				m.Update(tea.WindowSizeMsg{Width: 80, Height: 4})
				require.Contains(t, m.View().Content, "f002")
				require.Equal(t, 2, *calls)
				moveFiles(m, 'b')
				require.Contains(t, m.View().Content, "f001")
				require.Equal(t, 2, *calls)
			})
		}
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

func TestWrappedRecordContinuesFromTheInlinePreview(t *testing.T) {
	m, _ := filesViewerFixture(t, 12, 9, 1, 1)
	text := strings.Repeat("abcdefghijk", 12) + "--end-of-the-label"
	m.opts.RenderRecordLabels = func(gjson.Result, bool) (string, error) { return text, nil }
	finishFilesMove(m, m.Init())
	first := strings.ReplaceAll(m.records.screen.content, "\n", "")
	require.Equal(t, 8, len(m.records.screen.rows))
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
