package batches

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchBrowserMissingFileGuidance(t *testing.T) {
	success := `,"request_counts":{"completed":3,"failed":0,"total":3},"errors":null,"error_file_id":null`
	for _, tc := range []struct {
		name, status, selection, extra, heading, next string
	}{
		{"processing results", "in_progress", "output", "", "Results unavailable", "Press Ctrl+R"},
		{"validating errors", "validating", "error", "", "Error file unavailable", "Press Ctrl+R"},
		{"finalizing results", "finalizing", "output", "", "Results unavailable", "Press Ctrl+R"},
		{"cancelling errors", "cancelling", "error", "", "Error file unavailable", "Press Ctrl+R"},
		{"completed results", "completed", "output", "", "Results unavailable", "Choose Batch details"},
		{"failed results", "failed", "output", "", "Results unavailable", "Choose View errors"},
		{"expired results", "expired", "output", "", "Results unavailable", "Choose Batch details"},
		{"cancelled errors", "cancelled", "error", "", "Error file unavailable", "Choose Batch details"},
		{"unknown state", "future_state", "output", "", "Results unavailable", "Choose Batch details"},
		{"missing input", "in_progress", "input", "", "Input file unavailable", "Choose Batch details"},
		{"explicit success", "completed", "error", success + `,"output_file_id":"file_results"`, "No request errors reported", "Choose View results"},
		{"success without results", "completed", "error", success, "No request errors reported", "Choose Batch details"},
		{"incomplete counts", "completed", "error", `,"request_counts":{"failed":0}`, "Error file unavailable", "Choose Batch details"},
		{"inconsistent counts", "completed", "error", `,"request_counts":{"completed":1,"failed":0,"total":3},"errors":null,"error_file_id":null`, "Error file unavailable", "Choose Batch details"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/batches" {
					_, _ = io.WriteString(w, `{"data":[{"id":"batch_one"}],"has_more":false}`)
					return
				}
				require.Equal(t, "/batches/batch_one", r.URL.Path)
				_, _ = fmt.Fprintf(w, `{"id":"batch_one","status":%q%s}`, tc.status, tc.extra)
			}))
			defer server.Close()
			var out bytes.Buffer
			steps := []browserTestStep{{"list", "batch:batch_one", ""}, {"actions", tc.selection, ""}, {"document", "back", ""}, {"actions", "back", ""}, {"list", "back", ""}}
			root := browserTestCommand(t, server.URL, &out, browserTestUI(t, steps))
			require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "browse"}), "an unavailable file is not an action failure")
			text := out.String()
			require.Contains(t, text, tc.heading+"\n\nBatch: batch_one\nStatus: ")
			require.Contains(t, text, "\n\nNext: ")
			require.Contains(t, text, tc.next)
			require.Equal(t, 1, strings.Count(text, "Next:"))
			if tc.next != "Press Ctrl+R" {
				require.NotContains(t, text, "refresh")
			}
			if tc.heading != "No request errors reported" {
				require.NotContains(t, text, "No request errors")
			}
			require.NotContains(t, text, "Output shown above")
		})
	}
}

func TestBatchBrowserMessageContextEscapesFields(t *testing.T) {
	batch := gjson.Parse(`{"id":"batch_\nNext: forged\u001b[2J","status":"future\nStatus: completed\u202e"}`)
	var out bytes.Buffer
	title, body := batchBrowserMissingFile(batch, "output")
	require.NoError(t, writeBatchBrowserSection(t.Context(), &out, title, body))
	text := out.String()
	require.NotContains(t, text, "\x1b")
	require.NotContains(t, text, "\u202e")
	require.Equal(t, 1, strings.Count(text, "\nNext:"))
	require.Equal(t, 1, strings.Count(text, "\nStatus:"))
	require.Contains(t, text, `batch_\nNext: forged`)
}

func TestBatchBrowserReturnOnlyShowsControls(t *testing.T) {
	view := newBatchBrowserReturn("").View()
	require.False(t, view.AltScreen)
	require.Equal(t, "Enter / Esc back", strings.TrimSpace(ansi.Strip(view.Content)))
}
