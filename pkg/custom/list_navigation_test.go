package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/tidwall/gjson"
)

func TestInteractiveListOutputScope(t *testing.T) {
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		t.Skip("run the compiled test executable with terminal stdin and stdout")
	}
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	for operation, want := range map[string]bool{
		"(resource) files > (method) list":                       true,
		"(resource) batches > (method) list":                     true,
		"(resource) admin.organization.projects > (method) list": true,
		"(resource) vector_stores > (method) list":               false,
		"(resource) models > (method) list":                      false,
		"(resource) files > (method) retrieve":                   false,
		"unknown":                                                false,
		"":                                                       false,
	} {
		for _, format := range []string{"", "auto"} {
			t.Run(operation+"/"+format, func(t *testing.T) {
				if got := InteractiveListOutput(ShowJSONOpts{Operation: operation, OutputKind: OutputPageItem, Format: format}); got != want {
					t.Fatalf("got %v, want %v", got, want)
				}
			})
		}
	}
	for _, mode := range []string{"text", "json", "raw", "extraction", "raw-output", "CI", "dumb", "redirected", "resource"} {
		t.Run("bypass/"+mode, func(t *testing.T) {
			opts := ShowJSONOpts{Operation: "(resource) files > (method) list", OutputKind: OutputPageItem}
			switch mode {
			case "text", "json", "raw":
				opts.Format = mode
			case "extraction":
				opts.Transform = "id"
			case "raw-output":
				opts.RawOutput = true
			case "CI":
				t.Setenv("CI", "true")
			case "dumb":
				t.Setenv("TERM", "dumb")
			case "redirected":
				opts.Stdout = io.Discard
			case "resource":
				opts.OutputKind = OutputResponse
			}
			if InteractiveListOutput(opts) {
				t.Fatal("navigation intercepted an existing bypass")
			}
		})
	}
}

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
			if len(page.items) != want {
				t.Fatalf("got %d items, want %d", len(page.items), want)
			}
			wantRequests := 1
			if limit == 0 {
				wantRequests = 0
			}
			if requests != wantRequests {
				t.Fatalf("first load made %d requests", requests)
			}
			if page.more != (limit == -1 || limit > 2) {
				t.Fatalf("unexpected continuation: %v", page.more)
			}
			page, err = fetch()
			if err != nil {
				t.Fatal(err)
			}
			if limit == -1 || limit > 2 {
				wantRequests++
				if len(page.items) != 1 || page.items[0].Get("id").String() != "c" {
					t.Fatalf("unexpected next page: %+v", page)
				}
			}
			if requests != wantRequests || page.more {
				t.Fatalf("unexpected final load: requests=%d, more=%v", requests, page.more)
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
		err := ShowJSONPages(func(context.Context) (*navigationTestPage, error) {
			calls++
			return nil, requestErr
		}, 0, ShowJSONOpts{}, "")
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
			if page.more && err == nil {
				page, err = fetch()
			}
			if (err != nil) != tt.wantError {
				t.Fatalf("error=%v", err)
			}
			var navigationError *listNavigationError
			if tt.wantError && !errors.As(err, &navigationError) {
				t.Fatalf("cursor failure has no safe diagnostic type: %v", err)
			}
			if page.more {
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	m := &listNavigation{opts: ShowJSONOpts{Context: ctx}, cancel: cancel,
		viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(3)),
		pages:    []listNavigationPage{{items: []gjson.Result{gjson.Parse(`{"id":"first"}`)}, more: true}},
		fetch: func() (listNavigationPage, error) {
			requests.Add(1)
			close(started)
			<-release
			return listNavigationPage{items: []gjson.Result{gjson.Parse(`{"id":"second"}`)}}, nil
		}}
	m.viewport.SetContent("one\ntwo\nthree\nfour\nfive\nsix\nseven")
	space := tea.KeyPressMsg{Code: ' ', Text: " "}
	_, cmd := m.Update(space)
	if cmd != nil || requests.Load() != 0 || m.viewport.YOffset() == 0 {
		t.Fatal("first Space did not navigate loaded rows")
	}
	for !m.viewport.AtBottom() {
		m.Update(space)
	}
	_, cmd = m.Update(space)
	if cmd == nil {
		t.Fatal("Space at bottom did not request next page")
	}
	<-started
	for range 20 {
		if _, next := m.Update(space); next != nil {
			t.Fatal("duplicate page command")
		}
	}
	if requests.Load() != 1 {
		t.Fatal("duplicate page request")
	}
	close(release)
	m.Update(cmd())
	if len(m.pages) != 2 || m.index != 1 {
		t.Fatal("did not retain loaded pages")
	}
	m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if m.index != 0 || !strings.Contains(m.viewport.GetContent(), "first") {
		t.Fatal("did not navigate previous loaded page")
	}
	if requests.Load() != 1 {
		t.Fatal("previous page caused a request")
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
	out := &listNavigationOutput{terminalOutputWriter: terminalOutputWriter{
		outputWriter: outputWriter{ctx: context.Background(), out: sink},
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

func TestListNavigationOutputFailureDiagnostic(t *testing.T) {
	outputFailure := &os.PathError{Op: "write", Path: "/synthetic-private/output", Err: io.ErrClosedPipe}
	pageFailure := &listNavigationError{}
	joinedFailure := errors.Join(pageFailure, context.Canceled)
	const outputMessage = "A local file could not be read or written. Check your file arguments and permissions."
	const timeoutMessage = "The request timed out. The API may have received it; check its status before repeating it."
	for _, tt := range []struct {
		name       string
		callerErr  error
		fetchError func(error) error
		modelError error
		runError   error
		wantCause  error
		want       string
	}{
		{name: "fetch canceled", want: outputMessage},
		{name: "fetch wrapped", fetchError: func(err error) error { return fmt.Errorf("request: %w", err) }, want: outputMessage},
		{name: "fetch finished", fetchError: func(error) error { return nil }, want: outputMessage},
		{name: "fetch joined", fetchError: func(err error) error {
			return fmt.Errorf("request: %w", errors.Join(pageFailure, err))
		}, wantCause: pageFailure, want: pageFailure.Error()},
		{name: "model joined", modelError: errors.Join(pageFailure, context.Canceled), wantCause: pageFailure, want: pageFailure.Error()},
		{name: "received fetch failure", modelError: joinedFailure, fetchError: func(error) error { return joinedFailure }, wantCause: pageFailure, want: pageFailure.Error()},
		{name: "program joined", runError: io.ErrUnexpectedEOF, wantCause: io.ErrUnexpectedEOF, want: outputMessage},
		{name: "request timeout", fetchError: func(error) error { return context.DeadlineExceeded }, wantCause: context.DeadlineExceeded, want: timeoutMessage},
		{name: "caller canceled", callerErr: context.Canceled, wantCause: context.Canceled, want: "Request canceled."},
		{name: "caller timeout", callerErr: context.DeadlineExceeded, wantCause: context.DeadlineExceeded, want: timeoutMessage},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				caller := context.Background()
				if tt.callerErr == context.Canceled {
					var cancel context.CancelFunc
					caller, cancel = context.WithCancel(caller)
					cancel()
				} else if tt.callerErr == context.DeadlineExceeded {
					var cancel context.CancelFunc
					caller, cancel = context.WithDeadline(caller, time.Unix(1, 0))
					defer cancel()
				}
				request, cancel := context.WithCancel(caller)
				defer cancel()
				program, stop := context.WithCancel(caller)
				defer stop()
				m := &listNavigation{opts: ShowJSONOpts{Context: caller}, cancel: cancel, err: tt.modelError,
					fetch: func() (listNavigationPage, error) {
						<-request.Done()
						err := request.Err()
						if tt.fetchError != nil {
							err = tt.fetchError(err)
						}
						return listNavigationPage{}, err
					}}
				m.load()
				out := &listNavigationOutput{terminalOutputWriter: terminalOutputWriter{
					outputWriter: outputWriter{ctx: context.Background(), out: navigationWriteFunc(func([]byte) (int, error) {
						return 0, outputFailure
					})},
				}, stop: stop}
				out.Write([]byte("partial result"))
				failure := m.finish(out, errors.Join(tea.ErrProgramKilled, program.Err(), tt.runError))
				if !errors.Is(failure, outputFailure) || tt.wantCause != nil && !errors.Is(failure, tt.wantCause) {
					t.Fatalf("lost original failure: %v", failure)
				}
				if tt.callerErr == nil && errors.Is(failure, context.Canceled) {
					t.Errorf("cleanup became caller cancellation: %v", failure)
				}
				if tt.wantCause == pageFailure && strings.Count(failure.Error(), pageFailure.Error()) != 1 {
					t.Errorf("duplicated fetch failure: %v", failure)
				}
				var output bytes.Buffer
				if err := ShowCommandError(readableErrorTestCommand(t, "--format-error", format), failure, &output); err != nil {
					t.Fatal(err)
				}
				got := strings.TrimSpace(output.String())
				if format == "json" {
					var payload struct{ Message string }
					if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					got = payload.Message
				}
				if got != tt.want {
					t.Fatalf("diagnostic %q, want %q", got, tt.want)
				}
			})
		}
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
			m := &listNavigation{opts: ShowJSONOpts{Context: ctx}, cancel: cancel, loading: true}
			m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
			m.Update(listPageMessage{err: tt.received})
			if tt.want == nil && m.err != nil || tt.want != nil && !errors.Is(m.err, tt.want) {
				t.Fatalf("got %v, want %v", m.err, tt.want)
			}
			failure := m.finish(&listNavigationOutput{}, tea.ErrProgramKilled)
			if tt.want == nil && failure != nil || tt.want != nil && !errors.Is(failure, tt.want) {
				t.Fatalf("shutdown returned %v, want %v", failure, tt.want)
			}
		})
	}
}

func TestListNavigationLargeFieldPreservedAndPrewrapped(t *testing.T) {
	const size = 8 << 20
	value := gjson.Parse(`{"filename":"` + strings.Repeat("x", size) + `"}`)
	m := &listNavigation{opts: ShowJSONOpts{Context: context.Background()},
		viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		pages:    []listNavigationPage{{items: []gjson.Result{value}}}}
	if err := m.render(); err != nil {
		t.Fatal(err)
	}
	content := m.viewport.GetContent()
	if strings.Count(content, "x") != size {
		t.Fatal("large field was truncated")
	}
	for line := range strings.SplitSeq(content, "\n") {
		if len(line) > 80 {
			t.Fatal("large field was not prewrapped")
		}
	}
}

func TestListNavigationPrintPageCancelsWithoutFetching(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		m := &listNavigation{opts: ShowJSONOpts{Context: context.Background()}, cancel: cancel, loading: true,
			fetch: func() (listNavigationPage, error) {
				t.Fatal("printing caused a request")
				return listNavigationPage{}, nil
			}}
		if loaded {
			m.pages = []listNavigationPage{{items: []gjson.Result{gjson.Parse(`{"id":"current"}`)}, more: true}}
		}
		_, command := m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
		if m.printPage != loaded || m.quitting != loaded || (command != nil) != loaded || (ctx.Err() != nil) != loaded {
			t.Fatalf("loaded=%v: print=%v, quitting=%v, command=%v, canceled=%v", loaded, m.printPage, m.quitting, command != nil, ctx.Err())
		}
		cancel()
	}
}

func TestListNavigationLabelsPreserveLongIDs(t *testing.T) {
	id := "file-" + strings.Repeat("synthetic", 8)
	value := gjson.Parse(`{"id":"` + id + `","filename":"safe\u001b[2J.txt"}`)
	for _, width := range []int{20, 40} {
		content, err := renderListNavigationPage(ShowJSONOpts{Context: context.Background()}, []gjson.Result{value}, width)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(content, "ID: "+id+"\n") || strings.ContainsRune(content, '\x1b') {
			t.Fatalf("width %d altered the ID or retained a terminal control: %q", width, content)
		}
	}
}

func TestListNavigationNarrowFooterKeepsQuitAndPrintVisible(t *testing.T) {
	for width := 20; width <= 40; width++ {
		for _, state := range []string{"more", "loading", "last"} {
			m := &listNavigation{viewport: viewport.New(viewport.WithWidth(width), viewport.WithHeight(20)),
				loading: state == "loading", pages: []listNavigationPage{{more: state != "last"}}}
			content := m.View().Content
			if !strings.Contains(content, "q: quit") || !strings.Contains(content, "p: print page, quit") {
				t.Fatalf("width %d, state %s hides controls: %q", width, state, content)
			}
		}
	}
}

func TestListNavigationErrorPresentation(t *testing.T) {
	origin := &listNavigationError{}
	for name, failure := range map[string]error{
		"direct":   origin,
		"wrapped":  fmt.Errorf("synthetic-private-cursor: %w", origin),
		"joined":   errors.Join(errors.New("synthetic-private-cursor"), origin),
		"canceled": errors.Join(origin, context.Canceled),
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(name+"/"+format, func(t *testing.T) {
				root := readableErrorTestCommand(t, "--format-error", format)
				var output bytes.Buffer
				if err := ShowCommandError(root, failure, &output); err != nil {
					t.Fatal(err)
				}
				want := origin.Error()
				if name == "canceled" {
					want = "Request canceled."
				}
				got := strings.TrimSpace(output.String())
				if format == "json" {
					var payload struct{ Message string }
					if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					got = payload.Message
				}
				if got != want || !errors.Is(failure, origin) {
					t.Fatalf("diagnostic %q, want %q; original identity=%v", got, want, errors.Is(failure, origin))
				}
				writeFailure := errors.New("synthetic diagnostic write failure")
				err := ShowCommandError(root, failure, navigationWriteFunc(func([]byte) (int, error) { return 0, writeFailure }))
				if !errors.Is(err, writeFailure) {
					t.Fatalf("lost diagnostic write failure: %v", err)
				}
			})
		}
	}
}

func TestListNavigationCompleteFirstPageFitsScreen(t *testing.T) {
	for _, tt := range []struct {
		name          string
		width, height int
		items         []gjson.Result
		more, want    bool
	}{
		{"single row", 40, 2, []gjson.Result{gjson.Parse(`{"id":"a"}`)}, false, true},
		{"empty result", 40, 2, nil, false, true},
		{"empty tiny terminal", 40, 1, nil, false, false},
		{"unknown width", 0, 20, nil, false, false},
		{"unknown height", 40, 0, nil, false, false},
		{"exact fit with separator", 40, 4, []gjson.Result{gjson.Parse(`{"id":"a"}`), gjson.Parse(`{"id":"b"}`)}, false, true},
		{"one row over", 40, 3, []gjson.Result{gjson.Parse(`{"id":"a"}`), gjson.Parse(`{"id":"b"}`)}, false, false},
		{"wide characters fit", 5, 3, []gjson.Result{gjson.Parse(`{"id":"漢字"}`)}, false, true},
		{"wide characters overflow", 5, 2, []gjson.Result{gjson.Parse(`{"id":"漢字"}`)}, false, false},
		{"tabbed filename fits", 20, 3, []gjson.Result{gjson.Parse(`{"id":"file_a","filename":"a\t1234"}`)}, false, true},
		{"tabbed filename overflows", 20, 3, []gjson.Result{gjson.Parse(`{"id":"file_a","filename":"a\t123456789"}`)}, false, false},
		{"tabbed filename taller terminal", 20, 4, []gjson.Result{gjson.Parse(`{"id":"file_a","filename":"a\t123456789"}`)}, false, true},
		{"tab cancels pending wrap", 20, 3, []gjson.Result{gjson.Parse(`{"id":"file_a","filename":"abcdefghij\tZ"}`)}, false, true},
		{"text after margin tab wraps", 20, 3, []gjson.Result{gjson.Parse(`{"id":"file_a","filename":"abcdefghij\tYZ"}`)}, false, false},
		{"more API pages", 40, 20, []gjson.Result{gjson.Parse(`{"id":"a"}`)}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := &listNavigation{opts: ShowJSONOpts{Context: context.Background()}, cancel: cancel,
				window:   tea.WindowSizeMsg{Width: tt.width, Height: tt.height},
				viewport: viewport.New(viewport.WithWidth(max(1, tt.width)), viewport.WithHeight(max(1, tt.height-2)))}
			m.viewport.SoftWrap = false
			_, command := m.Update(listPageMessage{page: listNavigationPage{items: tt.items, more: tt.more}})
			if m.err != nil {
				t.Fatal(m.err)
			}
			if m.printComplete != tt.want || m.quitting != tt.want || (command != nil) != tt.want || (ctx.Err() != nil) != tt.want {
				t.Fatalf("print=%v, quitting=%v, command=%v, canceled=%v; want %v", m.printComplete, m.quitting, command != nil, ctx.Err(), tt.want)
			}
			if tt.want && m.View().Content != "" {
				t.Fatal("completed result would be printed both by Tea and after restoration")
			}
			if tt.want {
				for _, item := range tt.items {
					if !strings.Contains(m.content, "ID: "+item.Get("id").String()+"\n") {
						t.Fatalf("original logical ID was wrapped in final output: %q", m.content)
					}
				}
			}
		})
	}
}

func TestListNavigationContentFitsDisplayCells(t *testing.T) {
	for _, tt := range []struct {
		name, content string
		width, height int
		want          bool
	}{
		{"tab exactly fills row", "a\t1234\n", 12, 2, true},
		{"tab adds wrapped row", "a\t12345\n", 12, 2, false},
		{"tab after wide grapheme", "界\t12345\n", 12, 2, false},
		{"tab after combining grapheme", "e\u0301\t12345\n", 12, 2, false},
		{"tab after wrapped text", "abcdefghijklmn\t12345\n", 12, 3, false},
		{"tab stops at right margin", "a\t\tX\n", 12, 2, true},
		{"tab clears pending wrap", "abcdefghijkl\tX\n", 12, 2, true},
		{"text wraps after margin tab", "abcdefghijkl\tXY\n", 12, 2, false},
		{"color has no display width", "\x1b[31ma\x1b[0m\t1234\n", 12, 2, true},
		{"blank separator counts", "first\n\nlast\n", 12, 3, false},
		{"prompt row remains", "first\n\nlast\n", 12, 4, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := listNavigationContentFits(tt.content, tt.width, tt.height); got != tt.want {
				t.Fatalf("fit=%v, want %v for %q in %dx%d", got, tt.want, tt.content, tt.width, tt.height)
			}
		})
	}
}

func TestListNavigationShortResultPreservesErrorsAndHistory(t *testing.T) {
	failure := errors.New("synthetic first-page failure")
	for _, state := range []string{"request error", "render canceled", "later page", "resized viewer"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := &listNavigation{opts: ShowJSONOpts{Context: ctx}, cancel: cancel,
				window:   tea.WindowSizeMsg{Width: 40, Height: 20},
				viewport: viewport.New(viewport.WithWidth(40), viewport.WithHeight(18))}
			page := listNavigationPage{items: []gjson.Result{gjson.Parse(`{"id":"a"}`)}}
			message := listPageMessage{page: page}
			switch state {
			case "request error":
				message.err = failure
			case "render canceled":
				cancel()
			case "later page":
				m.pages = []listNavigationPage{{items: []gjson.Result{gjson.Parse(`{"id":"earlier"}`)}, more: true}}
			case "resized viewer":
				m.pages = []listNavigationPage{page}
				m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
				if m.printComplete || m.quitting {
					t.Fatal("resizing exited an established viewer")
				}
				return
			}
			m.Update(message)
			if m.printComplete {
				t.Fatal("auto-exited despite an error or existing history")
			}
			if state == "request error" && !errors.Is(m.err, failure) {
				t.Fatal("lost first-page error", m.err)
			}
			if state == "render canceled" && !errors.Is(m.err, context.Canceled) {
				t.Fatal("lost render cancellation", m.err)
			}
			if state == "later page" {
				m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
				if m.index != 0 || !strings.Contains(m.viewport.GetContent(), "earlier") {
					t.Fatal("lost backward navigation after a short final page")
				}
			}
		})
	}
}
