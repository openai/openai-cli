package transformers

import (
	"context"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// ProjectRateLimit gives known rate fields explicit units in readable output.
// It preserves every value and leaves ambiguous or malformed records unchanged.
func ProjectRateLimit(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	valid := value.IsObject() && gjson.Valid(value.Raw)
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !valid {
		return value, nil
	}
	type replacement struct {
		start, end int
		label      string
	}
	var replacements []replacement
	seen := make(map[string]bool)
	object := false
	value.ForEach(func(key, field gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		label := projectRateLimitLabel(key.Str)
		if seen[label] {
			valid = false
			return false
		}
		seen[label] = true
		switch key.Str {
		case "object":
			object = field.Type == gjson.String && field.Str == "project.rate_limit"
		case "id", "model":
			valid = field.Type == gjson.String
		}
		if label != key.Str {
			// Keep null distinct from zero. Do not narrow large integers to int64 or float64.
			valid = field.Type == gjson.Null || field.Type == gjson.Number && !strings.ContainsAny(field.Raw, ".eE")
			start := key.Index - value.Index
			replacements = append(replacements, replacement{start, start + len(key.Raw), label})
		}
		return valid
	})
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !valid || !object || len(replacements) == 0 {
		return value, nil
	}
	var out strings.Builder
	offset := 0
	for _, change := range replacements {
		if err := writeProjectRateLimitJSON(ctx, &out, value.Raw[offset:change.start]); err != nil {
			return gjson.Result{}, err
		}
		out.WriteString(strconv.Quote(change.label))
		offset = change.end
	}
	if err := writeProjectRateLimitJSON(ctx, &out, value.Raw[offset:]); err != nil {
		return gjson.Result{}, err
	}
	result := gjson.Parse(out.String())
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	return result, nil
}

func projectRateLimitLabel(key string) string {
	switch key {
	case "max_requests_per_1_minute":
		return "max_requests_per_minute"
	case "max_tokens_per_1_minute":
		return "max_tokens_per_minute"
	case "max_images_per_1_minute":
		return "max_images_per_minute"
	case "max_audio_megabytes_per_1_minute":
		return "max_audio_megabytes_per_minute"
	case "max_requests_per_1_day":
		return "max_requests_per_day"
	case "batch_1_day_max_input_tokens":
		return "max_batch_input_tokens_per_day"
	default:
		return key
	}
}

// Copy large unfamiliar fields in chunks so cancellation can stop output assembly.
func writeProjectRateLimitJSON(ctx context.Context, out *strings.Builder, raw string) error {
	for len(raw) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(raw), 32*1024)
		out.WriteString(raw[:n])
		raw = raw[n:]
	}
	return ctx.Err()
}
