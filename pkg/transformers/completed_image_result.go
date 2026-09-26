package transformers

import (
	"context"
	"errors"
	"strings"

	"github.com/tidwall/gjson"
)

// CompletedImageResult selects the final image bytes into the same data array
// used by nonstreaming generation and editing. It does not write files or handle previews.
func CompletedImageResult(ctx context.Context, event gjson.Result) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !gjson.Valid(event.Raw) || (event.Get("type").String() != "image_generation.completed" && event.Get("type").String() != "image_edit.completed") {
		return gjson.Result{}, errors.New("expected a completed image event")
	}
	data := event.Get("b64_json")
	if data.Type != gjson.String || data.String() == "" {
		return gjson.Result{}, errors.New("completed image event has no base64 image data")
	}
	var response strings.Builder
	response.Grow(len(data.Raw) + 32)
	response.WriteString(`{"data":[{"b64_json":`)
	response.WriteString(data.Raw)
	response.WriteString(`}]}`)
	return gjson.Parse(response.String()), ctx.Err()
}

// ImageGenerationResult retains the generation-only contract for existing callers.
func ImageGenerationResult(ctx context.Context, event gjson.Result) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if event.Get("type").String() != "image_generation.completed" {
		return gjson.Result{}, errors.New("expected a completed image generation event")
	}
	return CompletedImageResult(ctx, event)
}
