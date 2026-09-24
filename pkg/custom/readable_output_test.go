package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestResolvedOutputFormat(t *testing.T) {
	for _, tc := range []struct {
		format, transform string
		raw               bool
		want              string
	}{
		{"", "", false, "text"}, {"AuTo", "", false, "text"},
		{"auto", "id", false, "json"}, {"auto", "", true, "json"},
		{"TEXT", "id", false, "text"}, {"JSONL", "", false, "jsonl"},
		{"raw", "id", true, "raw"}, {"unknown", "", false, "unknown"},
	} {
		require.Equal(t, tc.want, resolvedOutputFormat(ShowJSONOpts{Format: tc.format, Transform: tc.transform, RawOutput: tc.raw}))
	}
}

func TestReadableBoundaryExtractionAndInjectedWriters(t *testing.T) {
	value := gjson.Parse(`{"id":"first","nested":{"id":"second"},"text":"a\u001b\n"}`)
	for _, tc := range []struct {
		name string
		opts ShowJSONOpts
		want string
	}{
		{"default", ShowJSONOpts{}, "ID: first\nNested:\n  ID: second\nText: a\\u001b\n  \n"},
		{"automatic extraction", ShowJSONOpts{Format: "AUTO", Transform: "nested.id"}, "\"second\"\n"},
		{"text extraction", ShowJSONOpts{Format: "text", Transform: "id"}, "first\n"},
		{"raw bytes", ShowJSONOpts{Format: "auto", Transform: "text", RawOutput: true}, "a\x1b\n\n"},
		{"text raw bytes", ShowJSONOpts{Format: "text", Transform: "text", RawOutput: true}, "a\x1b\n\n"},
		{"yaml", ShowJSONOpts{Format: "yaml", Transform: "id"}, "first\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			tc.opts.Stdout = &out
			require.NoError(t, ShowJSON(value, tc.opts))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestReadableIteratorUsesInjectedWriterAndRetainsEveryRecord(t *testing.T) {
	var out bytes.Buffer
	iter := &transformTestIterator{items: []any{map[string]any{"id": "same"}, map[string]any{"id": "same"}, map[string]any{"id": "unread"}}}
	require.NoError(t, ShowJSONIterator(iter, 2, ShowJSONOpts{Format: "auto", Stdout: &out}))
	require.Equal(t, "ID: same\n\nID: same\n", out.String())
	require.Equal(t, 2, iter.calls)
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			out.Reset()
			source := &transformTestIterator{items: []any{map[string]any{"id": strings.Repeat("x", 5000)}, map[string]any{"id": "last"}}}
			require.NoError(t, ShowJSONIterator(source, -1, ShowJSONOpts{Format: format, Stdout: &out}))
			require.Contains(t, out.String(), "last")
		})
	}
}

func TestReadableIteratorEmptyAndZeroLimit(t *testing.T) {
	for _, format := range []string{"auto", "text"} {
		var out bytes.Buffer
		require.NoError(t, ShowJSONIterator(&transformTestIterator{}, -1, ShowJSONOpts{Format: format, Stdout: &out}))
		require.Equal(t, "No results.\n", out.String())
		source := &transformTestIterator{items: []any{"must not read"}}
		require.NoError(t, ShowJSONIterator(source, 0, ShowJSONOpts{Format: format, Stdout: failOutputWriter{}}))
		require.Zero(t, source.calls)
		failure := errors.New("upstream failed")
		out.Reset()
		require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{err: failure}, -1, ShowJSONOpts{Format: format, Stdout: &out}), failure)
		require.Empty(t, out.String())
	}
}

type failOutputWriter struct{ err error }

func (w failOutputWriter) Write([]byte) (int, error) { return 0, w.err }

type cancelOutputWriter struct {
	cancel context.CancelFunc
	writes int
}

func (w *cancelOutputWriter) Write(p []byte) (int, error) { w.writes++; w.cancel(); return len(p), nil }

func TestReadableBoundaryWriterErrorsAndCancellation(t *testing.T) {
	failure := errors.New("synthetic writer failure")
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		for _, fail := range []error{failure, nil} {
			want := fail
			if want == nil {
				want = io.ErrShortWrite
			}
			opts := ShowJSONOpts{Format: format, Stdout: failOutputWriter{fail}}
			require.ErrorIs(t, ShowJSON(gjson.Parse(`{"id":"example"}`), opts), want)
			source := &transformTestIterator{items: []any{map[string]any{"id": "one"}, map[string]any{"id": "two"}}}
			require.ErrorIs(t, ShowJSONIterator(source, -1, opts), want)
			require.Equal(t, 1, source.calls)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := &cancelOutputWriter{cancel: cancel}
	source := &transformTestIterator{items: []any{map[string]any{"id": "one"}, map[string]any{"id": "two"}}}
	require.ErrorIs(t, ShowJSONIterator(source, -1, ShowJSONOpts{Context: ctx, Stdout: out}), context.Canceled)
	require.Equal(t, 1, out.writes)
	require.Equal(t, 1, source.calls)
	var untouched bytes.Buffer
	require.ErrorIs(t, ShowJSON(gjson.Parse(`{"id":"example"}`), ShowJSONOpts{Context: ctx, Stdout: &untouched}), context.Canceled)
	require.Empty(t, untouched.String())
}

func TestReadableFormatSelectsDefaultTransformerOnce(t *testing.T) {
	for _, format := range []string{"auto", "AUTO", "text", "TEXT"} {
		var out bytes.Buffer
		calls := 0
		err := showJSON(gjson.Parse(`{"id":"original"}`), ShowJSONOpts{Format: format, ExplicitFormat: true, Operation: "example", OutputKind: OutputResponse, Stdout: &out}, func(transformers.Route) transformers.Transformer {
			calls++
			return func(context.Context, gjson.Result) (gjson.Result, error) { return gjson.Parse(`{"id":"changed"}`), nil }
		})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
		require.Equal(t, "ID: changed\n", out.String())
	}
}

func TestReadableRegistrationNormalizesFormatAndPreservesAction(t *testing.T) {
	calls := 0
	root := &cli.Command{Name: "test", Flags: []cli.Flag{&cli.StringFlag{Name: "format", Value: "auto", Action: func(context.Context, *cli.Command, string) error { calls++; return nil }}}, Action: func(_ context.Context, command *cli.Command) error {
		require.Equal(t, "raw", command.String("format"))
		require.True(t, command.IsSet("format"))
		return nil
	}}
	ConfigureCommand(root)
	ConfigureCommand(root)
	require.NoError(t, root.Run(t.Context(), []string{"test", "--format", "RaW"}))
	require.Equal(t, 1, calls)
}

func TestExploreFallbackWarningPreservesWriterFailure(t *testing.T) {
	failure := errors.New("synthetic warning write failure")
	var out bytes.Buffer
	opts := ShowJSONOpts{Format: "explore", ExplicitFormat: true, Stdout: &out, Stderr: failOutputWriter{failure}}
	require.ErrorIs(t, ShowJSON(gjson.Parse(`{"id":"example"}`), opts), failure)
	require.Empty(t, out.String())
	source := &transformTestIterator{items: []any{"must not read"}}
	require.ErrorIs(t, ShowJSONIterator(source, -1, opts), failure)
	require.Zero(t, source.calls)
}
