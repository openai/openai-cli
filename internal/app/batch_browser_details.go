package app

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
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
		if consistent {
			w.text("", fmt.Sprintf(" of %d succeeded", counts[0]))
		} else {
			w.text("", " succeeded")
		}
		if !consistent || counts[2] > 0 {
			w.text("", "  ")
		}
		if counts[2] > 0 {
			w.text("negative", strconv.FormatUint(counts[2], 10))
			w.text("", " failed")
		} else if !consistent {
			w.text("", "0 failed")
		}
		if !consistent {
			w.text("", fmt.Sprintf("  %d total", counts[0]))
		}
	}
	duration := value.Get("completion_duration")
	if duration.Exists() {
		if value.Get("status").Exists() || compact {
			w.text("", "  ")
		}
		w.text("secondary", readable.Text(duration.Str)+" elapsed")
	}
	if value.Get("status").Exists() || compact || duration.Exists() {
		w.text("", "\n")
	}
	type field struct {
		key, day, clock string
		value           gjson.Result
	}
	groups := make(map[string][]field)
	value.ForEach(func(key, item gjson.Result) bool {
		group := ""
		f := field{key: key.Str, value: item}
		switch key.Str {
		case "status", "completion_duration":
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
		case "completion_window":
			group = "Window"
		default:
			if strings.HasSuffix(key.Str, "_at") {
				group = "Timeline"
				if stamp, err := time.Parse(time.RFC3339, item.Str); err == nil {
					f.day, f.clock = stamp.UTC().Format("Jan 2, 2006"), stamp.UTC().Format("15:04:05")
				}
			}
		}
		groups[group] = append(groups[group], f)
		return true
	})
	groups[""] = append(groups[""], groups["Window"]...)
	labels := map[string]string{
		"id": "Batch ID", "endpoint": "Endpoint", "model": "Model",
		"created_at": "Created", "in_progress_at": "In progress", "finalizing_at": "Finalizing",
		"completed_at": "Completed", "failed_at": "Failed", "cancelling_at": "Cancelling",
		"cancelled_at": "Cancelled", "expired_at": "Expired", "expires_at": "Expires",
		"completion_window": "Completion window",
		"input_file_id":     "Input", "output_file_id": "Output", "error_file_id": "Errors",
	}
	for _, group := range []string{"", "Requests", "Errors", "Timeline", "Files", "Usage", "Metadata"} {
		items := groups[group]
		if len(items) == 0 {
			continue
		}
		var day string
		if group != "" {
			w.text("", "\n")
			w.text("heading", group)
			if group == "Timeline" && items[0].day != "" {
				day = items[0].day
				w.text("secondary", "  "+day+" UTC")
			}
			w.text("", "\n")
		}
		width := 8
		for _, item := range items {
			width = max(width, len(labels[item.key]))
		}
		for _, item := range items {
			if item.day != "" && item.day != day {
				day = item.day
				w.text("secondary", day+" UTC\n")
			}
			if label := labels[item.key]; label != "" {
				w.text("secondary", label+strings.Repeat(" ", width-len(label)+2))
			}
			if item.clock != "" {
				w.text("", item.clock+"\n")
			} else if group == "Metadata" && writeBrowserMetadataRows(w, item.value) {
				continue
			} else if w.err == nil {
				w.err = readable.Write(w, item.value)
			}
		}
	}
	return w.err
}

// Align ordinary metadata string pairs. The existing renderer preserves
// unfamiliar keys, multiline values, nested data, and long field names.
func writeBrowserMetadataRows(w *batchDetailWriter, metadata gjson.Result) bool {
	if !metadata.IsObject() || w.err != nil {
		return false
	}
	width := 0
	simple := true
	metadata.ForEach(func(key, value gjson.Result) bool {
		labelWidth := ansi.StringWidth(key.Str)
		simple = value.Type == gjson.String && !strings.Contains(value.Str, "\n") &&
			labelWidth > 0 && labelWidth <= 18 && strings.TrimSpace(key.Str) == key.Str &&
			readable.Text(key.Str) == key.Str && !strings.ContainsAny(key.Str, ":\t\n")
		width = max(width, labelWidth)
		return simple
	})
	if !simple || width == 0 {
		return false
	}
	metadata.ForEach(func(key, value gjson.Result) bool {
		w.text("secondary", key.Str+strings.Repeat(" ", max(8, width)-ansi.StringWidth(key.Str)+2))
		if w.err == nil {
			w.err = readable.Write(w, value)
		}
		return w.err == nil
	})
	return true
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
