package custom

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/tidwall/gjson"
)

// Progress follows the same explicit font opt-in as the final image. The
// progress renderer reserves gallery space for the final image's placement.
func imageProgressProtocol(mode string, terminal bool, getenv func(string) string) string {
	return savedImageProtocol(mode, terminal, getenv)
}

// A malformed optional preview does not prevent receiving the final image.
// Rendering and output failures propagate, including cancellation, so callers
// never continue writing another image after partially failed terminal output.
func (p *imageOutputPlan) displayImageProgress(ctx context.Context, event gjson.Result, out io.Writer, protocol string) (unavailable bool, err error) {
	warn := func(message string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return true, err
		}
		// This notice belongs with the terminal progress. Keep stderr clear for
		// a later structured API, saving, or cancellation error.
		return true, readable.WriteText(outputWriter{ctx: ctx, out: out}, message)
	}
	const unavailableMessage = "Progress preview unavailable; waiting for the final image."
	encoded := event.Get("b64_json")
	if encoded.Type != gjson.String || encoded.String() == "" {
		return warn(unavailableMessage)
	}
	img, err := terminalimage.DecodePreview(ctx, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(encoded.String())))
	if err != nil {
		return warn(unavailableMessage)
	}
	if _, err := fmt.Fprintf(outputWriter{ctx: ctx, out: out}, "Progress preview %d of %d:\n", event.Get("partial_image_index").Int()+1, p.partialImages); err != nil {
		return false, err
	}
	// Share the saved-image sizing calculation, but not its recovery messages:
	// the final image has not arrived yet and there is no output file to retry.
	file, ok := out.(*os.File)
	if !ok || !isTerminal(out) {
		return false, errors.New("inline previews require a terminal")
	}
	width, height, sizeErr := term.GetSize(file.Fd())
	if sizeErr != nil {
		return warn(unavailableMessage)
	}
	cellWidth, cellHeight := terminalimage.CellSize(file.Fd())
	columns := savedImagePreviewColumns(img.Bounds(), width, height, cellWidth, cellHeight)
	if columns < 1 {
		return warn(unavailableMessage)
	}
	if protocol == "font" {
		columns = min(columns, 32)
	}
	if protocol == "blocks" {
		if err := readable.WriteText(outputWriter{ctx: ctx, out: out}, "Inline preview (color approximation):"); err != nil {
			return false, err
		}
	}
	err = terminalimage.WriteProgress(ctx, out, img, protocol, columns)
	if ctx.Err() != nil {
		return false, errors.Join(err, ctx.Err())
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, err
	}
	var fontErr *terminalimage.FontError
	if errors.As(err, &fontErr) {
		// No image characters were written. Rollback failures remain fatal.
		// Do not substitute a pixelated preview for the requested sharp one.
		return warn("Sharp progress preview unavailable; waiting for the final image.")
	}
	if err != nil {
		return false, err
	}
	_, err = fmt.Fprintln(outputWriter{ctx: ctx, out: out})
	return false, err
}
