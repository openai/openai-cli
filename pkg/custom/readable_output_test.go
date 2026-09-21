package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func readableStream(t *testing.T, events []string, kind OutputKind) (string, error) {
	t.Helper()
	file := outputFile(t)
	iter := &transformTestIterator{}
	for _, event := range events {
		iter.items = append(iter.items, outputJSON{gjson.Parse(event)})
	}
	err := ShowJSONIterator(iter, -1, ShowJSONOpts{Stdout: file, OutputKind: kind})
	_, seekErr := file.Seek(0, io.SeekStart)
	require.NoError(t, seekErr)
	data, readErr := io.ReadAll(file)
	require.NoError(t, readErr)
	return string(data), err
}

func TestReadableStreamSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []string
		want   string
	}{
		{"deltas and completion", []string{
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hello"}`,
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":" world"}`,
			`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"Hello world"}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"Hello world"}]}]}}`,
		}, "Hello world\n"},
		{"snapshot without deltas", []string{
			`{"type":"response.output_text.done","output_index":0,"text":"Complete answer"}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"Complete answer"}]}]}}`,
		}, "Complete answer\n"},
		{"multiple parts and original indexes", []string{
			`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"First"}`,
			`{"type":"response.output_text.delta","output_index":1,"content_index":1,"delta":"Second"}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","content":[{"type":"output_text","text":"First"},{"type":"output_text","text":"Second"}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"reasoning","summary":[]},{"type":"message","content":[{"type":"output_text","text":"First"},{"type":"output_text","text":"Second"}]}]}}`,
		}, "First\n\nSecond\n"},
		{"changed snapshot preserved", []string{
			`{"type":"response.output_text.delta","delta":"Draft"}`,
			`{"type":"response.output_text.done","text":"Final"}`,
		}, "Draft\n\nFinal\n"},
		{"independent identical parts", []string{
			`{"type":"response.output_text.delta","content_index":0,"delta":"Same"}`,
			`{"type":"response.output_text.delta","content_index":1,"delta":"Same"}`,
		}, "Same\n\nSame\n"},
		{"controls escaped", []string{
			`{"type":"response.output_text.delta","delta":"\u001b]52;c;secret\u0007"}`,
		}, `\u001b]52;c;secret\u0007` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readableStream(t, tc.events, OutputStreamEvent)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestReadableStreamKeepsUsageAndUnknownEvents(t *testing.T) {
	got, err := readableStream(t, []string{
		`{"type":"response.output_text.delta","delta":"Hello"}`,
		`{"type":"future.progress","detail":"Useful status"}`,
		`{"type":"response.output_text.done","text":"Hello"}`,
		`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":12,"output_tokens":3}}}`,
	}, OutputStreamEvent)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(got, "Hello"))
	require.Contains(t, got, "Type: future.progress")
	require.Contains(t, got, "Detail: Useful status")
	require.Contains(t, got, "Usage:")
	require.Contains(t, got, "Input tokens: 12")
	require.Contains(t, got, "Output tokens: 3")
}

func TestReadablePageKeepsIdenticalRecords(t *testing.T) {
	item := `{"id":"cmpl_test","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Same answer"}}]}`
	got, err := readableStream(t, []string{item, item}, OutputPageItem)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(got, "ID: cmpl_test"))
	require.Equal(t, 2, strings.Count(got, "Same answer"))
}

func TestReadableStreamPropagatesFailuresAndStopsReading(t *testing.T) {
	t.Run("writer failure", func(t *testing.T) {
		file := outputFile(t)
		require.NoError(t, file.Close())
		iter := &transformTestIterator{items: []any{outputJSON{gjson.Parse(`{"id":"first"}`)}, outputJSON{gjson.Parse(`{"id":"second"}`)}}}
		err := ShowJSONIterator(iter, -1, ShowJSONOpts{Stdout: file})
		require.ErrorIs(t, err, os.ErrClosed)
		require.Equal(t, 1, iter.calls)
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		iter := &transformTestIterator{items: []any{outputJSON{gjson.Parse(`{"id":"first"}`)}}}
		err := ShowJSONIterator(iter, -1, ShowJSONOpts{Context: ctx, Stdout: outputFile(t)})
		require.ErrorIs(t, err, context.Canceled)
		require.Zero(t, iter.calls)
	})
	t.Run("source error", func(t *testing.T) {
		failure := errors.New("synthetic stream failure")
		iter := &transformTestIterator{err: failure}
		err := ShowJSONIterator(iter, -1, ShowJSONOpts{Stdout: outputFile(t)})
		require.ErrorIs(t, err, failure)
	})
	t.Run("no results", func(t *testing.T) {
		got, err := readableStream(t, nil, OutputPageItem)
		require.NoError(t, err)
		require.Equal(t, "No results.\n", got)
	})
}
