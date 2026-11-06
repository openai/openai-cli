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
	m.Update(listPageMessage{page: Page{Items: []gjson.Result{gjson.Parse(`{"a":"one","b":"two","c":"three","d":"four","e":"five","f":"six"}`)}, More: true}})
	if cmd := viewerKey(m, ' '); cmd != nil || requests.Load() != 0 || !strings.Contains(m.View().Content, "six") {
		t.Fatal("Space must display the remaining loaded rows without fetching")
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
	page := viewerPage("first", "second")
	page.More = true
	m.Update(listPageMessage{page: page})
	for _, key := range []rune{'p', 'i', 'b'} {
		if viewerKey(m, key) != nil {
			t.Fatal("unsupported key or backward boundary started work")
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 4})
	if len(m.records.pages) != 1 || m.loading || m.quitting || !strings.Contains(m.View().Content, "first") {
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

func TestListNavigationShortCompletePagePrintsOnce(t *testing.T) {
	for _, items := range [][]gjson.Result{nil, viewerPage("first").Items, viewerPage("first", "second").Items} {
		var out bytes.Buffer
		opts := testOptions(t.Context())
		opts.Stdout = &out
		m := New(opts, nil, func() {}, 80, 20)
		m.Update(listPageMessage{page: Page{Items: items}})
		if !m.quitting || m.View().AltScreen || out.Len() != 0 {
			t.Fatal("a short result should print after the inline view releases the terminal")
		}
		if err := m.Finish(nil, nil); err != nil {
			t.Fatal(err)
		}
		want := "No results.\n"
		if len(items) != 0 {
			var records []string
			for _, item := range items {
				records = append(records, "ID: "+item.Get("id").String())
			}
			want = strings.Join(records, "\n") + "\n"
		}
		if out.String() != want {
			t.Fatalf("printed %q, want %q", out.String(), want)
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
	first := viewerPage("first")
	first.More = true
	m.Update(listPageMessage{page: first})
	m.Update(listPageMessage{})
	if m.quitting || len(m.records.pages) != 1 || !strings.Contains(m.View().Content, "first") || !strings.Contains(m.View().Content, "End") {
		t.Fatal("empty final response lost or duplicated loaded results")
	}
}
func testOptions(ctx context.Context) Options {
	render := func(items []gjson.Result) (string, error) {
		return RenderLabels(items, transformers.Identity, clioutput.ReadableOptions{Context: ctx, DiagnosticsAllowed: true})
	}
	return Options{
		Context:            ctx,
		RenderPage:         func(items []gjson.Result, _ int) (string, error) { return render(items) },
		RenderTable:        func([]gjson.Result, int) (string, bool, error) { return "", false, nil },
		RenderRecordLabels: func(item gjson.Result, _ bool) (string, error) { return render([]gjson.Result{item}) },
	}
}
