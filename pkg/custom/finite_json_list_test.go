package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type finiteListRecord string

func (r finiteListRecord) RawJSON() string { return string(r) }

type finiteListIterator struct {
	items        []finiteListRecord
	index, calls int
	err          error
	next         func(int)
}

func (it *finiteListIterator) Next() bool {
	it.calls++
	if it.next != nil {
		it.next(it.calls)
	}
	if it.index == len(it.items) {
		return false
	}
	it.index++
	return true
}
func (it *finiteListIterator) Current() finiteListRecord { return it.items[it.index-1] }
func (it *finiteListIterator) Err() error                { return it.err }

func finiteListOpts(out io.Writer) ShowJSONOpts {
	return ShowJSONOpts{Context: context.Background(), Operation: "synthetic finite boundary", OutputKind: OutputPageItem,
		Format: "json", ExplicitFormat: true, Stdout: out}
}

func TestFiniteJSONListDocumentsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		items   []finiteListRecord
		maximum int64
		want    string
		calls   int
	}{
		{"empty", nil, -1, `[]`, 1},
		{"zero", []finiteListRecord{`{"id":"unread"}`}, 0, `[]`, 0},
		{"single", []finiteListRecord{`{"id":"one","number":9007199254740993}`}, -1, `[{"id":"one","number":9007199254740993}]`, 2},
		{"limited", []finiteListRecord{`{"id":"one"}`, `{"id":"unread"}`}, 1, `[{"id":"one"}]`, 1},
		{"unlimited", []finiteListRecord{`null`, `false`, `"string"`, `17`, `[1,2]`, `{"data":[3]}`}, -1, `[null,false,"string",17,[1,2],{"data":[3]}]`, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			iter := &finiteListIterator{items: tc.items}
			var out bytes.Buffer
			require.NoError(t, ShowJSONIterator(iter, tc.maximum, finiteListOpts(&out)))
			var got []json.RawMessage
			require.NoError(t, json.Unmarshal(out.Bytes(), &got))
			require.JSONEq(t, tc.want, out.String())
			require.Equal(t, tc.calls, iter.calls)
		})
	}
}

func TestFiniteJSONListPreservesOtherBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name         string
		kind         OutputKind
		format, path string
		raw          bool
		want         string
	}{
		{"stream", OutputStreamEvent, "json", "", false, "{\n  \"id\": \"one\"\n}\n"},
		{"unspecified", OutputUnspecified, "json", "", false, "{\n  \"id\": \"one\"\n}\n"},
		{"jsonl", OutputPageItem, "jsonl", "", false, "{\"id\":\"one\"}\n"},
		{"extraction", OutputPageItem, "json", "id", false, "\"one\"\n"},
		{"raw extraction", OutputPageItem, "json", "id", true, "one\n"},
		{"raw output", OutputPageItem, "json", "", true, "{\n  \"id\": \"one\"\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			opts := finiteListOpts(&out)
			opts.OutputKind, opts.Format, opts.Transform, opts.RawOutput = tc.kind, tc.format, tc.path, tc.raw
			require.NoError(t, ShowJSONIterator(&finiteListIterator{items: []finiteListRecord{`{"id":"one"}`}}, -1, opts))
			require.Equal(t, tc.want, out.String())
		})
	}
	var out bytes.Buffer
	opts := finiteListOpts(&out)
	opts.OutputKind = OutputResponse
	require.NoError(t, ShowJSON(gjson.Parse(`{"data":[1,2]}`), opts))
	require.JSONEq(t, `{"data":[1,2]}`, out.String())
}

func TestFiniteJSONListWritesBeforeRequestingNextItem(t *testing.T) {
	var out bytes.Buffer
	iter := &finiteListIterator{items: []finiteListRecord{`{"id":"one"}`, `{"id":"two"}`}}
	iter.next = func(call int) {
		if call == 2 {
			require.Contains(t, out.String(), `"one"`)
			require.False(t, json.Valid(out.Bytes()))
		}
	}
	require.NoError(t, ShowJSONIterator(iter, -1, finiteListOpts(&out)))
	require.JSONEq(t, `[{"id":"one"},{"id":"two"}]`, out.String())
}

func TestFiniteJSONListFailuresKeepIncompletePrefix(t *testing.T) {
	upstream := errors.New("synthetic upstream")
	for _, tc := range []string{"initial", "upstream", "malformed", "suffix", "invalid UTF-8", "canceled"} {
		t.Run(tc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var out bytes.Buffer
			iter := &finiteListIterator{items: []finiteListRecord{`{"id":"one"}`, `{"id":"two"}`}}
			opts := finiteListOpts(&out)
			opts.Context = ctx
			want := upstream
			switch tc {
			case "initial":
				iter.err = upstream
			case "upstream":
				iter.next = func(n int) {
					if n == 2 {
						iter.err = upstream
						iter.items = iter.items[:1]
					}
				}
			case "malformed":
				iter.items[1] = `{"secret":"do not print",`
				want = nil
			case "suffix":
				iter.items[1] = `{} trailing-secret`
				want = nil
			case "invalid UTF-8":
				iter.items[1] = "{\"secret\":\"\xff\"}"
				want = nil
			case "canceled":
				iter.next = func(n int) {
					if n == 2 {
						cancel()
					}
				}
				want = context.Canceled
			}
			err := ShowJSONIterator(iter, -1, opts)
			require.Error(t, err)
			if want != nil {
				require.ErrorIs(t, err, want)
				require.Equal(t, 1, strings.Count(err.Error(), want.Error()), "report each failure once")
			}
			if tc == "initial" {
				require.Empty(t, out.String())
				require.Zero(t, iter.calls)
			} else {
				require.Contains(t, out.String(), `"one"`)
				require.False(t, json.Valid(out.Bytes()))
				require.NotContains(t, out.String(), "secret")
				require.NotContains(t, err.Error(), "secret")
				require.NotContains(t, out.String(), "two")
			}
		})
	}
}

type finiteListFailWriter struct {
	bytes.Buffer
	remaining int
	failure   error
}

func (w *finiteListFailWriter) WriteString(data string) (int, error) { return w.Write([]byte(data)) }

func (w *finiteListFailWriter) Write(data []byte) (int, error) {
	if len(data) > w.remaining {
		n, _ := w.Buffer.Write(data[:w.remaining])
		w.remaining = 0
		return n, w.failure
	}
	w.remaining -= len(data)
	return w.Buffer.Write(data)
}

func TestFiniteJSONListWriteFailuresStopPagination(t *testing.T) {
	failure := errors.New("synthetic sink error")
	for _, remaining := range []int{0, 2, 10, 20, 100} {
		for _, fail := range []error{nil, failure} {
			iter := &finiteListIterator{items: []finiteListRecord{`{"id":"one"}`, `{"id":"two"}`}}
			// Also cover a short write without a supplied error.
			out := &finiteListFailWriter{remaining: remaining, failure: fail}
			err := ShowJSONIterator(iter, -1, finiteListOpts(out))
			if remaining == 100 {
				require.NoError(t, err)
				require.True(t, json.Valid(out.Bytes()))
				continue
			}
			if fail == nil {
				require.ErrorIs(t, err, io.ErrShortWrite)
			} else {
				require.ErrorIs(t, err, failure)
			}
			require.LessOrEqual(t, iter.calls, 2)
			require.False(t, json.Valid(out.Bytes()))
		}
	}
}

func TestFiniteJSONListJoinsObservedFailures(t *testing.T) {
	upstream := errors.New("synthetic upstream")
	sink := errors.New("synthetic broken pipe")
	iter := &finiteListIterator{items: []finiteListRecord{`{"id":"one"}`}}
	iter.next = func(int) { iter.err = upstream }
	out := &finiteListFailWriter{failure: sink}
	err := ShowJSONIterator(iter, -1, finiteListOpts(out))
	require.ErrorIs(t, err, upstream)
	require.ErrorIs(t, err, sink)
	require.Equal(t, 1, iter.calls)
}

type finiteListWriteFunc func([]byte) (int, error)

func (f finiteListWriteFunc) Write(data []byte) (int, error) { return f(data) }

func TestFiniteJSONListCancellationDuringBrokenPipe(t *testing.T) {
	for _, failingWrite := range []int{1, 2, 3} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		out := finiteListWriteFunc(func(data []byte) (int, error) {
			calls++
			if calls == failingWrite {
				cancel()
				return 0, syscall.EPIPE
			}
			return len(data), nil
		})
		opts := finiteListOpts(out)
		opts.Context = ctx
		err := ShowJSONIterator(&finiteListIterator{items: []finiteListRecord{`{"id":"one"}`, `{"id":"two"}`}}, -1, opts)
		cancel()
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, syscall.EPIPE)
		require.False(t, isOutputBrokenPipe(err), "cancellation must prevent EPIPE suppression")
	}
}

func TestFiniteJSONListClosingWriteFailure(t *testing.T) {
	for _, items := range [][]finiteListRecord{nil, {`{"id":"one"}`}} {
		var complete bytes.Buffer
		require.NoError(t, ShowJSONIterator(&finiteListIterator{items: items}, -1, finiteListOpts(&complete)))
		// The closing bracket reaches the sink, but its trailing newline does not.
		out := &finiteListFailWriter{remaining: complete.Len() - 1}
		err := ShowJSONIterator(&finiteListIterator{items: items}, -1, finiteListOpts(out))
		require.ErrorIs(t, err, io.ErrShortWrite)
		require.True(t, json.Valid(out.Bytes()))
	}
}

func TestFiniteJSONListTransformsOnceAndValidatesResults(t *testing.T) {
	for _, transformed := range []string{`"selected"`, `null`, `[1,2]`, `"資料-�"`, `"\ud800"`, `{"invalid":`, "\"\xff\""} {
		var out bytes.Buffer
		calls := 0
		opts := finiteListOpts(&out)
		opts.ExplicitFormat = false
		err := showJSONIterator(&finiteListIterator{items: []finiteListRecord{`{"id":"one"}`}}, -1, opts, func(transformers.Route) transformers.Transformer {
			return func(context.Context, gjson.Result) (gjson.Result, error) {
				calls++
				return gjson.Parse(transformed), nil
			}
		})
		require.Equal(t, 1, calls)
		if !utf8.ValidString(transformed) || !json.Valid([]byte(transformed)) {
			require.Error(t, err)
			require.Empty(t, out.String())
		} else {
			require.NoError(t, err)
			require.JSONEq(t, "["+transformed+"]", out.String())
		}
	}
}

func TestFiniteJSONListLargeRecordHasNoNewLimit(t *testing.T) {
	// Larger than common line-reader limits; the writer imposes no item cap.
	value := strings.Repeat("x", 2<<20)
	var out bytes.Buffer
	require.NoError(t, ShowJSONIterator(&finiteListIterator{items: []finiteListRecord{finiteListRecord(`{"value":"` + value + `"}`)}}, -1, finiteListOpts(&out)))
	var got []struct{ Value string }
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Equal(t, value, got[0].Value)
}

func TestFiniteJSONListTerminalPrefixKeepsFramingAndFailure(t *testing.T) {
	for _, height := range []int{4, 100} {
		for _, failure := range []error{nil, errors.New("synthetic later page")} {
			out := outputFile(t)
			previous := os.Stdout
			os.Stdout = out
			func() {
				defer func() { os.Stdout = previous }()
				source := &finiteListIterator{items: []finiteListRecord{`{"id":"one"}`, `{"id":"two"}`}}
				source.next = func(n int) {
					if n == 2 && failure != nil {
						source.err = failure
						source.items = source.items[:1]
					}
				}
				opts := finiteListOpts(out)
				iter := &outputIterator[finiteListRecord]{source: source, context: opts.Context, transform: transformers.Identity, remaining: -1}
				list := &finiteJSONList{iter: iter, opts: opts}
				err := list.showTerminal(80, height)
				if failure != nil {
					require.ErrorIs(t, err, failure)
					require.Contains(t, readOutput(t, out), `"one"`)
					require.False(t, json.Valid([]byte(readOutput(t, out))))
				} else {
					require.NoError(t, err)
					require.JSONEq(t, `[{"id":"one"},{"id":"two"}]`, readOutput(t, out))
				}
			}()
		}
	}
}

func TestFiniteJSONListTerminalPagerPreservesDocument(t *testing.T) {
	path := configureCapturePager(t)
	t.Setenv("NO_COLOR", "1")
	items := make([]finiteListRecord, 128)
	for i := range items {
		items[i] = `{"id":"synthetic","unknown":[1,2]}`
	}
	require.NoError(t, ShowJSONIterator(&finiteListIterator{items: items}, -1, finiteListOpts(os.Stdout)))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var actual []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &actual))
	require.Len(t, actual, len(items))
	for _, item := range actual {
		require.JSONEq(t, `[1,2]`, string(item["unknown"]))
	}
}
