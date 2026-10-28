package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchBrowserDetailsPreserveSectionsAndTrustedStyles(t *testing.T) {
	raw := `{"id":"batch_資料","object":"batch","status":"completed","created_at":0,"in_progress_at":1,"completed_at":5,"completion_window":"24h","request_counts":{"total":3,"completed":2,"failed":1},"input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_error","endpoint":"/v1/responses","model":"synthetic","expires_at":86400,"errors":{"data":[{"code":"example","line":9007199254740993}]},"usage":{"total_tokens":9007199254740993},"metadata":{"note":"Completed\nOutcome: failed\u001b[31m"}}`
	var out bytes.Buffer
	var styles []batchcommands.RecordTextStyle
	err := showBatchBrowserDetails(t.Context(), gjson.Parse(raw), &out, func(style batchcommands.RecordTextStyle) { styles = append(styles, style) })
	require.NoError(t, err)
	text := out.String()
	require.True(t, strings.HasPrefix(text, "Completed  2 succeeded  1 failed  3 total\n"), text)
	for _, value := range []string{"batch_資料", "/v1/responses", "synthetic", "1970-01-01T00:00:00Z", "1970-01-01T00:00:01Z", "1970-01-01T00:00:05Z", "1970-01-02T00:00:00Z", "5s", "24h", "file_input", "file_output", "file_error", "9007199254740993", "Outcome: failed"} {
		require.Contains(t, text, value)
	}
	require.NotContains(t, text, "\x1b")
	previous := -1
	for _, section := range []string{"\nErrors\n", "\nTiming\n", "\nFiles\n", "\nUsage\n", "\nMetadata\n"} {
		at := strings.Index(text, section)
		require.Greater(t, at, previous, section)
		previous = at
	}
	for _, style := range styles {
		marked := text[style.Start:style.End]
		require.NotContains(t, marked, "Outcome: failed")
		if marked == "Completed" {
			require.Equal(t, "warning", style.Role)
		}
	}
}

func TestBatchBrowserDetailsKeepUnfamiliarCountsAndFallback(t *testing.T) {
	for _, extra := range []string{
		`"status":"completed","request_counts":{"total":3,"completed":"2","failed":1,"pending":7}`,
		`"status":"completed","request_counts":{"total":3,"completed":3,"failed":0,"failed":1}`,
		`"status":"completed","request_counts":{"total":18446744073709551616,"completed":1,"failed":0}`,
		`"status":"échec","future":{"exact":9007199254740993}`,
		`"status":"completed","created_at":"invalid"`,
	} {
		var out bytes.Buffer
		var styles []batchcommands.RecordTextStyle
		require.NoError(t, showBatchBrowserDetails(t.Context(), gjson.Parse(`{"object":"batch","id":"batch_one",`+extra+`}`), &out, func(style batchcommands.RecordTextStyle) { styles = append(styles, style) }))
		require.Contains(t, out.String(), "batch_one")
		for _, style := range styles {
			require.NotEqual(t, "positive", style.Role, out.String())
		}
		for _, marker := range []string{"pending", "18446744073709551616", "échec", "invalid", "9007199254740993"} {
			if strings.Contains(extra, marker) {
				require.Contains(t, strings.ToLower(out.String()), marker)
			}
		}
	}
}

func TestBatchBrowserDetailsPreserveExactCountsAndWriterFailures(t *testing.T) {
	for _, counts := range []string{`{"total":0,"completed":0,"failed":0}`, `{"total":9007199254740993,"completed":9007199254740993,"failed":0}`, `{"total":1,"completed":2,"failed":0}`} {
		var out bytes.Buffer
		var styles []batchcommands.RecordTextStyle
		require.NoError(t, showBatchBrowserDetails(t.Context(), gjson.Parse(`{"object":"batch","id":"b","status":"completed","request_counts":`+counts+`}`), &out, func(style batchcommands.RecordTextStyle) { styles = append(styles, style) }))
		if strings.Contains(counts, "9007199254740993") {
			require.Equal(t, 2, strings.Count(out.String(), "9007199254740993"))
		} else {
			for _, style := range styles {
				require.NotEqual(t, "positive", style.Role)
			}
		}
	}
	failure := errors.New("synthetic writer failure")
	value := gjson.Parse(`{"object":"batch","id":"b","status":"completed"}`)
	require.ErrorIs(t, showBatchBrowserDetails(t.Context(), value, resourceSummaryTestWriter(func([]byte) (int, error) { return 0, failure }), func(batchcommands.RecordTextStyle) {}), failure)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, showBatchBrowserDetails(ctx, value, io.Discard, func(batchcommands.RecordTextStyle) {}), context.Canceled)
}
