package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/openai/openai-go/v3"
)

type imageGenerationStream interface {
	Next() bool
	Current() openai.ImageGenStreamEventUnion
	Err() error
	Close() error
}

// saveStream keeps progress images temporary and saves only the completed image
// through the same path as ordinary generation. A completed event is terminal:
// waiting for another event must not turn a saved image into a failed request.
func (p *imageOutputPlan) saveStream(ctx context.Context, stream imageGenerationStream, out io.Writer) (resultErr error) {
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
		case "image_generation.completed":
			return p.save(ctx, imageStreamResponse(event.B64JSON), out)
		case "image_generation.partial_image":
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
				err = p.renderImageProgress(ctx, out, temporaryDirectory, event.B64JSON, index)
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

func imageStreamResponse(encoded string) []byte {
	// This JSON envelope contains only a string, so marshaling cannot fail.
	response, _ := json.Marshal(struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
	}{Data: []struct {
		Base64 string `json:"b64_json"`
	}{{Base64: encoded}}})
	return response
}

func (p *imageOutputPlan) renderImageProgress(ctx context.Context, out io.Writer, directory, encoded string, index int64) error {
	paths, err := imageoutput.SaveResponse(ctx, imageStreamResponse(encoded), directory, "progress")
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
