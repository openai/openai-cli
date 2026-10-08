package transformers

import (
	"context"

	"github.com/tidwall/gjson"
)

// Persisted items have no event envelope or object discriminator. Project only
// the same image slots used by stream presentation, retaining the complete item.
func summarizeAgentsItemImages(ctx context.Context, value gjson.Result) (gjson.Result, bool, error) {
	valid := value.IsObject() && gjson.Valid(value.Raw)
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	if !valid {
		return gjson.Result{}, false, nil
	}
	var content gjson.Result
	switch value.Get("type").Str {
	case "message":
		if value.Get("role").Str != "user" {
			return gjson.Result{}, false, nil
		}
		content = value.Get("content")
	case "function_call_output":
		content = value.Get("output")
	case "computer_use_call":
		output := value.Get("output")
		if output.Get("type").Str != "computer_screenshot" {
			return gjson.Result{}, false, nil
		}
		return summarizeAgentsEncodedImages(ctx, value, []gjson.Result{output.Get("image_url")}, "image/jpeg", "JPEG screenshot")
	default:
		return gjson.Result{}, false, nil
	}
	event, _, hidden, err := summarizeAgentsInputImages(ctx, value, content)
	return event.Details, hidden, err
}
