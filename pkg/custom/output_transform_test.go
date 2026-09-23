package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func outputFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	t.Cleanup(func() { file.Close() })
	return file
}

func readOutput(t *testing.T, file *os.File) string {
	t.Helper()
	data, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	return string(data)
}

func TestShowJSONTransformsOnceBeforeFormatFallback(t *testing.T) {
	t.Setenv("FORCE_COLOR", "0")
	for _, format := range []string{"auto", "explore", "json", "jsonl", "raw", "pretty", "yaml"} {
		t.Run(format, func(t *testing.T) {
			file := outputFile(t)
			ctx := context.WithValue(context.Background(), struct{}{}, "request")
			selected, transformed := 0, 0
			selector := func(route transformers.Route) transformers.Transformer {
				selected++
				require.Equal(t, transformers.Route{Operation: "images.generate", OutputKind: OutputResponse}, route)
				return func(got context.Context, value gjson.Result) (gjson.Result, error) {
					transformed++
					require.Same(t, ctx, got)
					require.Equal(t, "original", value.Get("id").String())
					return gjson.Parse(`{"id":"transformed"}`), nil
				}
			}
			err := showJSON(gjson.Parse(`{"id":"original"}`), ShowJSONOpts{
				Context: ctx, Operation: "images.generate", OutputKind: OutputResponse,
				Format: format, Stdout: file, Stderr: io.Discard,
			}, selector)
			require.NoError(t, err)
			require.Equal(t, 1, selected)
			require.Equal(t, 1, transformed)
			require.Contains(t, readOutput(t, file), "transformed")
		})
	}
}

func TestShowJSONExplicitOutputAndErrorsBypassDefaults(t *testing.T) {
	t.Setenv("FORCE_COLOR", "0")
	for _, test := range []struct {
		name string
		opts ShowJSONOpts
		want string
	}{
		{"explicit JSON", ShowJSONOpts{Format: "JSON", ExplicitFormat: true}, "{\n  \"id\": \"original\"\n}\n"},
		{"explicit raw format", ShowJSONOpts{Format: "raw", ExplicitFormat: true}, "{\"id\":\"original\"}\n"},
		{"GJSON", ShowJSONOpts{Format: "raw", Transform: "id"}, "\"original\"\n"},
		{"raw string", ShowJSONOpts{Format: "auto", Transform: "id", RawOutput: true}, "original\n"},
		{"missing GJSON path", ShowJSONOpts{Format: "raw", Transform: "absent"}, "{\"id\":\"original\"}\n"},
		{"unspecified output", ShowJSONOpts{Format: "raw", OutputKind: OutputUnspecified}, "{\"id\":\"original\"}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := test.opts
			opts.Operation = "images.generate"
			if test.name != "unspecified output" {
				opts.OutputKind = OutputResponse
			}
			opts.Stdout = outputFile(t)
			err := showJSON(gjson.Parse(`{"id":"original"}`), opts, func(transformers.Route) transformers.Transformer {
				t.Fatal("explicit output or error presentation selected a default transformer")
				return transformers.Identity
			})
			require.NoError(t, err)
			require.Equal(t, test.want, readOutput(t, opts.Stdout))
		})
	}
	for _, opts := range []ShowJSONOpts{
		{OutputKind: OutputResponse},
		{Operation: "images.generate", OutputKind: "unknown"},
	} {
		selected := selectOutputTransformer(opts, func(transformers.Route) transformers.Transformer {
			t.Fatal("incomplete routing selected a default transformer")
			return transformers.Identity
		})
		value := gjson.Parse(`{"error":"synthetic"}`)
		result, err := selected(context.Background(), value)
		require.NoError(t, err)
		require.Equal(t, value, result)
	}
}

type transformTestIterator struct {
	items []any
	index int
	calls int
	err   error
}

func (it *transformTestIterator) Next() bool {
	it.calls++
	if it.index == len(it.items) {
		return false
	}
	it.index++
	return true
}
func (it *transformTestIterator) Current() any { return it.items[it.index-1] }
func (it *transformTestIterator) Err() error   { return it.err }

func TestShowJSONIteratorTransformsLazilyAcrossPagerBoundary(t *testing.T) {
	t.Setenv("FORCE_COLOR", "0")
	file := outputFile(t)
	originalStdout := os.Stdout
	os.Stdout = file
	t.Cleanup(func() { os.Stdout = originalStdout })
	for _, kind := range []OutputKind{OutputPageItem, OutputStreamEvent} {
		t.Run(string(kind), func(t *testing.T) {
			require.NoError(t, file.Truncate(0))
			_, err := file.Seek(0, io.SeekStart)
			require.NoError(t, err)
			iter := &transformTestIterator{items: make([]any, 100)}
			for i := range iter.items {
				iter.items[i] = map[string]any{"id": i}
			}
			selected, transformed := 0, 0
			err = showJSONIterator(iter, 40, ShowJSONOpts{
				Operation: "responses.list", OutputKind: kind, Format: "jsonl", Stdout: file,
			}, func(route transformers.Route) transformers.Transformer {
				selected++
				require.Equal(t, kind, route.OutputKind)
				return func(_ context.Context, value gjson.Result) (gjson.Result, error) {
					transformed++
					return gjson.Parse(fmt.Sprintf(`{"transformed":%d}`, value.Get("id").Int())), nil
				}
			})
			require.NoError(t, err)
			require.Equal(t, 1, selected)
			require.Equal(t, 40, transformed)
			require.Equal(t, 40, iter.calls)
			lines := strings.Split(strings.TrimSpace(readOutput(t, file)), "\n")
			require.Len(t, lines, 40)
			for i, line := range lines {
				require.Equal(t, int64(i), gjson.Get(line, "transformed").Int())
			}
		})
	}
}

func TestOutputIteratorSharedExplorerBoundary(t *testing.T) {
	// The explorer reads RawJSON and can read Current repeatedly; it receives
	// the same transformed value as other formats without applying the explicit
	// GJSON presentation path (the explorer historically displays the whole item).
	source := &transformTestIterator{items: []any{map[string]any{"original": true}, map[string]any{"unvisited": true}}}
	calls := 0
	iter := &outputIterator[any]{
		source: source, context: context.Background(), remaining: 1,
		transform: func(context.Context, gjson.Result) (gjson.Result, error) {
			calls++
			return gjson.Parse(`{"nested":{"changed":true}}`), nil
		},
	}
	require.Equal(t, 0, source.calls)
	require.True(t, iter.Next())
	require.Equal(t, `{"nested":{"changed":true}}`, iter.Current().RawJSON())
	require.Equal(t, `{"nested":{"changed":true}}`, iter.Current().RawJSON())
	require.False(t, iter.Next())
	require.NoError(t, iter.Err())
	require.Equal(t, 1, calls)
	require.Equal(t, 1, source.calls)
}

func TestShowJSONIteratorPreservesErrorsAndCancellation(t *testing.T) {
	transformErr := errors.New("synthetic transform failure")
	upstreamErr := errors.New("synthetic upstream failure")
	for _, test := range []struct {
		name         string
		cancelBefore bool
		cancelDuring bool
		transformErr error
		upstreamErr  error
		closedOutput bool
		want         error
		wantCalls    int
	}{
		{name: "cancelled request", cancelBefore: true, want: context.Canceled},
		{name: "cancelled transform", cancelDuring: true, want: context.Canceled, wantCalls: 1},
		{name: "transform failure", transformErr: transformErr, want: transformErr, wantCalls: 1},
		{name: "upstream failure", upstreamErr: upstreamErr, want: upstreamErr, wantCalls: 1},
		{name: "output failure", closedOutput: true, want: os.ErrClosed, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancelBefore {
				cancel()
			}
			file := outputFile(t)
			if test.closedOutput {
				require.NoError(t, file.Close())
			}
			iter := &transformTestIterator{items: []any{map[string]any{"id": "synthetic"}}, err: test.upstreamErr}
			calls := 0
			err := showJSONIterator(iter, -1, ShowJSONOpts{
				Context: ctx, Operation: "responses.list", OutputKind: OutputPageItem, Format: "jsonl", Stdout: file,
			}, func(transformers.Route) transformers.Transformer {
				return func(_ context.Context, value gjson.Result) (gjson.Result, error) {
					calls++
					if test.cancelDuring {
						cancel()
					}
					return value, test.transformErr
				}
			})
			require.ErrorIs(t, err, test.want)
			require.Equal(t, test.wantCalls, calls)
			if test.cancelBefore {
				require.Zero(t, iter.calls)
			}
		})
	}
}

func TestShowJSONCancellationAndTransformerErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	file := outputFile(t)
	err := ShowJSON(gjson.Parse(`{"id":"original"}`), ShowJSONOpts{Context: ctx, Format: "raw", Stdout: file})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, readOutput(t, file))
	failure := errors.New("synthetic transform failure")
	err = showJSON(gjson.Parse(`{"id":"original"}`), ShowJSONOpts{
		Operation: "images.generate", OutputKind: OutputResponse, Format: "auto", Stdout: file,
	}, func(transformers.Route) transformers.Transformer {
		return func(context.Context, gjson.Result) (gjson.Result, error) { return gjson.Result{}, failure }
	})
	require.ErrorIs(t, err, failure)
	require.Empty(t, readOutput(t, file))
}

// blockingErrorIterator models a network iterator whose error is only safe to
// inspect after Next completes, as is the case during lazy explorer loading.
type blockingErrorIterator struct {
	started  chan struct{}
	release  chan struct{}
	failure  error
	err      error
	errReads atomic.Int32
}

func (it *blockingErrorIterator) Next() bool {
	close(it.started)
	<-it.release
	it.err = it.failure
	return false
}

func (it *blockingErrorIterator) Current() any { return nil }
func (it *blockingErrorIterator) Err() error {
	it.errReads.Add(1)
	return it.err
}

func TestOutputIteratorErrDoesNotWaitForOrReadActiveNext(t *testing.T) {
	failure := errors.New("synthetic lazy page failure")
	source := &blockingErrorIterator{
		started: make(chan struct{}), release: make(chan struct{}), failure: failure,
	}
	iter := &outputIterator[any]{
		source: source, context: context.Background(), remaining: -1,
		transform: transformers.Identity,
	}
	nextDone := make(chan bool, 1)
	go func() { nextDone <- iter.Next() }()
	<-source.started
	t.Cleanup(func() {
		select {
		case <-source.release:
		default:
			close(source.release)
		}
	})

	// Quitting the explorer must neither wait for a network read nor ask the
	// underlying iterator for state that its Next goroutine still owns.
	errorRead := make(chan error, 1)
	go func() { errorRead <- iter.Err() }()
	select {
	case err := <-errorRead:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Err waited for the active Next call")
	}
	require.Zero(t, source.errReads.Load(), "Err must read only the completed-step snapshot")

	close(source.release)
	require.False(t, <-nextDone)
	require.ErrorIs(t, iter.Err(), failure)
	require.ErrorIs(t, iter.Err(), failure)
	require.Equal(t, int32(1), source.errReads.Load(), "only Next may read the source error")
}
