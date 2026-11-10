package listview

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/output/transform"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPagerSwitchPrintsAllCachedRecordsOnceAndContinuesOnSpace(t *testing.T) {
	var out bytes.Buffer
	opts := viewerOptions(t, &out)
	opts.Pager, opts.Hint = true, "Details: --format json"
	opts.RenderTable = func(items []gjson.Result, _ int) (string, bool, error) {
		text, err := opts.RenderPage(items, 80)
		return "ID\n" + text, true, err
	}
	opts.ProjectPrintTable = func(items []gjson.Result) (transform.ListTableProjection, error) {
		projection := transform.ListTableProjection{Headers: []string{"ID", "NAME"}}
		for _, item := range items {
			projection.Rows = append(projection.Rows, []string{item.Get("id").Str, "row"})
		}
		return projection, nil
	}
	pages := []Page{viewerPage("alpha", "bravo", "charlie"), viewerPage("delta"), viewerPage("echo", "foxtrot", "golf", "hotel", "india")}
	pages[0].More, pages[1].More = true, true
	calls := 0
	m := New(opts, func() (Page, error) {
		p := pages[calls]
		calls++
		return p, nil
	}, func() {}, 80, 5)
	require.True(t, m.View().AltScreen)
	finishFilesMove(m, m.Init())
	require.Contains(t, m.View().Content, "i: inline")
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "charlie")
	require.Equal(t, 1, calls)
	moveFiles(m, ' ')
	require.Contains(t, m.View().Content, "delta")
	moveFiles(m, 'b')
	require.Contains(t, m.View().Content, "charlie")
	require.Equal(t, 2, calls)

	require.NotNil(t, viewerKey(m, 'i'))
	require.Empty(t, out.String(), "the full-screen program still owns the terminal")
	require.NoError(t, m.printLoaded())
	require.Nil(t, m.Init())
	require.False(t, m.View().AltScreen)
	require.Equal(t, "Space: more   q: quit", m.View().Content)
	require.Equal(t, 2, calls)
	require.Empty(t, m.records.items)
	require.Empty(t, m.records.history)
	finishFilesMove(m, viewerKey(m, ' '))
	require.NoError(t, m.printPending())
	require.Equal(t, 3, calls)
	require.Contains(t, out.String(), "hotel")
	require.NotContains(t, out.String(), "india")
	finishFilesMove(m, viewerKey(m, ' '))
	require.NoError(t, m.printPending())
	require.Empty(t, m.inline.lines, "no hint-only advance")
	require.False(t, m.more)
	require.NoError(t, m.Finish(nil, nil))
	text := out.String()
	require.Equal(t, 1, strings.Count(text, "NAME"))
	require.Equal(t, 1, strings.Count(text, opts.Hint))
	last := -1
	for _, p := range pages {
		for _, item := range p.Items {
			id := item.Get("id").Str
			require.Equal(t, 1, strings.Count(text, id))
			index := strings.Index(text, id)
			require.Greater(t, index, last)
			last = index
		}
	}
}

func TestPagerEmptyOrShortFinalRemainsOpenUntilQuitOrInline(t *testing.T) {
	for _, p := range []Page{{}, viewerPage("one")} {
		var out bytes.Buffer
		opts := testOptions(t.Context())
		opts.Stdout, opts.Pager = &out, true
		m := New(opts, func() (Page, error) { return p, nil }, func() {}, 80, 20)
		finishFilesMove(m, m.Init())
		require.True(t, m.View().AltScreen)
		require.False(t, m.quitting)
		require.Contains(t, m.View().Content, "End")
		require.Nil(t, viewerKey(m, ' '))
		require.NotNil(t, viewerKey(m, 'i'))
		require.NoError(t, m.printLoaded())
		require.NoError(t, m.Finish(nil, nil))
		want := "No results."
		if len(p.Items) > 0 {
			want = "one"
		}
		require.Equal(t, 1, strings.Count(out.String(), want))
	}
}

func TestInlinePartialFailureAndQuitDuringPendingRequest(t *testing.T) {
	failure := errors.New("synthetic page failure")
	for _, quit := range []bool{false, true} {
		var out bytes.Buffer
		opts := testOptions(t.Context())
		opts.Stdout, opts.Pager = &out, true
		ctx, cancel := context.WithCancel(t.Context())
		m := New(opts, func() (Page, error) {
			if quit {
				<-ctx.Done()
				return Page{}, ctx.Err()
			}
			return viewerPage("partial"), failure
		}, cancel, 80, 20)
		first := viewerPage("first")
		first.More = true
		m.Update(listPageMessage{page: first})
		require.NotNil(t, viewerKey(m, 'i'))
		require.NoError(t, m.printLoaded())
		cmd := viewerKey(m, ' ')
		require.NotNil(t, cmd)
		require.Nil(t, viewerKey(m, ' '), "pending fetch must remain unique")
		if quit {
			require.NotNil(t, viewerKey(m, 'q'))
			require.NoError(t, m.Finish(nil, nil))
			continue
		}
		m.Update(cmd())
		require.ErrorIs(t, m.Finish(nil, nil), failure)
		require.Equal(t, 1, strings.Count(out.String(), "partial"))
		require.Equal(t, 1, strings.Count(out.String(), "first"))
	}
}

func TestInlineRowsUsesCellWidthsAndBoundsLongValues(t *testing.T) {
	require.Equal(t, 2, inlineRows("12345界", 6, 20))
	require.Equal(t, 2, inlineRows("A\tbc", 6, 20))
	require.Equal(t, 1, inlineRows("\x1b[36mID\x1b[0m", 6, 20))
	require.Equal(t, 3, inlineRows(strings.Repeat("x", 20000), 6, 2))
}
