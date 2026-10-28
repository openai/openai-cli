package app

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// Group the existing readable projection only inside the Batch browser.
func showBatchBrowserDetails(ctx context.Context, batch gjson.Result, out io.Writer, decorate func(batchcommands.RecordTextStyle)) error {
	out = clioutput.Writer{Context: ctx, Sink: out}
	value, _, err := transform.SummarizeResource(ctx, batch, transform.Route{
		Operation: "(resource) batches > (method) retrieve", OutputKind: transform.OutputResponse,
	})
	if err != nil {
		return err
	}
	if !value.Exists() {
		return readable.Write(out, batch)
	}
	w := &batchDetailWriter{out: out, decorate: decorate}
	counts, compact := batchBrowserDetailCounts(value.Get("request_counts"))
	status, tone := batchcommands.BrowserStatus(batch)
	consistent := compact && counts[1] <= counts[0] && counts[2] <= counts[0]-counts[1]
	if batch.Get("status").Str == "completed" {
		consistent = consistent && counts[2] == counts[0]-counts[1]
	}
	if batch.Get("status").Str == "completed" && (!consistent || counts[0] == 0) {
		tone = ""
	}
	if value.Get("errors").Exists() && tone == "positive" {
		tone = "warning"
	}
	if value.Get("status").Exists() {
		w.text(tone, readable.Text(status))
	}
	if compact {
		if value.Get("status").Exists() {
			w.text("", "  ")
		}
		successTone := ""
		if consistent && counts[1] > 0 {
			successTone = "positive"
		}
		w.text(successTone, strconv.FormatUint(counts[1], 10))
		w.text("", " succeeded  ")
		failureTone := ""
		if counts[2] > 0 {
			failureTone = "negative"
		}
		w.text(failureTone, strconv.FormatUint(counts[2], 10))
		w.text("", fmt.Sprintf(" failed  %d total", counts[0]))
	}
	if value.Get("status").Exists() || compact {
		w.text("", "\n")
	}
	type field struct {
		key   string
		value gjson.Result
	}
	groups := make(map[string][]field)
	value.ForEach(func(key, item gjson.Result) bool {
		group := ""
		switch key.Str {
		case "status":
			return true
		case "request_counts":
			if compact {
				return true
			}
			group = "Requests"
		case "errors":
			group = "Errors"
		case "input_file_id", "output_file_id", "error_file_id":
			group = "Files"
		case "usage":
			group = "Usage"
		case "metadata":
			group = "Metadata"
		default:
			if strings.HasSuffix(key.Str, "_at") || key.Str == "completion_duration" || key.Str == "completion_window" {
				group = "Timing"
			}
		}
		groups[group] = append(groups[group], field{key.Str, item})
		return true
	})
	labels := map[string]string{
		"id": "Batch ID", "endpoint": "Endpoint", "model": "Model",
		"created_at": "Created", "in_progress_at": "In progress", "finalizing_at": "Finalizing",
		"completed_at": "Completed", "failed_at": "Failed", "cancelling_at": "Cancelling",
		"cancelled_at": "Cancelled", "expired_at": "Expired", "expires_at": "Expires",
		"completion_duration": "Duration", "completion_window": "Completion window",
		"input_file_id": "Input", "output_file_id": "Output", "error_file_id": "Errors",
	}
	for _, group := range []string{"", "Requests", "Errors", "Timing", "Files", "Usage", "Metadata"} {
		items := groups[group]
		if len(items) == 0 {
			continue
		}
		if group != "" {
			w.text("", "\n")
			w.text("heading", group+"\n")
		}
		width := 0
		for _, item := range items {
			width = max(width, len(labels[item.key]))
		}
		for _, item := range items {
			if label := labels[item.key]; label != "" {
				w.text("secondary", label+strings.Repeat(" ", width-len(label)+2))
			}
			if w.err == nil {
				w.err = readable.Write(w, item.value)
			}
		}
	}
	return w.err
}

// Compact only complete, familiar counters. Preserve other shapes verbatim.
func batchBrowserDetailCounts(value gjson.Result) ([3]uint64, bool) {
	var result [3]uint64
	if !value.IsObject() {
		return result, false
	}
	seen := make(map[string]bool)
	valid := true
	value.ForEach(func(key, item gjson.Result) bool {
		index := map[string]int{"total": 0, "completed": 1, "failed": 2}
		i, known := index[key.Str]
		count, err := strconv.ParseUint(item.Raw, 10, 64)
		if !known || seen[key.Str] || item.Type != gjson.Number || err != nil {
			valid = false
			return false
		}
		seen[key.Str] = true
		result[i] = count
		return true
	})
	return result, valid && len(seen) == 3
}
