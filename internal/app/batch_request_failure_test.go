package app

import (
	"bytes"
	"io"
	"strings"
	"testing"

	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	"github.com/stretchr/testify/require"
)

func TestBatchBrowserFailureShowsMessageRecoveryAndDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, raw, expected, absent string }{
		{"server failure", `{"custom_id":"one","response":{"status_code":500,"request_id":"req_server","body":{"error":{"message":"Service unavailable.","code":"server_error","type":"server_error","param":null}}},"error":null}`,
			"one  Request failed\n\nService unavailable.\n\nNext  Retry only this request in a new batch.\n\nHTTP        500  Internal Server Error\nCode        server_error\nRequest ID  req_server", "Error details"},
		{"record failure", `{"custom_id":"two","error":{"message":"This batch expired.","code":"batch_expired"},"response":null}`,
			"Next  If you still need this result, submit this request in a new batch.", "HTTP"},
		{"invalid field", `{"custom_id":"three","response":{"status_code":400,"body":{"message":"Missing input.","type":"invalid_request_error","param":"input"}}}`,
			"Parameter   input", "Retry only"},
		{"quota", `{"custom_id":"four","response":{"status_code":429,"body":{"error":{"message":"Limit reached.","type":"insufficient_quota"}}}}`,
			"billing and usage limits", "rate and usage limits"},
		{"status takes precedence", `{"custom_id":"five","response":{"status_code":403,"body":{"error":{"message":"Access denied.","code":"insufficient_quota"}}}}`,
			"credentials and access", "billing"},
		{"unrecognized nested data", `{"custom_id":"six","response":{"status_code":500,"body":{"error":{"message":"Service unavailable.","future":{"nested":5}}}}}`,
			"Nested: 5", "Error details"},
		{"empty server body", `{"custom_id":"six-null","response":{"status_code":503,"body":null}}`,
			"The API returned no error message.", "Error details"},
		{"duplicate message", `{"custom_id":"seven","response":{"status_code":500,"body":{"error":{"message":"first","message":"second"}}}}`,
			"Message: first", "Error details"},
		{"response preserved", `{"custom_id":"eight","response":{"extra":"Keep this response"},"error":{"message":"Batch cancelled","code":"cancelled"}}`,
			"Keep this response", "HTTP"},
		{"terminal controls", `{"custom_id":"nine","response":{"status_code":422,"body":{"error":{"message":"Bad\u001b[31m field","param":"a\u001b[H"}}}}`,
			`Bad\u001b[31m field`, "\x1b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records := batchReadableOutput(batchcommands.RecordItem{Kind: "result", WriteJSON: func(w io.Writer) error {
				_, err := io.WriteString(w, tc.raw)
				return err
			}})
			var styles []batchcommands.RecordTextStyle
			records.Detail, records.Decorate = true, func(style batchcommands.RecordTextStyle) { styles = append(styles, style) }
			var out bytes.Buffer
			require.NoError(t, showReadableBatchRecords(t.Context(), records, &out, io.Discard))
			text := out.String()
			require.Contains(t, text, tc.expected)
			require.NotContains(t, text, tc.absent)
			for _, style := range styles {
				if style.Role == "negative" {
					require.Contains(t, []string{"Request failed", "Failed"}, text[style.Start:style.End])
				}
			}
			if tc.name == "server failure" {
				require.Equal(t, 1, strings.Count(text, "Service unavailable."))
				require.Equal(t, 1, strings.Count(text, "server_error"))
			}
		})
	}
}
