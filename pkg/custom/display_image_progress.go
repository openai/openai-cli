package custom

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/tidwall/gjson"
)

// Progress images are temporary. Font previews retain immutable cache entries
// for scrollback, so reserve that path for the saved final image.
func imageProgressProtocol(mode string, terminal bool, getenv func(string) string) string {
	protocol := savedImageProtocol(mode, terminal, getenv)
	if protocol == "font" {
		if savedImageBlockColor(getenv) {
			return "blocks"
		}
		return ""
	}
	return protocol
}

// A malformed optional preview does not prevent receiving the final image.
// Rendering and output failures propagate, including cancellation, so callers
// never continue writing another image after partially failed terminal output.
func (p *imageOutputPlan) displayImageProgress(ctx context.Context, event gjson.Result, out io.Writer, protocol string) (unavailable bool, err error) {
	warn := func() (bool, error) {
		if err := ctx.Err(); err != nil {
			return true, err
		}
		// This notice belongs with the terminal progress. Keep stderr clear for
		// a later structured API, saving, or cancellation error.
		return true, readable.WriteText(outputWriter{ctx: ctx, out: out}, "Progress preview unavailable; waiting for the final image.")
	}
	encoded := event.Get("b64_json")
	if encoded.Type != gjson.String || encoded.String() == "" {
		return warn()
	}
	img, err := terminalimage.DecodePreview(ctx, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(encoded.String())))
	if err != nil {
		return warn()
	}
	if _, err := fmt.Fprintf(outputWriter{ctx: ctx, out: out}, "Progress preview %d of %d:\n", event.Get("partial_image_index").Int()+1, p.partialImages); err != nil {
		return false, err
	}
	// Geometry diagnostics for saved images mention retrying the file. Progress
	// has no saved file; use the concise progress message if it cannot fit.
	var diagnostic bytes.Buffer
	err = displayDecodedSavedImage(ctx, img, out, &diagnostic, protocol)
	if errors.Is(err, errImagePreviewUnavailable) {
		return warn()
	}
	return false, err
}
