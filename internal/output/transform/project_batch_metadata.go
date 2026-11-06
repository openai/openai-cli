package transform

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// projectBatchMetadata retains Batch details and formats dates only for readable output.
func projectBatchMetadata(ctx context.Context, value gjson.Result) (gjson.Result, bool, error) {
	status := value.Get("status")
	if status.Exists() && status.Type != gjson.Null {
		switch status.Str {
		case "validating", "in_progress", "finalizing", "completed", "failed", "expired", "cancelling", "cancelled":
		default:
			return gjson.Result{}, false, ctx.Err()
		}
	}
	lifecycle := strings.Fields("created_at in_progress_at finalizing_at completed_at failed_at cancelling_at cancelled_at expired_at")
	timestamps := make(map[string]int64, len(lifecycle)+1)
	dates := make(map[string]string, len(lifecycle)+1)
	for _, key := range append(append([]string(nil), lifecycle...), "expires_at") {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, false, err
		}
		field := value.Get(key)
		if !field.Exists() || field.Type == gjson.Null {
			continue
		}
		// Accept integer Unix seconds within RFC3339's year range.
		seconds, err := strconv.ParseInt(field.Raw, 10, 64)
		if field.Type != gjson.Number || err != nil {
			return gjson.Result{}, false, nil
		}
		date := time.Unix(seconds, 0).UTC()
		if date.Year() < 0 || date.Year() > 9999 {
			return gjson.Result{}, false, nil
		}
		timestamps[key] = seconds
		dates[key] = strconv.Quote(date.Format(time.RFC3339))
	}
	// Stable ties retain the declared lifecycle order, regardless of API key order.
	sort.SliceStable(lifecycle, func(i, j int) bool {
		return timestamps[lifecycle[i]] < timestamps[lifecycle[j]]
	})
	fields := append([]string{"id", "status"}, lifecycle...)
	fields = append(fields, strings.Fields("completion_window request_counts input_file_id output_file_id error_file_id errors endpoint model expires_at usage metadata")...)
	summary, omitted, err := summarizeResourceFields(ctx, value, fields, []string{"object"})
	if err != nil || !summary.Exists() {
		return summary, omitted, err
	}
	created, hasCreated := timestamps["created_at"]
	completed, hasCompleted := timestamps["completed_at"]
	var out strings.Builder
	out.WriteByte('{')
	first := true
	summary.ForEach(func(key, field gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.WriteString(key.Raw)
		out.WriteByte(':')
		if date, ok := dates[key.Str]; ok {
			out.WriteString(date)
		} else {
			out.WriteString(field.Raw)
		}
		if key.Str == "completed_at" && status.Str == "completed" && hasCreated && hasCompleted && completed >= created {
			out.WriteString(`,"completion_duration":`)
			// The accepted year range keeps this subtraction within int64.
			out.WriteString(strconv.Quote(formatBatchDuration(completed - created)))
		}
		return true
	})
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	out.WriteByte('}')
	return gjson.Parse(out.String()), omitted, nil
}

func formatBatchDuration(seconds int64) string {
	var parts []string
	for _, unit := range []struct {
		seconds int64
		suffix  string
	}{{86400, "d"}, {3600, "h"}, {60, "m"}, {1, "s"}} {
		if count := seconds / unit.seconds; count > 0 {
			parts = append(parts, strconv.FormatInt(count, 10)+unit.suffix)
		}
		seconds %= unit.seconds
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}
