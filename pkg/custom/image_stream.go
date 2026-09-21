package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
)

type imageGenerationStream interface {
	Next() bool
	Current() openai.ImageGenStreamEventUnion
	Err() error
	Close() error
}

// Adapt the generated output hook without changing who reads or closes the
// underlying SDK stream. No event is read ahead of the presentation consumer.
type imageOutputStream[T any] struct {
	source  jsonview.Iterator[T]
	current openai.ImageGenStreamEventUnion
	err     error
}

func (stream *imageOutputStream[T]) Next() bool {
	if stream.err != nil || !stream.source.Next() {
		return false
	}
	switch event := any(stream.source.Current()).(type) {
	case openai.ImageGenStreamEventUnion:
		stream.current = event
	case openai.ImageEditStreamEventUnion:
		// The two SDK unions expose the same image fields. Preserve raw event JSON
		// so normalization retains metadata and any future response fields.
		stream.err = json.Unmarshal([]byte(event.RawJSON()), &stream.current)
		if stream.err != nil {
			stream.err = errors.New("could not read image edit stream event")
		}
	default:
		stream.err = errors.New("unexpected image stream event type")
	}
	return stream.err == nil
}

func (stream *imageOutputStream[T]) Current() openai.ImageGenStreamEventUnion { return stream.current }
func (stream *imageOutputStream[T]) Err() error {
	if stream.err != nil {
		return stream.err
	}
	return stream.source.Err()
}
func (stream *imageOutputStream[T]) Close() error {
	if closer, ok := stream.source.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// saveStream keeps progress images temporary and saves only the completed image
// through the same path as ordinary generation. A completed event is terminal:
// waiting for another event must not turn a saved image into a failed request.
// normalize is selected once by the shared output boundary for the actual route.
func (p *imageOutputPlan) saveStream(ctx context.Context, stream imageGenerationStream, out io.Writer, normalize transformers.Transformer) (resultErr error) {
	defer stream.Close()
	var temporaryDirectory string
	defer func() {
		if temporaryDirectory == "" {
			return
		}
		if err := os.RemoveAll(temporaryDirectory); err != nil {
			cleanup := fmt.Errorf("could not remove temporary image previews in %q", temporaryDirectory)
			if resultErr != nil {
				resultErr = errors.Join(resultErr, cleanup)
			} else {
				// The final image has already been saved; cleanup needs no new
				// API request and must not invite generation again.
				_, resultErr = fmt.Fprintf(out, "Your final image is saved. Remove temporary previews from %q.\n", temporaryDirectory)
			}
		}
	}()
	seen := make(map[int64]bool)
	previewUnavailable := false
	if err := ctx.Err(); err != nil {
		return err
	}
	for stream.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := stream.Current()
		switch event.Type {
		case "image_generation.completed", "image_edit.completed":
			response, err := imageEventResponse(ctx, event, normalize)
			if err != nil {
				return err
			}
			return p.save(ctx, response, out)
		case "image_generation.partial_image", "image_edit.partial_image":
			index := event.PartialImageIndex
			if previewUnavailable || p.partialImages < 1 || p.partialImages > 3 ||
				(p.preview == "" && !p.textPreview) || index < 0 || index >= p.partialImages || seen[index] {
				continue
			}
			// Track attempts, including malformed previews, so duplicated or
			// unexpected events cannot produce unbounded terminal output.
			seen[index] = true
			var err error
			if temporaryDirectory == "" {
				temporaryDirectory, err = os.MkdirTemp("", "openai-image-preview-*")
			}
			if err == nil {
				var response []byte
				response, err = imageEventResponse(ctx, event, normalize)
				if err == nil {
					err = p.renderImageProgress(ctx, out, temporaryDirectory, response, index)
				}
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				previewUnavailable = true
				if _, err := fmt.Fprintln(out, "Progress preview unavailable; waiting for the final image."); err != nil {
					return err
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := stream.Err(); err != nil {
		var apierr *openai.Error
		if errors.As(err, &apierr) {
			return err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		// Stream errors may quote the prompt, credentials or malformed event.
		// Keep their text out of the terminal while retaining a clear next step.
		return errors.New("the image stream stopped before a final image was received; check your API usage before trying again")
	}
	return errors.New("the image stream ended without a final image; check your API usage before trying again")
}

func imageEventResponse(ctx context.Context, event openai.ImageGenStreamEventUnion, normalize transformers.Transformer) ([]byte, error) {
	raw := event.RawJSON()
	if raw == "" {
		data, err := json.Marshal(event)
		if err != nil {
			return nil, errors.New("could not read image stream event")
		}
		raw = string(data)
	}
	// The output boundary selected this normalizer once for the actual operation.
	// Saving consumes that selection without registering or selecting its own
	// route; explicit API-data streams never enter this presentation consumer.
	response, err := transformOutput(ctx, gjson.Parse(raw), normalize)
	return []byte(response.Raw), err
}

func (p *imageOutputPlan) renderImageProgress(ctx context.Context, out io.Writer, directory string, response []byte, index int64) error {
	paths, err := imageoutput.SaveResponse(ctx, response, directory, "progress")
	if err != nil {
		return err
	}
	path := paths[0]
	defer os.Remove(path) // The private directory is also removed by saveStream.
	if _, err := fmt.Fprintf(out, "Progress preview %d of %d:\n", index+1, p.partialImages); err != nil {
		return err
	}
	return renderImagePreview(ctx, out, path, p.preview, p.textColor, p.textTrueColor)
}
