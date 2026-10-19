package transform

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"
)

// ProjectLimitsOverview adds units to recognized records in a limits snapshot.
// The caller selects readable output; machine formats keep the original snapshot.
func ProjectLimitsOverview(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	unique, err := limitsOverviewUniqueObject(ctx, value)
	if err != nil || !unique {
		return value, err
	}
	scope := value.Get("scope")
	if scope.Type != gjson.String || scope.Str != "organization" && scope.Str != "project" {
		return value, nil
	}
	var out strings.Builder
	out.WriteByte('{')
	first := true
	value.ForEach(func(key, field gjson.Result) bool {
		if err = ctx.Err(); err != nil {
			return false
		}
		projected := field
		switch key.Str {
		case "spend_limit":
			projected, err = projectSpendThreshold(ctx, field, scope.Str+".spend_limit", false)
		case "spend_alerts":
			projected, err = projectLimitsOverviewArray(ctx, field, func(item gjson.Result) (gjson.Result, error) {
				return projectSpendThreshold(ctx, item, scope.Str+".spend_alert", true)
			})
		case "project_rate_limits":
			if scope.Str == "project" {
				projected, err = projectLimitsOverviewArray(ctx, field, func(item gjson.Result) (gjson.Result, error) {
					return projectLimitsOverviewRate(ctx, item)
				})
			}
		}
		if err != nil {
			return false
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.WriteString(key.Raw)
		out.WriteByte(':')
		out.WriteString(projected.Raw)
		return true
	})
	if err != nil {
		return gjson.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	out.WriteByte('}')
	return gjson.Parse(out.String()), nil
}

func projectLimitsOverviewArray(ctx context.Context, value gjson.Result, project func(gjson.Result) (gjson.Result, error)) (gjson.Result, error) {
	if !value.IsArray() {
		return value, ctx.Err()
	}
	var out strings.Builder
	out.WriteByte('[')
	first := true
	var err error
	value.ForEach(func(_, item gjson.Result) bool {
		if err = ctx.Err(); err != nil {
			return false
		}
		var projected gjson.Result
		projected, err = project(item)
		if err != nil {
			return false
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.WriteString(projected.Raw)
		return true
	})
	if err != nil {
		return gjson.Result{}, err
	}
	out.WriteByte(']')
	return gjson.Parse(out.String()), ctx.Err()
}

var limitsOverviewRateLabels = map[string]string{
	"max_requests_per_1_minute":        "max_requests_per_minute",
	"max_tokens_per_1_minute":          "max_tokens_per_minute",
	"max_images_per_1_minute":          "max_images_per_minute",
	"max_audio_megabytes_per_1_minute": "max_audio_megabytes_per_minute",
	"max_requests_per_1_day":           "max_requests_per_day",
	"batch_1_day_max_input_tokens":     "max_batch_input_tokens_per_day",
}

func projectLimitsOverviewRate(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	unique, err := limitsOverviewUniqueObject(ctx, value)
	if err != nil || !unique || value.Get("object").Str != "project.rate_limit" {
		return value, err
	}
	// A future response field must never collide with a derived readable label.
	for source, label := range limitsOverviewRateLabels {
		if value.Get(source).Type == gjson.Number && value.Get(label).Exists() {
			return value, nil
		}
	}
	var out strings.Builder
	out.WriteByte('{')
	first := true
	value.ForEach(func(key, field gjson.Result) bool {
		if err = ctx.Err(); err != nil {
			return false
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		if label, ok := limitsOverviewRateLabels[key.Str]; ok && field.Type == gjson.Number {
			out.WriteByte('"')
			out.WriteString(label)
			out.WriteByte('"')
		} else {
			out.WriteString(key.Raw)
		}
		out.WriteByte(':')
		out.WriteString(field.Raw)
		return true
	})
	if err != nil {
		return gjson.Result{}, err
	}
	out.WriteByte('}')
	return gjson.Parse(out.String()), ctx.Err()
}

func limitsOverviewUniqueObject(ctx context.Context, value gjson.Result) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !value.IsObject() {
		return false, nil
	}
	seen := make(map[string]bool)
	unique := true
	value.ForEach(func(key, _ gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		if seen[key.Str] {
			unique = false
			return false
		}
		seen[key.Str] = true
		return true
	})
	return unique, ctx.Err()
}
