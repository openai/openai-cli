package batches

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestPrepareBatchCSVConvertsOnlySelectedColumn(t *testing.T) {
	temp := batchRecordsPrivateTemp(t)
	path := filepath.Join(t.TempDir(), "support tickets.CSV")
	const source = "\uFEFF\"Ticket\",Message,Other\r\n001,\"Line 1, hello\r\nLine 2 ✨\",private-not-uploaded\r\n002,\"She said \"\"okay\"\"\",second\r\n"
	require.NoError(t, os.WriteFile(path, []byte(source), 0600))
	header, err := readBatchCSVHeader(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, []string{"Ticket", "Message", "Other"}, header)

	snapshot, preview, err := prepareBatchCSV(t.Context(), path, "Message", "demo-model", "Classify.")
	require.NoError(t, err)
	defer func() { require.NoError(t, snapshot.close()) }()
	require.Equal(t, 2, preview.Count)
	require.Equal(t, header, preview.Header)
	require.Contains(t, preview.FirstInput, "Line 1, hello")
	require.Equal(t, "support tickets.jsonl", snapshot.filename)
	require.Len(t, snapshot.store.files, 1)

	upload, err := openBatchSubmissionFile(t.Context(), path, snapshot)
	require.NoError(t, err)
	content, err := io.ReadAll(upload.Reader)
	require.NoError(t, err)
	require.NoError(t, upload.Close())
	require.Equal(t, int64(len(content)), snapshot.Size())
	require.NotContains(t, string(content), "private-not-uploaded")
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	require.Len(t, lines, 2)
	expectedRows := [][]string{
		{"001", "Line 1, hello\nLine 2 ✨", "private-not-uploaded"},
		{"002", "She said \"okay\"", "second"},
	}
	for i, line := range lines {
		var record struct {
			ID     string            `json:"custom_id"`
			Method string            `json:"method"`
			URL    string            `json:"url"`
			Body   map[string]string `json:"body"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		require.Equal(t, "POST", record.Method)
		require.Equal(t, "/v1/responses", record.URL)
		require.Equal(t, batchCSVRowID(header, expectedRows[i], i+1), record.ID)
		require.Equal(t, map[string]string{"input": expectedRows[i][1], "model": "demo-model", "instructions": "Classify."}, record.Body)
	}
	require.NoError(t, snapshot.close())
	entries, err := os.ReadDir(temp)
	require.NoError(t, err)
	require.Empty(t, entries)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, source, string(data))
}

func TestPrepareBatchCSVFailuresLeaveNoOutput(t *testing.T) {
	for _, tc := range []struct{ name, input, column, match string }{
		{"empty", "", "input", "CSV is empty"},
		{"no records", "id,input\n", "input", "no data"},
		{"blank heading", "id,  \n1,second\n", "id", "CSV column 2"},
		{"repeated heading", "input, input \n1,second\n", "input", "CSV column 2"},
		{"invalid header encoding", "\xff,text\n1,hello\n", "text", "CSV column 1"},
		{"changed schema", "id,renamed\n1,hello\n", "input", "no longer exists"},
		{"blank input", "id,input\n001,first\n002, \t\n", "input", "data row 2"},
		{"invalid encoding", "id,input\n001,first\n002,private\xff\n", "input", "data row 2 contains invalid UTF-8"},
		{"malformed last row", "id,input\n001,first\n002,\"private\n", "input", "row 2"},
		{"wrong width", "id,input\n001,first\n002,second,private\n", "input", "row 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := batchRecordsPrivateTemp(t)
			path := filepath.Join(t.TempDir(), "input.csv")
			require.NoError(t, os.WriteFile(path, []byte(tc.input), 0600))
			snapshot, _, err := prepareBatchCSV(t.Context(), path, tc.column, "model", "instruction")
			require.ErrorContains(t, err, tc.match)
			require.NotContains(t, err.Error(), "private")
			require.Nil(t, snapshot)
			entries, err := os.ReadDir(temp)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestPrepareBatchCSVPreviewBoundsAndSnapshotIdentity(t *testing.T) {
	input := strings.Repeat("🦊", 80_000)
	path := filepath.Join(t.TempDir(), "unicode.csv")
	source := "text\n" + input + "\n"
	require.NoError(t, os.WriteFile(path, []byte(source), 0600))
	snapshot, preview, err := prepareBatchCSV(t.Context(), path, "text", "model", "instruction")
	require.NoError(t, err)
	defer func() { require.NoError(t, snapshot.close()) }()
	require.True(t, utf8.ValidString(preview.FirstInput))
	require.Less(t, len(preview.FirstInput), 1024)
	require.True(t, strings.HasSuffix(preview.FirstInput, "…"))
	bytesRead, err := io.ReadAll(io.NewSectionReader(snapshot.file, 0, snapshot.Size()))
	require.NoError(t, err)
	require.Contains(t, string(bytesRead), input)
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), len(source)), 0600))
	require.NoError(t, os.Chtimes(path, snapshot.ModTime(), snapshot.ModTime()))
	upload, err := openBatchSubmissionFile(t.Context(), path, snapshot)
	require.NoError(t, err)
	read, err := io.ReadAll(upload.Reader)
	require.NoError(t, err)
	require.Equal(t, bytesRead, read)
	require.NoError(t, upload.Close())
}

func TestBatchCSVRowIDMatchesOnlyOriginalRecord(t *testing.T) {
	header := []string{"id", "input", "extra"}
	first := []string{"001", "hello", "x"}
	id := batchCSVRowID(header, first, 1)
	require.Equal(t, id, batchCSVRowID(header, first, 1))
	require.NotEqual(t, id, batchCSVRowID(header, first, 2))
	require.NotEqual(t, id, batchCSVRowID(header, []string{"1", "hello", "x"}, 1))
	require.NotEqual(t, id, batchCSVRowID(header, []string{"001", "hell", "ox"}, 1))
	require.NotEqual(t, id, batchCSVRowID([]string{"id", "text", "extra"}, first, 1))
}

func TestPrepareBatchCSVCanceled(t *testing.T) {
	temp := batchRecordsPrivateTemp(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := readBatchCSVHeader(ctx, "absent.csv")
	require.ErrorIs(t, err, context.Canceled)
	snapshot, _, err := prepareBatchCSV(ctx, "absent.csv", "text", "model", "instruction")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, snapshot)
	entries, err := os.ReadDir(temp)
	require.NoError(t, err)
	require.Empty(t, entries)
}
