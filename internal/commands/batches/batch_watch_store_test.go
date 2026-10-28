package batches

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchWatchSpoolPreservesRecordsAndCursorHistory(t *testing.T) {
	for _, tc := range []struct {
		name, after string
		maximum     int64
		pages       []string
		cursors     []string
		ids         []string
		failure     string
	}{
		{
			name: "duplicate-order", maximum: -1,
			pages: []string{
				`{"data":[{"id":"same","value":1},{"id":"same","value":2}],"has_more":true}`,
				`{"data":[{"id":"same","value":3},{"id":"next","value":4}],"has_more":false}`,
			},
			cursors: []string{"", "same"}, ids: []string{"same", "same", "same", "next"},
		},
		{
			name: "repeated-cursor", maximum: -1,
			pages: []string{
				`{"data":[{"id":"first"}],"has_more":true}`,
				`{"data":[{"id":"second"}],"has_more":true}`,
				`{"data":[{"id":"first"}],"has_more":true}`,
			},
			cursors: []string{"", "first", "second"}, ids: []string{"first", "second", "first"}, failure: "repeated cursor",
		},
		{
			name: "seed-cursor", after: "seed", maximum: -1,
			pages:   []string{`{"data":[{"id":"seed"}],"has_more":true}`},
			cursors: []string{"seed"}, ids: []string{"seed"}, failure: "repeated cursor",
		},
		{
			name: "zero-still-fetches", maximum: 0,
			pages:   []string{`{"data":[{"invalid":"ignored beyond max-items"}],"has_more":true}`},
			cursors: []string{""},
		},
		{
			name: "limit-before-invalid-item", maximum: 1,
			pages:   []string{`{"data":[{"id":"kept"},{"invalid":true}],"has_more":true}`},
			cursors: []string{""}, ids: []string{"kept"},
		},
		{
			name: "valid-prefix-before-invalid-item", maximum: -1,
			pages:   []string{`{"data":[{"id":"kept"},{"invalid":true}],"has_more":true}`},
			cursors: []string{""}, ids: []string{"kept"}, failure: "without a valid ID",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := newBatchRecordStore(t.Context())
			require.NoError(t, err)
			defer func() { require.NoError(t, store.close()) }()
			snapshot := batchWatchSnapshot{store: store}
			var cursors []string
			err = snapshot.fetch(tc.after, tc.maximum, func(after string) ([]byte, error) {
				require.Less(t, len(cursors), len(tc.pages), "unexpected extra page")
				page := tc.pages[len(cursors)]
				cursors = append(cursors, after)
				return []byte(page), nil
			})
			if tc.failure == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.failure)
			}
			require.Equal(t, tc.cursors, cursors)
			require.Equal(t, int64(len(tc.ids)), store.recordCount)
			for index, id := range tc.ids {
				item, err := snapshot.item(int64(index + 1))
				require.NoError(t, err)
				require.Equal(t, id, item.Get("id").Str)
				if tc.name == "duplicate-order" {
					require.Equal(t, int64(index+1), item.Get("value").Int())
				}
			}
		})
	}
	t.Run("cursor-index-growth", func(t *testing.T) {
		store, err := newBatchRecordStore(t.Context())
		require.NoError(t, err)
		defer func() { require.NoError(t, store.close()) }()
		snapshot := batchWatchSnapshot{store: store}
		for index := 0; index < 2113; index++ {
			seen, err := snapshot.rememberCursor(fmt.Sprintf("cursor-%d", index))
			require.NoError(t, err)
			require.False(t, seen)
		}
		require.Greater(t, store.bucketCount, uint64(1024))
		for _, index := range []int{0, 1023, 2048, 2112} {
			seen, err := snapshot.rememberCursor(fmt.Sprintf("cursor-%d", index))
			require.NoError(t, err)
			require.True(t, seen)
		}
		require.Zero(t, store.recordCount, "cursors are not snapshot records")
	})
}

func TestBatchWatchSpoolCompaction(t *testing.T) {
	for name, raw := range map[string]string{
		"pretty":         "{\n\t\"id\" : \"batch_a\",\n\"data\" : [ true, null, {\"x\": 2} ]\r\n}",
		"lexemes":        `{"id":"batch_a","n":9007199254740993,"n":-0,"e":1.2300e+99,"text":" a\\\"\n\t\u0062\ud800資料 "}`,
		"invalid-utf8":   "{\"id\":\"batch_a\",\"\xff\":\"\xff\"}",
		"chunk-boundary": `{"id":"batch_a","text":"` + strings.Repeat("x", 32742) + `\\\" a "}`,
	} {
		t.Run(name, func(t *testing.T) {
			var expected bytes.Buffer
			require.NoError(t, json.Compact(&expected, []byte(raw)))
			store, err := newBatchRecordStore(t.Context())
			require.NoError(t, err)
			defer func() { require.NoError(t, store.close()) }()
			snapshot := batchWatchSnapshot{store: store}
			require.NoError(t, snapshot.fetch("", -1, func(string) ([]byte, error) {
				return []byte(`{"data":[` + raw + `],"has_more":false}`), nil
			}))
			var output bytes.Buffer
			require.NoError(t, snapshot.writeRecord(&output, 1))
			require.Equal(t, expected.String(), output.String())
		})
	}
}

func TestBatchWatchSpoolFailuresAndCleanup(t *testing.T) {
	t.Run("creation", func(t *testing.T) {
		directory := batchRecordsPrivateTemp(t)
		for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
			t.Setenv(key, filepath.Join(directory, "missing"))
		}
		root := batchWatchTestCommand(t, "http://127.0.0.1:1", io.Discard, testOptions(), nil)
		err := root.Run(t.Context(), []string{"openai", "--format", "jsonl", "batches", "list", "--watch", "--watch-count", "1"})
		require.Error(t, err)
		message := WorkflowErrorMessage(err)
		require.Contains(t, message, "temporary storage space and permissions")
		require.Contains(t, message, "batches list --watch")
		require.NotContains(t, message, "batches results")
		require.NotContains(t, message, directory)
	})
	t.Run("record-write", func(t *testing.T) {
		store, err := newBatchRecordStore(t.Context())
		require.NoError(t, err)
		defer func() { require.NoError(t, store.close()) }()
		snapshot := batchWatchSnapshot{store: store}
		require.NoError(t, store.records.Close())
		err = snapshot.appendRecord(`{"id":"batch_a"}`)
		require.ErrorIs(t, err, os.ErrClosed)
		require.Zero(t, store.recordCount, "failed records must not appear in a partial snapshot")
		require.Contains(t, WorkflowErrorMessage(err), "batches list --watch")
	})
	t.Run("read-and-output", func(t *testing.T) {
		store, err := newBatchRecordStore(t.Context())
		require.NoError(t, err)
		defer func() { require.NoError(t, store.close()) }()
		snapshot := batchWatchSnapshot{store: store}
		require.NoError(t, snapshot.appendRecord(`{"id":"batch_a"}`))
		outputErr := errors.New("synthetic output failure")
		err = snapshot.writeRecord(failedBatchRecordWriter{outputErr}, 1)
		require.ErrorIs(t, err, outputErr)
		require.NotContains(t, err.Error(), "temporary storage")
		require.NoError(t, store.data.Close())
		err = snapshot.writeRecord(io.Discard, 1)
		require.ErrorIs(t, err, os.ErrClosed)
		require.Contains(t, WorkflowErrorMessage(err), "temporary storage space and permissions")
		require.Contains(t, WorkflowErrorMessage(err), "batches list --watch")
	})
	t.Run("snapshot-boundary", func(t *testing.T) {
		directory := batchRecordsPrivateTemp(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Len(t, entries, 4, "only the current snapshot may own scratch files")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"batch_a"}],"has_more":false}`)
		}))
		defer server.Close()
		root := batchWatchTestCommand(t, server.URL, io.Discard, testOptions(), nil)
		require.NoError(t, root.Run(t.Context(), []string{"openai", "--format", "jsonl", "batches", "list", "--watch", "--watch-count", "3", "--poll-interval", "1ns"}))
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Empty(t, entries)
	})
	t.Run("truncated-record", func(t *testing.T) {
		const record = `{"id":"batch_a","status":"completed"}`
		directory := batchRecordsPrivateTemp(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("after") == "" {
				_, _ = io.WriteString(w, `{"data":[`+record+`],"has_more":true}`)
				return
			}
			truncateBatchRecordPayload(t, directory, record, 0)
			_, _ = io.WriteString(w, `{"data":[],"has_more":false}`)
		}))
		defer server.Close()
		root := batchWatchTestCommand(t, server.URL, io.Discard, testOptions(), nil)
		err := root.Run(t.Context(), []string{"openai", "--format", "jsonl", "batches", "list", "--watch", "--watch-count", "1"})
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		message := WorkflowErrorMessage(err)
		require.Contains(t, message, "Temporary batch data is incomplete")
		require.Contains(t, message, "Output may be incomplete")
		require.Contains(t, message, "batches list --watch")
		require.NotContains(t, message, directory)
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Empty(t, entries)
	})
	t.Run("cancel-fetch", func(t *testing.T) {
		directory := batchRecordsPrivateTemp(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("after") == "" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"id":"batch_a"}],"has_more":true}`)
				return
			}
			cancel()
			<-r.Context().Done()
		}))
		defer server.Close()
		root := batchWatchTestCommand(t, server.URL, io.Discard, testOptions(), nil)
		err := root.Run(ctx, []string{"openai", "--format", "jsonl", "batches", "list", "--watch", "--watch-count", "1"})
		require.ErrorIs(t, err, context.Canceled)
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Empty(t, entries)
	})
}

func TestBatchWatchBoundedSnapshotMemory(t *testing.T) {
	directory := batchRecordsPrivateTemp(t)
	const pages = 48
	chunk := strings.Repeat("x", 32*1024)
	var baseline runtime.MemStats
	var retained atomic.Int64
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := requests.Add(1)
		if page == pages {
			runtime.GC()
			var current runtime.MemStats
			runtime.ReadMemStats(&current)
			retained.Store(int64(current.HeapAlloc) - int64(baseline.HeapAlloc))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"id":"batch_%d","metadata":{"payload":"`, page)
		for range 32 {
			_, _ = io.WriteString(w, chunk)
		}
		_, _ = fmt.Fprintf(w, `"}}],"has_more":%t}`, page < pages)
	}))
	defer server.Close()
	root := batchWatchTestCommand(t, server.URL, io.Discard, testOptions(), nil)
	runtime.GC()
	runtime.ReadMemStats(&baseline)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--format", "jsonl", "batches", "list", "--watch", "--watch-count", "1"}))
	require.Equal(t, int64(pages), requests.Load())
	t.Logf("retained heap before final page: %d bytes", retained.Load())
	require.Less(t, retained.Load(), int64(16*1024*1024), "previous API pages must not remain in memory")
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}
