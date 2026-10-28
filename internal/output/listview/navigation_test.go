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
	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/tidwall/gjson"
)

type navigationTestPage struct {
	raw      string
	next     *navigationTestPage
	err      error
	requests *int
}

func (p *navigationTestPage) RawJSON() string { return p.raw }
func (p *navigationTestPage) GetNextPage() (*navigationTestPage, error) {
	*p.requests++
	return p.next, p.err
}

func TestListPageFetcherWaitsAndPreservesPageSize(t *testing.T) {
	for _, limit := range []int64{-1, 0, 1, 2, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			requests := 0
			last := &navigationTestPage{raw: `{"data":[{"id":"c"}],"has_more":false}`, requests: &requests}
			first := &navigationTestPage{raw: `{"data":[{"id":"a"},{"id":"b"}],"has_more":true}`, next: last, requests: &requests}
			fetch := newListPageFetcher(context.Background(), func(context.Context) (*navigationTestPage, error) { requests++; return first, nil }, limit, "")
			if requests != 0 {
				t.Fatal("constructed fetcher made a request")
			}
			page, err := fetch()
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if limit >= 0 && limit < 2 {
				want = int(limit)
			}
			if len(page.Items) != want {
				t.Fatalf("got %d items, want %d", len(page.Items), want)
			}
			wantRequests := 1
			if limit == 0 {
				wantRequests = 0
			}
			if requests != wantRequests {
				t.Fatalf("first load made %d requests", requests)
			}
			if page.More != (limit == -1 || limit > 2) {
				t.Fatalf("unexpected continuation: %v", page.More)
			}
			page, err = fetch()
			if err != nil {
				t.Fatal(err)
			}
			if limit == -1 || limit > 2 {
				wantRequests++
				if len(page.Items) != 1 || page.Items[0].Get("id").String() != "c" {
					t.Fatalf("unexpected next page: %+v", page)
				}
			}
			if requests != wantRequests || page.More {
				t.Fatalf("unexpected final load: requests=%d, more=%v", requests, page.More)
			}
			if _, err := fetch(); err != nil || requests != wantRequests {
				t.Fatal("fetched after the last page", err, requests)
			}
		})
	}
}

func TestShowJSONPagesZeroRetainsRequestErrors(t *testing.T) {
	failure := errors.New("synthetic first request failure")
	for _, requestErr := range []error{nil, failure} {
		calls := 0
		err := ShowPages(func(context.Context) (*navigationTestPage, error) {
			calls++
			return nil, requestErr
		}, 0, testOptions(t.Context()), "")
		if calls != 1 || !errors.Is(err, requestErr) {
			t.Fatalf("calls=%d, error=%v; want %v", calls, err, requestErr)
		}
	}
}

func TestListPageFetcherStopsEmptyAndStalledCursors(t *testing.T) {
	for _, tt := range []struct {
		name, cursor, first, second string
		wantError                   bool
	}{
		{"empty", "", `{"data":[],"has_more":true}`, "", true},
		{"last", "", `{"data":[{"id":"a"}],"has_more":false}`, "", false},
		{"missing", "", `{"data":[{}],"has_more":true}`, "", true},
		{"repeated", "", `{"data":[{"id":"a"}],"has_more":true}`, `{"data":[{"id":"a"}],"has_more":true}`, true},
		{"last_id", "last_id", `{"data":[{"id":"a"}],"has_more":true,"last_id":"x"}`, `{"data":[{"id":"b"}],"has_more":true,"last_id":"x"}`, true},
		{"next", "next", `{"data":[{"id":"a"}],"has_more":true,"next":"x"}`, `{"data":[{"id":"b"}],"has_more":false}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			second := &navigationTestPage{raw: tt.second, requests: &requests}
			first := &navigationTestPage{raw: tt.first, next: second, requests: &requests}
			fetch := newListPageFetcher(context.Background(), func(context.Context) (*navigationTestPage, error) { requests++; return first, nil }, -1, tt.cursor)
			page, err := fetch()
			if page.More && err == nil {
				page, err = fetch()
			}
			if (err != nil) != tt.wantError {
				t.Fatalf("error=%v", err)
			}
			var navigationError *PaginationError
			if tt.wantError && !errors.As(err, &navigationError) {
				t.Fatalf("cursor failure has no safe diagnostic type: %v", err)
			}
			if page.More {
				t.Fatal("continues after final or stalled page")
			}
			prior := requests
			if _, err := fetch(); err != nil || requests != prior {
				t.Fatal("made request after stop", err)
			}
		})
	}
}

func TestListPageFetcherCancellationPreventsRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fetch := newListPageFetcher(ctx, func(context.Context) (*navigationTestPage, error) {
		t.Fatal("request after cancellation")
		return nil, nil
	}, -1, "")
	if _, err := fetch(); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestListNavigationUsesLoadedRowsBeforeOneRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var requests atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	m := New(testOptions(ctx), func() (Page, error) {
		requests.Add(1)
		close(started)
		<-release
		return viewerPage("second"), nil
	}, cancel, 80, 4)
	m.Update(listPageMessage{page: Page{Items: []gjson.Result{gjson.Parse(`{"a":"one","b":"two","c":"three","d":"four","e":"five","f":"six","g":"seven"}`)}, More: true}})
	for !m.viewer.viewport.AtBottom() {
		if cmd := viewerKey(m, ' '); cmd != nil || requests.Load() != 0 {
			t.Fatal("Space requested data before displaying loaded rows")
		}
	}
	cmd := viewerKey(m, ' ')
	if cmd == nil {
		t.Fatal("Space after loaded rows did not request the next page")
	}
	<-started
	for range 20 {
		if viewerKey(m, ' ') != nil {
			t.Fatal("duplicate page command")
		}
	}
	close(release)
	m.Update(cmd())
	if requests.Load() != 1 || m.quitting || !strings.Contains(m.View().Content, "second") {
		t.Fatal("final page must remain open after one request")
	}
}

type navigationWriteFunc func([]byte) (int, error)

func (write navigationWriteFunc) Write(data []byte) (int, error) { return write(data) }

func TestListNavigationOutputPreservesFailureAndRestoresModes(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	failure := errors.New("synthetic output failure")
	calls := 0
	var cleanup string
	sink := navigationWriteFunc(func(data []byte) (int, error) {
		calls++
		if calls == 1 {
			return 0, failure
		}
		cleanup = string(data)
		return len(data), nil
	})
	out := &listNavigationOutput{TerminalWriter: clioutput.TerminalWriter{
		Writer: clioutput.Writer{Context: context.Background(), Sink: sink},
	}, stop: stop}
	out.Write([]byte("results"))
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("sink failure did not stop viewer")
	}
	if _, err := out.WriteString("\x1b[?25h"); err != nil {
		t.Fatal("cleanup failed", err)
	}
	if cleanup != "\x1b[?25h" || !errors.Is(out.Err(), failure) {
		t.Fatal("lost original failure or terminal cleanup", out.Err())
	}
}

func TestListNavigationQuitRetainsRealFailures(t *testing.T) {
	failure := errors.New("synthetic page failure")
	for _, tt := range []struct {
		name           string
		received, want error
	}{
		{"canceled", context.Canceled, nil},
		{"wrapped", fmt.Errorf("request: %w", context.Canceled), nil},
		{"joined cancellation", errors.Join(context.Canceled, fmt.Errorf("request: %w", context.Canceled)), nil},
		{"failed", failure, failure},
		{"joined failure", errors.Join(failure, context.Canceled), failure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := &Model{opts: testOptions(ctx), cancel: cancel, loading: true}
			m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
			m.Update(listPageMessage{err: tt.received})
			if tt.want == nil && m.err != nil || tt.want != nil && !errors.Is(m.err, tt.want) {
				t.Fatalf("got %v, want %v", m.err, tt.want)
			}
			failure := m.Finish(nil, tea.ErrProgramKilled)
			if tt.want == nil && failure != nil || tt.want != nil && !errors.Is(failure, tt.want) {
				t.Fatalf("shutdown returned %v, want %v", failure, tt.want)
			}
		})
	}
}

func TestListNavigationFinishRetainsInterruptDuringCompletion(t *testing.T) {
	m := New(testOptions(t.Context()), nil, func() {}, 80, 24)
	m.quitting = true
	m.pending = []string{"unprinted result\n"}
	if err := m.Finish(nil, errors.Join(tea.ErrProgramKilled, tea.ErrInterrupted)); !errors.Is(err, tea.ErrInterrupted) {
		t.Fatalf("completion lost interruption: %v", err)
	}
	if len(m.pending) != 0 {
		t.Fatal("interrupted completion retained output for printing")
	}
}

func TestListNavigationRemovedKeysAndResizeDoNotFetch(t *testing.T) {
	m := New(testOptions(t.Context()), func() (Page, error) { t.Fatal("unexpected fetch"); return Page{}, nil }, func() {}, 80, 20)
	m.Update(listPageMessage{page: viewerPage("first", "second")})
	for _, key := range []rune{'p', 'i', 'b'} {
		if viewerKey(m, key) != nil {
			t.Fatal("unsupported key or backward boundary started work")
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 4})
	if len(m.viewer.pages) != 1 || m.loading || m.quitting || !strings.Contains(m.View().Content, "first") {
		t.Fatal("resize changed loaded content")
	}
}
func TestListNavigationLabelsPreserveLongIDs(t *testing.T) {
	id := "file-" + strings.Repeat("synthetic", 8)
	value := gjson.Parse(`{"id":"` + id + `","filename":"safe\u001b[2J.txt"}`)
	for _, width := range []int{20, 40} {
		content, err := testOptions(context.Background()).RenderPage([]gjson.Result{value}, width)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(content, "ID: "+id+"\n") || strings.ContainsRune(content, '\x1b') {
			t.Fatalf("width %d altered the ID or retained a terminal control: %q", width, content)
		}
	}
}

func TestListNavigationCompletePageStaysOpenWithoutPrinting(t *testing.T) {
	for _, items := range [][]gjson.Result{nil, {gjson.Parse(`{"id":"first"}`)}, {gjson.Parse(`{"id":"first"}`), gjson.Parse(`{"id":"second"}`)}} {
		var out bytes.Buffer
		opts := testOptions(t.Context())
		opts.Stdout = &out
		m := New(opts, nil, func() {}, 80, 20)
		_, cmd := m.Update(listPageMessage{page: Page{Items: items}})
		if cmd != nil || m.quitting || !m.View().AltScreen || !strings.Contains(m.View().Content, "End") || out.Len() != 0 {
			t.Fatal("successful completion must stay in the pager")
		}
		viewerKey(m, 'q')
		if err := m.Finish(nil, nil); err != nil || out.Len() != 0 {
			t.Fatal("quit must restore the terminal without printing history", err)
		}
	}
}
func TestListNavigationPartialPagePreservesErrorAndRecords(t *testing.T) {
	failure := errors.New("synthetic page failure")
	var out bytes.Buffer
	opts := testOptions(t.Context())
	opts.Stdout = &out
	m := New(opts, nil, func() {}, 40, 20)
	m.loaded = true
	m.Update(listPageMessage{page: Page{Items: []gjson.Result{gjson.Parse(`{"id":"partial"}`)}}, err: failure})
	if !m.quitting || !errors.Is(m.Finish(nil, nil), failure) || !strings.Contains(out.String(), "ID: partial") {
		t.Fatal("lost partial records or request failure")
	}
}

func TestListNavigationFinalEmptyPageKeepsLoadedResults(t *testing.T) {
	m := New(testOptions(t.Context()), nil, func() {}, 40, 20)
	m.Update(listPageMessage{page: viewerPage("first")})
	m.Update(listPageMessage{})
	if m.quitting || len(m.viewer.pages) != 1 || !strings.Contains(m.View().Content, "first") || !strings.Contains(m.View().Content, "End") {
		t.Fatal("empty final response lost or duplicated loaded results")
	}
}
func testOptions(ctx context.Context) Options {
	render := func(items []gjson.Result) (string, error) {
		return RenderLabels(items, transformers.Identity, clioutput.ReadableOptions{Context: ctx, DiagnosticsAllowed: true})
	}
	return Options{Context: ctx, RenderPage: func(items []gjson.Result, _ int) (string, error) { return render(items) }}
}
