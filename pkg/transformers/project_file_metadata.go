package transformers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// ProjectFileMetadata preserves complete file records and formats known timestamps.
// Machine output never calls this presentation-only projection.
func ProjectFileMetadata(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !value.IsObject() || value.Get("object").Str != "file" || !gjson.Valid(value.Raw) {
		return value, ctx.Err()
	}
	var out strings.Builder
	out.WriteByte('{')
	first := true
	value.ForEach(func(key, field gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.WriteString(key.Raw)
		out.WriteByte(':')
		if key.Str == "created_at" || key.Str == "expires_at" {
			if seconds, err := strconv.ParseInt(field.Raw, 10, 64); field.Type == gjson.Number && err == nil {
				date := time.Unix(seconds, 0).UTC()
				if date.Year() >= 0 && date.Year() <= 9999 {
					out.WriteString(strconv.Quote(date.Format(time.RFC3339)))
					return true
				}
			}
		}
		out.WriteString(field.Raw)
		return true
	})
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	out.WriteByte('}')
	return gjson.Parse(out.String()), nil
}
