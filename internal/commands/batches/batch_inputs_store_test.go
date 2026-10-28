package batches

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func batchInputCollectionFixture(t *testing.T, records ...string) *batchInputCollection {
	t.Helper()
	store, err := newBatchRecordStore(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.close()) })
	for _, record := range records {
		require.NoError(t, store.appendListRecord(record, false))
	}
	return &batchInputCollection{store: store}
}

func TestBatchInputsStoredSelectionPreservesCompleteRecords(t *testing.T) {
	longName := strings.Repeat("x", 128*1024) + "資料-Requests[1].JSONL"
	first := fmt.Sprintf(`{"id":"file_first","filename":%q,"bytes":9007199254740993,"future":{"needle":"future-only"}}`, longName)
	second := `{"id":"file_SECOND","filename":"second.jsonl","unknown":[true,null]}`
	files := batchInputCollectionFixture(t, first, second, second)
	for _, tc := range []struct {
		query string
		count int64
		id    string
	}{
		{"requests[1]", 1, "file_first"}, {"FILE_SECOND", 2, "file_SECOND"}, {"future-only", 0, ""}, {"", 3, "file_first"},
	} {
		require.NoError(t, files.filter(t.Context(), tc.query))
		require.Equal(t, tc.count, files.matchCount)
		if tc.count > 0 {
			value, err := files.selected(t.Context(), 0)
			require.NoError(t, err)
			require.Equal(t, tc.id, value.Get("id").Str)
			if tc.id == "file_first" {
				require.Equal(t, first, value.Raw)
			}
		}
	}
	model := newBatchInputPicker(t.Context(), nil, 40, 8)
	model.files = files
	model.filter()
	require.NoError(t, model.err)
	require.Empty(t, model.items)
	require.Empty(t, model.visible)
	require.LessOrEqual(t, len(model.storedRows), 2)
	for _, row := range model.storedRows {
		require.Less(t, len(row.title), 200)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, quit := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, quit)
	require.Equal(t, second, model.selected.Raw)
	model.input.SetValue("requests[1]")
	model.filter()
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, first, model.selected.Raw)
}

func TestBatchInputsStoredFailuresPreserveCauses(t *testing.T) {
	t.Run("truncated-record", func(t *testing.T) {
		files := batchInputCollectionFixture(t, `{"id":"file_one","filename":"one.jsonl"}`)
		require.NoError(t, files.store.data.Truncate(1))
		var output bytes.Buffer
		err := files.writeJSON(&output)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.Contains(t, WorkflowErrorMessage(err), "Temporary batch data is incomplete")
		_, err = files.item(t.Context(), 1)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})
	t.Run("output", func(t *testing.T) {
		files := batchInputCollectionFixture(t, `{"id":"file_one"}`)
		err := files.writeJSON(batchPickerFailWriter{})
		require.ErrorIs(t, err, io.ErrClosedPipe)
		require.NotContains(t, err.Error(), "temporary storage")
	})
	t.Run("filter-cancellation", func(t *testing.T) {
		files := batchInputCollectionFixture(t, `{"id":"file_one"}`, `{"id":"file_two"}`)
		require.NoError(t, files.filter(t.Context(), ""))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.ErrorIs(t, files.filter(ctx, "two"), context.Canceled)
		require.Zero(t, files.matchCount)
		require.NoError(t, files.filter(t.Context(), "two"))
		require.Equal(t, int64(1), files.matchCount)
	})
	t.Run("picker-storage", func(t *testing.T) {
		files := batchInputCollectionFixture(t, `{"id":"file_one"}`)
		model := newBatchInputPicker(t.Context(), nil, 80, 24)
		model.files = files
		model.filter()
		require.NoError(t, model.err)
		require.NoError(t, files.store.data.Truncate(1))
		_, quit := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		require.NotNil(t, quit)
		require.ErrorIs(t, model.err, io.ErrUnexpectedEOF)
		require.NotPanics(t, func() { model.View() })
	})
}

func TestBatchInputsSpoolReleasesPriorPages(t *testing.T) {
	files := batchInputCollectionFixture(t)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	pages := 0
	err := files.fetch(t.Context(), -1, func(cursor string) ([]byte, error) {
		pages++
		if pages > 1 {
			require.Equal(t, fmt.Sprintf("file_other_%d", pages-1), cursor)
		}
		return []byte(fmt.Sprintf(`{"data":[{"id":"file_match_%d","filename":"requests.jsonl","purpose":"batch"},{"id":"file_other_%d","padding":"%s"}],"has_more":%t}`, pages, pages, strings.Repeat("x", 512*1024), pages < 48)), nil
	}, func(item gjson.Result) bool { return item.Get("purpose").Str == "batch" })
	require.NoError(t, err)
	require.Equal(t, int64(48), files.store.recordCount)
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(files)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("retained heap delta after 48 synthetic pages: %d bytes", retained)
	require.Less(t, retained, int64(8*1024*1024), "small matching records retained prior 512 KiB pages")
}

func TestBatchInputsPartialFailureKeepsRecordsAndCleansStorage(t *testing.T) {
	directory := batchRecordsPrivateTemp(t)
	store, err := newBatchRecordStore(t.Context())
	require.NoError(t, err)
	files := &batchInputCollection{store: store}
	want := errors.New("synthetic second-page failure")
	calls := 0
	err = files.fetch(t.Context(), -1, func(string) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, want
		}
		return []byte(`{"data":[{"id":"file_one","future":null}],"has_more":true}`), nil
	}, nil)
	require.ErrorIs(t, err, want)
	var output bytes.Buffer
	require.NoError(t, files.writeJSON(&output))
	require.JSONEq(t, `[{"id":"file_one","future":null}]`, output.String())
	require.NoError(t, store.close())
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}
