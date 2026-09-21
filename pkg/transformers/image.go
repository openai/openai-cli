package transformers

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tidwall/gjson"
)

// ImageGenerateOperation matches the operation identifier emitted by the
// generated images generate handler.
const (
	ImageGenerateOperation  = "(resource) images > (method) generate"
	ImageEditOperation      = "(resource) images > (method) edit"
	ImageVariationOperation = "(resource) images > (method) create_variation"
)

// IsImageOperation identifies generated handlers whose responses contain images.
func IsImageOperation(operation string) bool {
	return operation == ImageGenerateOperation || operation == ImageEditOperation || operation == ImageVariationOperation
}

type imageOutputContextKey struct{}

// WithImageOutput opts this invocation into image normalization when selecting
// an output transformer. It has no effect on requests, files, or presentation.
func WithImageOutput(ctx context.Context) context.Context {
	return context.WithValue(ctx, imageOutputContextKey{}, true)
}

func imageOutputOnly(transform Transformer) Transformer {
	return func(ctx context.Context, value gjson.Result) (gjson.Result, error) {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, err
		}
		if enabled, _ := ctx.Value(imageOutputContextKey{}).(bool); !enabled {
			return Identity(ctx, value)
		}
		return transform(ctx, value)
	}
}

// ImageResponse preserves the complete image response, already shaped as
// {"data":[...]}. Image consumers validate individual items so one malformed
// item does not prevent them from handling the remaining images.
func ImageResponse(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	return value, nil
}

// ImageStreamEvent places a known image event's b64_json in a data array, making
// completed and partial images consumable through the same response interface.
// Other event fields, including type, partial_image_index and usage, survive
// unchanged. Unknown event types retain their original raw JSON bytes.
// This function transforms one value only; the caller owns stream completion.
func ImageStreamEvent(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	switch value.Get("type").String() {
	case "image_generation.completed", "image_generation.partial_image", "image_edit.completed", "image_edit.partial_image":
	default:
		return value, nil
	}

	var event map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value.Raw), &event); err != nil {
		// JSON decoding errors may include data from the event. Keep the error
		// independent of image bytes and other response contents.
		return gjson.Result{}, errors.New("could not read image stream event")
	}
	encoded := value.Get("b64_json")
	if encoded.Type != gjson.String || encoded.Str == "" {
		return gjson.Result{}, errors.New("image stream event has no base64 image data")
	}
	event["data"] = json.RawMessage(`[{"b64_json":` + encoded.Raw + `}]`)
	delete(event, "b64_json")
	data, err := json.Marshal(event)
	if err != nil {
		return gjson.Result{}, errors.New("could not normalize image stream event")
	}
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(data), nil
}
