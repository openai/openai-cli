package batches

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func batchInputsTestCommand(t *testing.T, server string, out io.Writer, options Options) *cli.Command {
	root := batchDownloadTestCommand(t, server, out)
	root.Flags = append(root.Flags, &cli.BoolFlag{Name: "quiet"}, &cli.BoolFlag{Name: "debug"}, &cli.StringFlag{Name: "format-error"}, &cli.StringFlag{Name: "transform-error"})
	root.Commands[0].Commands = []*cli.Command{(workflow{options: options}).newBatchInputsCommand()}
	return root
}

func TestBatchInputsSearchPaginatesAndPreservesData(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/files", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "batch", r.URL.Query().Get("purpose"))
		require.Equal(t, "desc", r.URL.Query().Get("order"))
		require.Equal(t, "synthetic-context", r.Header.Get("X-Batch-Test"))
		cursor := r.URL.Query().Get("after")
		cursors = append(cursors, cursor)
		w.Header().Set("Content-Type", "application/json")
		if cursor == "" {
			io.WriteString(w, `{"data":[{"id":"file_other","filename":"other.jsonl","purpose":"batch"}],"has_more":true}`)
			return
		}
		io.WriteString(w, `{"data":[{"id":"file_match","filename":"資料-ReQuEsTs.jsonl","purpose":"batch","bytes":9007199254740993,"future":{"value":null}},{"id":"file_wrong_purpose","filename":"requests.jsonl","purpose":"assistants"}],"has_more":false}`)
	}))
	defer server.Close()
	var out bytes.Buffer
	options := testOptions()
	parse := options.RequestOptions
	parsed := 0
	options.RequestOptions = func(cmd *cli.Command, input io.Reader) ([]option.RequestOption, error) {
		parsed++
		return parse(cmd, input)
	}
	root := batchInputsTestCommand(t, server.URL, &out, options)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--header", "X-Batch-Test: synthetic-context", "batches", "inputs", "--search", "requests", "--max-items", "-1"}))
	require.Equal(t, 1, parsed)
	require.Equal(t, []string{"", "file_other"}, cursors)
	value := gjson.ParseBytes(out.Bytes())
	require.Len(t, value.Array(), 1)
	require.Equal(t, "file_match", value.Get("0.id").Str)
	require.Equal(t, "9007199254740993", value.Get("0.bytes").Raw)
	require.True(t, value.Get("0.future.value").Exists())
	require.Equal(t, gjson.Null, value.Get("0.future.value").Type)
}

func TestBatchInputsLimitAndPreflight(t *testing.T) {
	for _, arguments := range [][]string{{"--max-items", "-2"}, {"--select"}, {"extra"}} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			called := 0
			options := testOptions()
			options.RequestOptions = func(*cli.Command, io.Reader) ([]option.RequestOption, error) { called++; return nil, nil }
			root := batchInputsTestCommand(t, "http://127.0.0.1:1", io.Discard, options)
			require.Error(t, root.Run(t.Context(), append([]string{"openai", "batches", "inputs"}, arguments...)))
			require.Zero(t, called)
		})
	}
}

func TestBatchInputSelectionOutputPolicy(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		allowed bool
	}{
		{nil, true}, {[]string{"--format", "json"}, false}, {[]string{"--format", "jsonl"}, false},
		{[]string{"--format", "json", "--format-error", "text"}, true},
		{[]string{"--transform-error", "message"}, false}, {[]string{"--format-error", "json"}, false}, {[]string{"--quiet"}, false}, {[]string{"--debug"}, false},
	} {
		root := &cli.Command{Name: "openai", Flags: []cli.Flag{&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "format-error"}, &cli.StringFlag{Name: "transform-error"}, &cli.BoolFlag{Name: "quiet"}, &cli.BoolFlag{Name: "debug"}},
			Action: func(_ context.Context, cmd *cli.Command) error {
				require.Equal(t, tc.allowed, batchInputSelectionAllowed(cmd, true, true))
				require.False(t, batchInputSelectionAllowed(cmd, false, true))
				require.False(t, batchInputSelectionAllowed(cmd, true, false))
				return nil
			}}
		require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, tc.args...)))
	}
}

func TestBatchPageCollectionFailuresAndLimits(t *testing.T) {
	for _, tc := range []struct {
		body    string
		maximum int64
		length  int
		fail    bool
	}{
		{`{"data":[],"has_more":true}`, -1, 0, true},
		{`{"data":[{"id":"same"}],"has_more":true}`, -1, 2, true},
		{`{"data":[{"id":"one"},{"id":"two"}],"has_more":true}`, 1, 1, false},
		{`{"data":[{"id":"one"}],"has_more":true}`, 0, 0, false},
		{`{"data":[{}],"has_more":false}`, -1, 0, true},
		{`{"data":[],"has_more":"false"}`, -1, 0, true},
		{`{"data":[`, -1, 0, true},
	} {
		calls := 0
		items, err := batchFetchInputTestPages(t, t.Context(), "", tc.maximum, func(string) ([]byte, error) { calls++; require.Less(t, calls, 4); return []byte(tc.body), nil }, nil)
		require.Equal(t, tc.fail, err != nil, tc.body)
		require.Len(t, items, tc.length, tc.body)
	}
	want := errors.New("synthetic second-page failure")
	calls := 0
	items, err := batchFetchInputTestPages(t, t.Context(), "", -1, func(string) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, want
		}
		return []byte(`{"data":[{"id":"one"}],"has_more":true}`), nil
	}, nil)
	require.ErrorIs(t, err, want)
	require.Len(t, items, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = batchFetchInputTestPages(t, ctx, "", -1, func(string) ([]byte, error) { t.Fatal("request after cancellation"); return nil, nil }, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func batchFetchInputTestPages(t *testing.T, ctx context.Context, after string, maximum int64, fetch func(string) ([]byte, error), keep func(gjson.Result) bool) ([]gjson.Result, error) {
	t.Helper()
	require.Empty(t, after)
	store, err := newBatchRecordStore(t.Context())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.close()) }()
	files := &batchInputCollection{store: store}
	fetchErr := files.fetch(ctx, maximum, fetch, keep)
	var items []gjson.Result
	for id := int64(1); id <= store.recordCount; id++ {
		item, err := files.item(ctx, id)
		require.NoError(t, err)
		items = append(items, item)
	}
	return items, fetchErr
}
