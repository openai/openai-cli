package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
)

func batchReadableItem(value string) batchcommands.RecordItem {
	return batchcommands.RecordItem{Kind: "result", WriteJSON: func(out io.Writer) error { _, err := io.WriteString(out, value); return err }}
}

func batchReadableRecord(text string) string {
	return `{"custom_id":"request-demo","response":{"status_code":200,"body":{"object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}]}}}`
}

func batchReadableOutput(items ...batchcommands.RecordItem) batchcommands.RecordOutput {
	return batchcommands.RecordOutput{BatchID: "batch-demo", File: "output", FileID: "file-demo", Visit: func(visit func(batchcommands.RecordItem) error) error {
		for _, item := range items {
			if err := visit(item); err != nil {
				return err
			}
		}
		return nil
	}}
}

func TestReadableBatchRecordProjectionBoundaryPreservesCompleteContent(t *testing.T) {
	for _, length := range []int{batchRecordProjectionBuffer - 1, batchRecordProjectionBuffer, batchRecordProjectionBuffer + 1, 2 * batchRecordProjectionBuffer} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			text := strings.Repeat("x", length-len(batchReadableRecord("")))
			record := batchReadableRecord(text)
			require.Len(t, record, length)
			var out, diagnostics bytes.Buffer
			err := showReadableBatchRecords(context.Background(), batchReadableOutput(batchReadableItem(record)), &out, &diagnostics)
			require.NoError(t, err)
			require.Contains(t, out.String(), text)
			require.Equal(t, length > batchRecordProjectionBuffer, strings.Contains(out.String(), "large record"))
			require.Equal(t, 1, strings.Count(out.String(), "batch-demo"))
		})
	}
}

func TestReadableBatchRecordsSharedHintAndQuiet(t *testing.T) {
	items := []batchcommands.RecordItem{batchReadableItem(batchReadableRecord("first")), batchReadableItem(batchReadableRecord("second"))}
	for _, quiet := range []bool{false, true} {
		ctx := context.WithValue(context.Background(), outputPolicyKey{}, outputPolicy{quiet: quiet, diagnostics: !quiet})
		var out, diagnostics bytes.Buffer
		require.NoError(t, showReadableBatchRecords(ctx, batchReadableOutput(items...), &out, &diagnostics))
		require.Contains(t, out.String(), "first")
		require.Contains(t, out.String(), "second")
		want := 1
		if quiet {
			want = 0
		}
		require.Equal(t, want, strings.Count(diagnostics.String(), "Full data:"))
	}
}

func TestReadableBatchRecordsFallbackGroupsDoNotAddAHint(t *testing.T) {
	group := batchcommands.RecordItem{Kind: "group", WriteJSON: func(out io.Writer) error {
		_, err := io.WriteString(out, `{"custom_id":"request-demo","match":"matched","input_count":1,"result_count":1}`)
		return err
	}}
	result := batchReadableItem(`{"custom_id":"request-demo","response":{"body":{"unknown":"complete value"}}}`)
	result.Kind = "matched_result"
	var out, diagnostics bytes.Buffer
	require.NoError(t, showReadableBatchRecords(context.Background(), batchReadableOutput(group, result), &out, &diagnostics))
	require.Contains(t, out.String(), "matched")
	require.Contains(t, out.String(), "complete value")
	require.Empty(t, diagnostics.String())
}

func TestReadableBatchRecordsPreservePartialOutputAndSourceFailure(t *testing.T) {
	upstream := errors.New("synthetic source failure")
	item := batchcommands.RecordItem{Kind: "result", WriteJSON: func(out io.Writer) error {
		_, _ = io.WriteString(out, batchReadableRecord("complete prefix"))
		return upstream
	}}
	var out, diagnostics bytes.Buffer
	err := showReadableBatchRecords(context.Background(), batchReadableOutput(batchReadableItem(batchReadableRecord("first record")), item), &out, &diagnostics)
	require.ErrorIs(t, err, upstream)
	require.Contains(t, out.String(), "first record")
	require.Contains(t, out.String(), "complete prefix")
	require.Empty(t, diagnostics.String())
}

func TestReadableBatchRecordsPreserveDiagnosticFailure(t *testing.T) {
	failure := errors.New("synthetic diagnostic failure")
	var out bytes.Buffer
	err := showReadableBatchRecords(context.Background(), batchReadableOutput(batchReadableItem(batchReadableRecord("answer"))), &out, resourceSummaryTestWriter(func([]byte) (int, error) { return 0, failure }))
	require.ErrorIs(t, err, failure)
	var diagnostic *clioutput.DiagnosticError
	require.ErrorAs(t, err, &diagnostic)
	require.Contains(t, out.String(), "answer")
}

func TestReadableBatchRecordsPreserveHugeIDsAndEscapeControls(t *testing.T) {
	id := strings.Repeat("id", batchRecordProjectionBuffer)
	record := strings.Replace(batchReadableRecord(`safe\u001b[31manswer`), "request-demo", id, 1)
	output := batchReadableOutput(batchReadableItem(record))
	output.TemporaryFile = func() (*os.File, func() error, error) {
		file, err := os.CreateTemp(t.TempDir(), "record-*")
		if err != nil {
			return nil, nil, err
		}
		return file, func() error { return errors.Join(file.Close(), os.Remove(file.Name())) }, nil
	}
	var out, diagnostics bytes.Buffer
	require.NoError(t, showReadableBatchRecords(context.Background(), output, &out, &diagnostics))
	require.Contains(t, out.String(), id)
	require.NotContains(t, out.String(), "\x1b")
	require.Contains(t, out.String(), "answer")
}

func TestReadableBatchRecordsCancellationStopsRecordProducer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	sink := resourceSummaryTestWriter(func(data []byte) (int, error) {
		if bytes.Contains(data, []byte("Details (large record")) {
			cancel()
		}
		return out.Write(data)
	})
	err := showReadableBatchRecords(ctx, batchReadableOutput(batchReadableItem(batchReadableRecord(strings.Repeat("x", 2*batchRecordProjectionBuffer)))), sink, io.Discard)
	require.ErrorIs(t, err, context.Canceled)
}
