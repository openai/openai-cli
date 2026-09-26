package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

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

// Scalar @file references arrive as upload readers after shared trusted-input
// expansion. Inspect their contents once and replay the exact original bytes,
// retaining multipart filenames/content types. Source image and mask readers
// never enter this path and stay streamed. There is no new file-size limit.
func inspectImageMultipartSetting(ctx context.Context, name string, value any) (any, any, error) {
	if text, ok := value.(string); ok {
		return inspectImageMultipartScalar(name, text), value, nil
	}
	reader, ok := value.(io.Reader)
	if !ok {
		return value, value, nil
	}
	upload, owned := value.(fileUpload)
	var closeOnce sync.Once
	var closeErr error
	closeSource := func() {
		if owned {
			closeOnce.Do(func() { closeErr = upload.Close() })
		}
	}
	cancelDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(cancelDone); closeSource() })
	data, readErr := io.ReadAll(imageSettingReader{ctx, reader})
	if !stop() {
		<-cancelDone
	}
	closeSource()
	var replay any = bytes.NewReader(data)
	if owned {
		upload.Reader = bytes.NewReader(data)
		upload.size, upload.knownSize = int64(len(data)), true
		replay = upload
	}
	if err := ctx.Err(); err != nil {
		return nil, replay, err
	}
	if readErr != nil || closeErr != nil {
		// Reader errors may quote file contents. Keep only the known option name.
		return nil, replay, imageSavingFailure(fmt.Sprintf("could not read --%s from its file", strings.ReplaceAll(name, "_", "-")), errors.Join(readErr, closeErr))
	}
	return inspectImageMultipartScalar(name, string(data)), replay, nil
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

type imageSettingReader struct {
	context context.Context
	source  io.Reader
}

func (r imageSettingReader) Read(buffer []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(buffer)
}
