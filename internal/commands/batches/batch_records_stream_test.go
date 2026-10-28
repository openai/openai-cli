package batches

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchRecordStreamReusesSpillFile(t *testing.T) {
	directory := batchRecordsPrivateTemp(t)
	records := []string{
		`{"custom_id":"one","body":"` + strings.Repeat("x", 2*batchRecordMemory) + `","n":1.2300e+20}`,
		`{"custom_id":"two","body":"` + strings.Repeat("y", 3*batchRecordMemory) + `"}`,
		`{"custom_id":"three"}`,
	}
	stream := newBatchRecordStream(t.Context(), strings.NewReader(strings.Join(records, "\n")), "output", "")
	require.NoError(t, stream.next())
	defer stream.close()
	var filename string
	count := 0
	require.NoError(t, stream.visit(func(item RecordItem) error {
		var output bytes.Buffer
		require.NoError(t, item.WriteJSON(&output))
		require.Equal(t, records[count], output.String())
		require.LessOrEqual(t, stream.buffer.Cap(), batchRecordMemory)
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		if filename == "" {
			filename = entries[0].Name()
		}
		require.Equal(t, filename, entries[0].Name())
		info, err := stream.file.Stat()
		require.NoError(t, err)
		if stream.spilled {
			require.Equal(t, int64(len(records[count])), info.Size())
		} else {
			require.Zero(t, info.Size())
		}
		count++
		return nil
	}))
	require.Equal(t, len(records), count)
	require.NoError(t, stream.close())
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestBatchRecordStreamPreservesIDSemantics(t *testing.T) {
	records := []string{
		`{"custom_id":"A","n":9007199254740993}`,
		`{"\u0063ustom_id":"a"}`,
		`{"custom_id":"\ud800\u0061"}`,
		`{"custom_id":"\ud834\udd1e"}`,
		"{\"custom_id\":\"\xff\"}",
		`{"custom_id":"�"}`,
		`{"custom_id":"�a"}`,
		`{"custom_id":"a","duplicate":true}`,
	}
	for _, filter := range []string{"", "a", "A", "\ufffd", "\ufffda", "𝄞", "\xff", "missing"} {
		t.Run(filter, func(t *testing.T) {
			stream := newBatchRecordStream(t.Context(), strings.NewReader("\n\u2003"+strings.Join(records, "\n")+"\n"), "output", filter)
			defer stream.close()
			require.NoError(t, stream.next())
			var got, want []string
			require.NoError(t, stream.visit(func(item RecordItem) error {
				var output bytes.Buffer
				require.NoError(t, item.WriteJSON(&output))
				got = append(got, output.String())
				return nil
			}))
			for _, record := range records {
				if filter == "" || gjson.Get(record, "custom_id").String() == filter {
					want = append(want, record)
				}
			}
			require.Equal(t, want, got)
		})
	}
}

func TestBatchRecordStreamValidatesRecordBoundary(t *testing.T) {
	for _, invalid := range []string{`{"private":true} garbage`, `{"custom_id":"a","\u0063ustom_id":"b"}`, `{"private":`} {
		stream := newBatchRecordStream(t.Context(), strings.NewReader("{\"custom_id\":\"first\"}\n"+invalid), "output", "")
		require.NoError(t, stream.next())
		var output bytes.Buffer
		err := stream.writeJSON(&output, "batch_example", "file_example")
		require.Error(t, err)
		require.Contains(t, output.String(), `"custom_id":"first"`)
		require.NotContains(t, output.String(), "private")
		require.NotContains(t, output.String(), `"custom_id":"a"`)
		require.Contains(t, WorkflowErrorMessage(err), "Output may be incomplete")
		require.NoError(t, stream.close())
	}
}

type batchStreamCancelReader struct {
	reader io.Reader
	cancel context.CancelFunc
	read   int
}

func (r *batchStreamCancelReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	r.read += n
	if r.read > 2*batchRecordMemory {
		r.cancel()
	}
	return n, err
}

func TestBatchRecordStreamCancellationRemovesSpill(t *testing.T) {
	directory := batchRecordsPrivateTemp(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader := &batchStreamCancelReader{reader: strings.NewReader(`{"custom_id":"` + strings.Repeat("x", 4*batchRecordMemory)), cancel: cancel}
	stream := newBatchRecordStream(ctx, reader, "output", "small-id")
	require.ErrorIs(t, stream.next(), context.Canceled)
	require.NotNil(t, stream.file)
	require.LessOrEqual(t, stream.buffer.Cap(), batchRecordMemory)
	require.NoError(t, stream.close())
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestBatchRecordStreamPreservesStorageAndWriterErrors(t *testing.T) {
	batchRecordsPrivateTemp(t)
	stream := newBatchRecordStream(t.Context(), strings.NewReader(`{"body":"`+strings.Repeat("x", 2*batchRecordMemory)+`"}`), "output", "")
	require.NoError(t, stream.next())
	defer stream.close()
	writeErr := errors.New("synthetic output failure")
	require.ErrorIs(t, stream.writeRecord(failedBatchRecordWriter{writeErr}), writeErr)
	require.NoError(t, stream.file.Truncate(0))
	err := stream.writeRecord(io.Discard)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Contains(t, WorkflowErrorMessage(err), "Temporary batch data is incomplete")
}
