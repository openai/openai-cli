package listview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/tidwall/gjson"
)

func TestViewerResizeRendersOnlySelectedPage(t *testing.T) {
	var out bytes.Buffer
	opts := viewerOptions(t, &out)
	render := opts.RenderPage
	var calls []string
	opts.RenderPage = func(items []gjson.Result, width int) (string, error) {
		calls = append(calls, fmt.Sprintf("%s/%d", items[0].Get("id").String(), width))
		return render(items, width)
	}
	m := New(opts, nil, func() {}, 80, 20)
	first := viewerPage("first")
	m.Update(listPageMessage{page: first})
	m.Update(listPageMessage{page: viewerPage("second")})
	// A producer can reuse its slice after delivery. Cached records remain owned.
	first.Items[0] = gjson.Parse(`{"id":"replacement"}`)
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 20})
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 10})
	viewerKey(m, 'b')
	if !strings.Contains(m.View().Content, "first") {
		t.Fatal("cached source changed after the producer reused its slice")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	viewerKey(m, ' ')
	if got, want := strings.Join(calls, ","), "first/80,second/80,second/20,first/20,first/80,second/80"; got != want {
		t.Fatalf("unexpected render sequence: got %q, want %q", got, want)
	}
	viewerKey(m, 'q')
	if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
		t.Fatal("resizing changed successful quit output", err, out.String())
	}
}

func TestViewerResizeRenderFailureRetainsCachedPages(t *testing.T) {
	for _, back := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("back%t/canceled%t", back, canceled), func(t *testing.T) {
				var out bytes.Buffer
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := errors.New("synthetic render failure")
				opts := viewerOptions(t, &out)
				opts.Context = ctx
				opts.RenderPage = func(items []gjson.Result, width int) (string, error) {
					id := items[0].Get("id").String()
					if width == 20 && (!back || id == "first") {
						if canceled {
							cancel()
							return "incomplete replacement", ctx.Err()
						}
						return "incomplete replacement", failure
					}
					return fmt.Sprintf("%s/%d\n", id, width), nil
				}
				m := New(opts, nil, func() {}, 80, 20)
				m.Update(listPageMessage{page: viewerPage("first")})
				m.Update(listPageMessage{page: viewerPage("second")})
				m.Update(tea.WindowSizeMsg{Width: 20, Height: 20})
				want := "first/80\nsecond/80\n"
				if back {
					viewerKey(m, 'b')
					want = "first/80\nsecond/20\n"
				}
				if !m.quitting || m.View().Content != "" || !m.View().AltScreen {
					t.Fatal("render failure did not preserve terminal restoration")
				}
				if canceled {
					failure = context.Canceled
					want = ""
				}
				if err := m.Finish(nil, nil); !errors.Is(err, failure) {
					t.Fatal("lost render failure or cancellation", err)
				}
				if out.String() != want {
					t.Fatalf("cached output changed: got %q, want %q", out.String(), want)
				}
			})
		}
	}
}
