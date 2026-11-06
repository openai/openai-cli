package listview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestViewerResizeRendersOnlyVisibleRecords(t *testing.T) {
	var out bytes.Buffer
	opts := viewerOptions(t, &out)
	var calls []string
	opts.RenderTable = func(items []gjson.Result, width int) (string, bool, error) {
		require.Len(t, items, 1)
		id := items[0].Get("id").String()
		calls = append(calls, fmt.Sprintf("%s/%d", id, width))
		return "ID\n" + id + "\n", true, nil
	}
	// One table row fits in this browser. Moving forward requests the next
	// row, while resize and backward navigation only use received records.
	m := New(opts, func() (Page, error) { return viewerPage("second"), nil }, func() {}, 80, 3)
	first := viewerPage("first")
	first.More = true
	m.Update(listPageMessage{page: first})
	m.Update(viewerKey(m, ' ')())
	first.Items[0] = gjson.Parse(`{"id":"replacement"}`)
	calls = nil
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 3})
	require.Equal(t, []string{"second/19"}, calls)
	viewerKey(m, 'b')
	require.Contains(t, m.View().Content, "first", "the producer can reuse its slice after delivery")
	require.Equal(t, "first/19", calls[len(calls)-1])
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 3})
	require.Equal(t, "first/79", calls[len(calls)-1])
	require.Nil(t, viewerKey(m, ' '), "forward navigation should reuse the cached second row")
	require.Equal(t, "second/79", calls[len(calls)-1])
	viewerKey(m, 'q')
	require.NoError(t, m.Finish(nil, nil))
	require.Empty(t, out.String())
}

func TestViewerResizeRenderFailureRetainsReceivedPages(t *testing.T) {
	for _, back := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("back%t/canceled%t", back, canceled), func(t *testing.T) {
				var out bytes.Buffer
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := errors.New("synthetic render failure")
				opts := viewerOptions(t, &out)
				opts.Context = ctx
				opts.RenderTable = func(items []gjson.Result, width int) (string, bool, error) {
					id := items[0].Get("id").String()
					if width == 19 && (!back || id == "first") {
						if canceled {
							cancel()
							return "", false, ctx.Err()
						}
						return "", false, failure
					}
					return "ID\n" + id + "\n", true, nil
				}
				m := New(opts, func() (Page, error) { return viewerPage("second"), nil }, func() {}, 80, 3)
				first := viewerPage("first")
				first.More = true
				m.Update(listPageMessage{page: first})
				m.Update(viewerKey(m, ' ')())
				m.Update(tea.WindowSizeMsg{Width: 20, Height: 3})
				if back {
					viewerKey(m, 'b')
				}
				require.True(t, m.quitting)
				require.Empty(t, m.View().Content)
				require.True(t, m.View().AltScreen, "preserve terminal ownership through restoration")
				want := "first\nsecond\n"
				if canceled {
					failure, want = context.Canceled, ""
				}
				require.ErrorIs(t, m.Finish(nil, nil), failure)
				require.Equal(t, want, out.String())
			})
		}
	}
}
