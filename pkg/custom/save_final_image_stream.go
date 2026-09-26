package custom

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// Close the SDK stream even when stopping at completion before EOF. Never save
// intermediate image bytes or wait for a redundant event after completion.
func saveFinalImageStream[T any](ctx context.Context, source jsonview.Iterator[T], plan *imageOutputPlan, out io.Writer) error {
	if closer, ok := any(source).(io.Closer); ok {
		defer closer.Close()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var seen [3]bool
	progressUnavailable := false
	for source.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := source.Current()
		var raw string
		if value, ok := any(item).(hasRawJSON); ok {
			raw = value.RawJSON()
		} else {
			encoded, err := json.Marshal(item)
			if err != nil {
				return imageSavingFailure("Could not read an image stream event. Check API usage before trying again.", err)
			}
			raw = string(encoded)
		}
		event := gjson.Parse(raw)
		switch event.Get("type").String() {
		case "image_generation.partial_image", "image_edit.partial_image":
			index := event.Get("partial_image_index")
			if progressUnavailable || plan.partialImages < 1 || plan.partialImages > 3 ||
				index.Type != gjson.Number || index.Float() != float64(index.Int()) ||
				index.Int() < 0 || index.Int() >= plan.partialImages || seen[index.Int()] {
				continue
			}
			protocol := imageProgressProtocol(plan.inline, isTerminal(out), os.Getenv)
			if protocol == "" {
				continue
			}
			// Count attempts too: repeated malformed events must not flood output.
			seen[index.Int()] = true
			var err error
			progressUnavailable, err = plan.displayImageProgress(ctx, event, out, protocol)
			if err != nil {
				return imageSavingFailure("The image preview could not finish before a final image was received. Check API usage before trying again.", err)
			}
		case "image_generation.completed", "image_edit.completed":
			response, err := transformers.CompletedImageResult(ctx, event)
			if err != nil {
				return imageSavingFailure("The completed image event could not be saved. Check API usage before trying again.", err)
			}
			return plan.save(ctx, []byte(response.Raw), out)
		case "error", "image_generation.failed", "image_edit.failed":
			return imageSavingFailure("Image processing failed before a final image was received. Check API usage before trying again.", nil)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := source.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return imageSavingFailure("The image stream stopped before a final image was received. Check API usage before trying again.", err)
	}
	return imageSavingFailure("The image stream ended without a final image. Check API usage before trying again.", nil)
}
