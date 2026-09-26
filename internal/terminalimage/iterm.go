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
	// Finish PNG preparation before starting an OSC, so cancellation or an
	// encoding error cannot leave terminal output behind. Only this one image
	// buffer is retained; base64 and protocol framing are streamed below.
	var encoded bytes.Buffer
	if err := imagefont.EncodePNG(ctx, &png.Encoder{}, &encoded, img); err != nil {
		return err
	}
	return writeITermPayload(ctx, out, encoded.Bytes(), columns)
}

func writeITermPayload(ctx context.Context, out io.Writer, encoded []byte, columns int) (err error) {
	width := iterm2.Auto
	if columns > 0 {
		width = iterm2.Cells(columns)
	}
	header := "\x1b]1337;" + (iterm2.File{Inline: true, Width: width, Height: iterm2.Auto}).String() + ":"
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
