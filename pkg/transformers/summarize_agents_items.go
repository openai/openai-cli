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
	var images []agentsImageSlot
	collectAgentsItemImages(ctx, value, &images)
	return summarizeAgentsEncodedImages(ctx, value, images)
}
