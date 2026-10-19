package custom

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const imageMultipartMetadata = "openai-image-multipart-preparation"

type imageMultipartPreparation struct {
	context   context.Context
	plan      *imageOutputPlan
	streaming bool
	body      io.Closer
}

// Inspect merged scalar settings after trusted file expansion, before the
// existing multipart encoder takes ownership. Source images and masks remain
// streamed and are never decoded, buffered, or rewritten here.
func prepareImageMultipartBody(command *cli.Command, body map[string]any) error {
	state, ok := command.Metadata[imageMultipartMetadata].(*imageMultipartPreparation)
	if !ok {
		return nil
	}
	settings := make(map[string]any)
	for _, name := range []string{"prompt", "model", "n", "size", "quality", "output_format", "background", "response_format", "partial_images", "stream"} {
		if value, found := body[name]; found {
			inspected, replay, err := inspectImageMultipartSetting(state.context, name, value)
			body[name] = replay
			if err != nil {
				return imageSavingFailure(err.Error(), err)
			}
			settings[name] = inspected
		}
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return imageSavingFailure("Could not read image settings.", err)
	}
	parsed := gjson.ParseBytes(raw)
	if command.Name == "create-variation" && (parsed.Get("stream").Bool() || parsed.Get("partial_images").Int() > 0) {
		return imageSavingFailure("Image variations do not support streaming; remove stream and partial_images from your input.", nil)
	}
	state.plan, state.streaming, err = prepareImageSaving(state.context, command, parsed)
	if err != nil {
		return err
	}
	if state.plan != nil {
		for name, value := range state.plan.defaults {
			body[name] = value
		}
	}
	return nil
}

// Inspect scalar uploads with shared ownership; normalization remains image-specific.
func inspectImageMultipartSetting(ctx context.Context, name string, value any) (any, any, error) {
	inspected, replay, err := requestconfig.ReadMultipartScalar(ctx, name, value)
	var readFailure *requestconfig.MultipartScalarError
	if errors.As(err, &readFailure) {
		return nil, replay, imageSavingFailure(readFailure.Error(), readFailure.Unwrap())
	}
	if err != nil {
		return nil, replay, err
	}
	if text, ok := inspected.(string); ok {
		inspected = inspectImageMultipartScalar(name, text)
	}
	return inspected, replay, nil
}

// Multipart scalar text has the same meaning whether supplied by stdin or a
// file. Normalize only the inspection view; replay keeps the original bytes.
func inspectImageMultipartScalar(name, text string) any {
	if name == "n" || name == "partial_images" || name == "stream" {
		parsed := gjson.Parse(text)
		if gjson.Valid(text) && (parsed.Type == gjson.Number || parsed.Type == gjson.True || parsed.Type == gjson.False || parsed.Type == gjson.Null) {
			return json.RawMessage(text)
		}
	}
	return text
}
