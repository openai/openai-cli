package listview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/tidwall/gjson"
)

func viewerOptions(t *testing.T, out *bytes.Buffer) Options {
	opts := testOptions(t.Context())
	opts.Stdout = out
	opts.RenderPage = func(items []gjson.Result, _ int) (string, error) {
		var text strings.Builder
		for _, item := range items {
			fmt.Fprintln(&text, item.Get("id").String())
		}
		return text.String(), nil
	}
	return opts
}

func viewerPage(ids ...string) Page {
	var items []gjson.Result
	for _, id := range ids {
		items = append(items, gjson.Parse(fmt.Sprintf(`{"id":%q}`, id)))
	}
	return Page{Items: items}
}

func viewerKey(m *Model, key rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
	return cmd
}

func TestViewerBackKeepsFetchFrontier(t *testing.T) {
	var out bytes.Buffer
	pages := []Page{viewerPage("first"), viewerPage("second"), viewerPage("third")}
	pages[0].More, pages[1].More = true, true
	var requests atomic.Int32
	m := New(viewerOptions(t, &out), func() (Page, error) {
		return pages[requests.Add(1)-1], nil
	}, func() {}, 80, 20)
	m.Update(m.Init()())
	m.Update(viewerKey(m, ' ')())
	viewerKey(m, 'b')
	if !strings.Contains(m.View().Content, "first") || requests.Load() != 2 || out.Len() != 0 {
		t.Fatal("back navigation fetched or printed data")
	}
	view := m.View().Content
	if viewerKey(m, 'i') != nil || m.View().Content != view {
		t.Fatal("removed inline shortcut changed the pager")
	}
	if viewerKey(m, ' ') != nil || !strings.Contains(m.View().Content, "second") || requests.Load() != 2 {
		t.Fatal("forward navigation refetched cached data")
	}
	m.Update(viewerKey(m, ' ')())
	if !strings.Contains(m.View().Content, "third") || requests.Load() != 3 || m.quitting {
		t.Fatal("forward navigation lost its fetch frontier")
	}
	viewerKey(m, 'q')
	if !m.View().AltScreen || m.View().Content != "" {
		t.Fatal("screen ownership changed before terminal restoration")
	}
	if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
		t.Fatal("quit printed cached results", err)
	}
}

func TestViewerPendingFetchIgnoresNavigation(t *testing.T) {
	failure := errors.New("synthetic second-page failure")
	for _, fetchErr := range []error{nil, failure} {
		t.Run(fmt.Sprint(fetchErr), func(t *testing.T) {
			var out bytes.Buffer
			started, release := make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			m := New(viewerOptions(t, &out), func() (Page, error) {
				requests.Add(1)
				close(started)
				<-release
				return viewerPage("second"), fetchErr
			}, func() {}, 80, 20)
			first := viewerPage("first")
			first.More = true
			m.Update(listPageMessage{page: first})
			cmd := viewerKey(m, ' ')
			<-started
			for _, key := range " bi " {
				if viewerKey(m, key) != nil || m.quitting || !m.loading {
					t.Fatal("input changed the pending request")
				}
			}
			close(release)
			m.Update(cmd())
			want := "first\nsecond\n"
			if fetchErr == nil {
				if m.quitting || !strings.Contains(m.View().Content, "second") {
					t.Fatal("successful response did not remain in the pager")
				}
				viewerKey(m, 'q')
				want = ""
			}
			if err := m.Finish(nil, nil); !errors.Is(err, fetchErr) {
				t.Fatal("lost request failure", err)
			}
			if out.String() != want || requests.Load() != 1 {
				t.Fatal("pending request lost or duplicated results", out.String())
			}
		})
	}
}

func TestViewerFinalPageStaysUntilQuit(t *testing.T) {
	var out bytes.Buffer
	m := New(viewerOptions(t, &out), nil, func() {}, 80, 20)
	m.Update(listPageMessage{page: viewerPage("final")})
	for _, key := range []rune{' ', 'i', 'p'} {
		if viewerKey(m, key) != nil || m.quitting || !strings.Contains(m.View().Content, "End") {
			t.Fatal("viewer closed or fetched at the final page")
		}
	}
	if got := strings.Count(m.View().Content, "\n"); got != 1 {
		t.Fatalf("short viewer has %d extra rows", got)
	}
	viewerKey(m, 'q')
	if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
		t.Fatal("quit printed history", err)
	}
}

func TestViewerLargeLineAndResizePreserveOriginal(t *testing.T) {
	const size = 8 << 20
	id := "file-" + strings.Repeat("x", size) + "-end"
	var out bytes.Buffer
	m := New(viewerOptions(t, &out), nil, func() {}, 80, 12)
	m.Update(listPageMessage{page: viewerPage(id, "next-record")})
	viewerKey(m, ' ')
	m.Update(tea.WindowSizeMsg{Width: 31, Height: 7})
	view := ansi.Strip(m.View().Content)
	if strings.Count(view, "\n") > 6 || len(view) > 31*7+7 {
		t.Fatal("large line escaped the bounded viewer")
	}
	if m.viewer.pages[0] != id+"\nnext-record\n" {
		t.Fatal("resize truncated or hardwrapped the original ID")
	}
	viewerKey(m, 'q')
	if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
		t.Fatal("quit printed a large cached page", err)
	}
}

func TestViewerErrorRetainsPreviouslyLoadedResults(t *testing.T) {
	var out bytes.Buffer
	failure := errors.New("synthetic API failure")
	m := New(viewerOptions(t, &out), nil, func() {}, 80, 20)
	m.Update(listPageMessage{page: viewerPage("first")})
	m.Update(listPageMessage{err: failure})
	if err := m.Finish(nil, nil); !errors.Is(err, failure) || out.String() != "first\n" {
		t.Fatal("lost earlier results or later failure", err, out.String())
	}
}

func TestViewerHistoryWriteFailureDoesNotRetry(t *testing.T) {
	var out bytes.Buffer
	failure := errors.New("synthetic output failure")
	opts := viewerOptions(t, &out)
	calls := 0
	opts.Stdout = navigationWriteFunc(func(data []byte) (int, error) {
		calls++
		if calls == 2 {
			out.Write(data[:2])
			return 2, failure
		}
		return out.Write(data)
	})
	m := New(opts, nil, func() {}, 80, 20)
	m.Update(listPageMessage{page: viewerPage("first")})
	m.Update(listPageMessage{page: viewerPage("second")})
	m.Update(listPageMessage{err: errors.New("synthetic API failure")})
	if err := m.printErrorPages(); !errors.Is(err, failure) {
		t.Fatal("lost output failure", err)
	}
	if err := m.Finish(failure, nil); !errors.Is(err, failure) {
		t.Fatal("finish lost output failure", err)
	}
	if calls != 2 || out.String() != "first\nse" {
		t.Fatal("retried partial history write", calls, out.String())
	}
}

func TestViewerLoadingCanCancel(t *testing.T) {
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	opts := viewerOptions(t, &out)
	opts.Context = ctx
	m := New(opts, func() (Page, error) {
		<-ctx.Done()
		return Page{}, ctx.Err()
	}, cancel, 80, 20)
	cmd := m.Init()
	viewerKey(m, 'q')
	m.Update(cmd())
	if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
		t.Fatal("quit printed cached history or retained cleanup cancellation", err)
	}
}
