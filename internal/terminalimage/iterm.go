package terminalimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"

	"github.com/charmbracelet/x/ansi/iterm2"
	"github.com/openai/openai-cli/internal/imagefont"
)

func writeITermImage(ctx context.Context, out io.Writer, img image.Image, columns int) error {
	return writeITermImageRows(ctx, out, img, columns, 0)
}

// WriteITermRegion fits one contiguous portion of the source into a single
// attachment. Terminal-side aspect fitting then adds padding only at its edges.
func WriteITermRegion(ctx context.Context, out io.Writer, img image.Image, columns, totalRows, first, rows int) error {
	if img == nil || img.Bounds().Empty() || columns < 1 || totalRows < 1 || first < 0 || rows < 1 || first > totalRows-rows {
		return errors.New("invalid iTerm2 image region")
	}
	if first != 0 || rows != totalRows {
		bounds := img.Bounds()
		top := bounds.Min.Y + int(int64(first)*int64(bounds.Dy())/int64(totalRows))
		end := bounds.Min.Y + int(int64(first+rows)*int64(bounds.Dy())/int64(totalRows))
		img = previewStripe{Image: img, bounds: image.Rect(bounds.Min.X, top, bounds.Max.X, max(top+1, end))}
	}
	return writeITermImageRows(ctx, out, img, columns, rows)
}

func writeITermImageRows(ctx context.Context, out io.Writer, img image.Image, columns, rows int) error {
	// Finish PNG preparation before starting an OSC, so cancellation or an
	// encoding error cannot leave terminal output behind. Only this one image
	// buffer is retained; base64 and protocol framing are streamed below.
	var encoded bytes.Buffer
	if err := imagefont.EncodePNG(ctx, &png.Encoder{}, &encoded, img); err != nil {
		return err
	}
	return writeITermPayloadRows(ctx, out, encoded.Bytes(), columns, rows)
}

func writeITermPayload(ctx context.Context, out io.Writer, encoded []byte, columns int) (err error) {
	return writeITermPayloadRows(ctx, out, encoded, columns, 0)
}

func writeITermPayloadRows(ctx context.Context, out io.Writer, encoded []byte, columns, rows int) (err error) {
	width := iterm2.Auto
	if columns > 0 {
		width = iterm2.Cells(columns)
	}
	// Released xterm image addons require the decoded byte count, even though
	// newer implementations also accept a payload without it.
	height := iterm2.Auto
	if rows > 0 {
		height = iterm2.Cells(rows)
	}
	header := "\x1b]1337;" + (iterm2.File{Size: int64(len(encoded)), Inline: true, Width: width, Height: height}).String() + ":"
	destination := contextWriter{ctx, out}
	started, complete := false, false
	defer func() {
		if started && !complete {
			// ST also terminates a partial OSC/header. Attempt only these two
			// bytes after cancellation, and retain any output/cleanup errors.
			_, cleanupErr := io.WriteString(contextWriter{context.WithoutCancel(ctx), out}, "\x1b\\")
			err = errors.Join(err, cleanupErr)
		}
	}()
	n, err := io.WriteString(destination, header)
	started = n > 0
	if err != nil {
		return err
	}
	encoder := base64.NewEncoder(base64.StdEncoding, destination)
	if _, err := encoder.Write(encoded); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	n, err = io.WriteString(destination, "\x07")
	complete = n == 1
	return err
}
