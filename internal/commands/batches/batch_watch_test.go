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

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func batchWatchTestCommand(t *testing.T, server string, out io.Writer, options Options, ordinary cli.ActionFunc) *cli.Command {
	root := batchDownloadTestCommand(t, server, out)
	command := &cli.Command{Name: "list", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "after", QueryPath: "after"},
		&requestflag.Flag[int64]{Name: "limit", Default: 20, QueryPath: "limit"},
		&requestflag.Flag[int64]{Name: "max-items"},
	}, Action: ordinary}
	(workflow{options: options}).configureBatchListWatch(command)
	root.Commands[0].Commands = []*cli.Command{command}
	return root
}

func TestBatchWatchSnapshotsPreserveQueriesAndRawRecords(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/batches", r.URL.Path)
		require.Equal(t, "1", r.URL.Query().Get("limit"))
		require.Equal(t, "synthetic-context", r.Header.Get("X-Batch-Test"))
		cursor := r.URL.Query().Get("after")
		cursors = append(cursors, cursor)
		w.Header().Set("Content-Type", "application/json")
		if cursor == "seed" {
			io.WriteString(w, "{\"data\":[{\n\"id\":\"batch_a\",\n\"usage\":{\"total_tokens\":9007199254740993},\"future\":null}],\"has_more\":true}")
			return
		}
		io.WriteString(w, `{"data":[{"id":"batch_b","status":"completed"}],"has_more":false}`)
	}))
	defer server.Close()
	var out bytes.Buffer
	options := testOptions()
	parse, parsed := options.RequestOptions, 0
	options.RequestOptions = func(cmd *cli.Command, input io.Reader) ([]option.RequestOption, error) {
		parsed++
		return parse(cmd, input)
	}
	root := batchWatchTestCommand(t, server.URL, &out, options, nil)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--format", "jsonl", "--header", "X-Batch-Test: synthetic-context", "batches", "list", "--watch", "--watch-count", "2", "--poll-interval", "1ns", "--limit", "1", "--after", "seed", "--max-items", "-1"}))
	require.Equal(t, 1, parsed)
	require.Equal(t, []string{"seed", "batch_a", "seed", "batch_a"}, cursors)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2, "pretty API JSON must not split JSONL records")
	for i, line := range lines {
		require.True(t, gjson.Valid(line))
		value := gjson.Parse(line)
		require.Equal(t, "batch.list_snapshot", value.Get("object").Str)
		require.Equal(t, int64(i+1), value.Get("sequence").Int())
		require.True(t, value.Get("complete").Bool())
		require.Len(t, value.Get("data").Array(), 2)
		require.Equal(t, "9007199254740993", value.Get("data.0.usage.total_tokens").Raw)
		require.True(t, value.Get("data.0.future").Exists())
	}
}

func TestBatchWatchRejectsModesBeforeParsingOrHTTP(t *testing.T) {
	for _, args := range [][]string{
		{"--format", "json"}, {"--format", "yaml"}, {"--format", "raw"}, {"--format", "pretty"},
		{"--format", "jsonl", "--transform", "id"}, {"--format", "jsonl", "--raw-output"},
	} {
		options := testOptions()
		options.RequestOptions = func(*cli.Command, io.Reader) ([]option.RequestOption, error) {
			t.Fatal("parsed unsupported watch invocation")
			return nil, nil
		}
		root := batchWatchTestCommand(t, "http://127.0.0.1:1", io.Discard, options, nil)
		arguments := append([]string{"openai"}, args...)
		require.Error(t, root.Run(t.Context(), append(arguments, "batches", "list", "--watch")))
	}
	for _, args := range [][]string{{"--poll-interval", "1s"}, {"--watch-count", "1"}} {
		root := batchWatchTestCommand(t, "http://127.0.0.1:1", io.Discard, testOptions(), func(context.Context, *cli.Command) error {
			t.Fatal("ordinary action called with watch-only flag")
			return nil
		})
		require.Error(t, root.Run(t.Context(), append([]string{"openai", "batches", "list"}, args...)))
	}
}

func TestBatchWatchOrdinaryListUnchanged(t *testing.T) {
	want := errors.New("ordinary list result")
	called := 0
	root := batchWatchTestCommand(t, "http://127.0.0.1:1", io.Discard, testOptions(), func(context.Context, *cli.Command) error { called++; return want })
	require.ErrorIs(t, root.Run(t.Context(), []string{"openai", "batches", "list", "--watch=false"}), want)
	require.Equal(t, 1, called)
}

func TestBatchWatchPartialFailureRetainsSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			io.WriteString(w, `{"data":[{"id":"batch_a"}],"has_more":true}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"synthetic failure"}}`)
	}))
	defer server.Close()
	var out bytes.Buffer
	root := batchWatchTestCommand(t, server.URL, &out, testOptions(), nil)
	require.Error(t, root.Run(t.Context(), []string{"openai", "--format", "jsonl", "batches", "list", "--watch", "--watch-count", "2"}))
	value := gjson.ParseBytes(out.Bytes())
	require.False(t, value.Get("complete").Bool())
	require.Equal(t, "batch_a", value.Get("data.0.id").Str)
	require.Equal(t, 1, strings.Count(out.String(), "\n"))
}
