// Package terminalimage writes native images, image fonts, or color-block previews.
package terminalimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/openai/openai-cli/internal/imagefont"
	"golang.org/x/image/draw"
)

// Write displays img at the requested width in terminal cells, preserving its
// aspect ratio. iterm-auto delegates sizing to the terminal's live viewport.
// The caller selects the protocol and supplies a terminal writer.
func Write(ctx context.Context, w io.Writer, img image.Image, protocol string, columns int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if img == nil || img.Bounds().Empty() {
		return errors.New("cannot display an empty image")
	}
	destination := contextWriter{ctx, w}
	switch protocol {
	case "kitty":
		// Keep native cursor movement enabled (C=0): the terminal advances over
		// the placement using its actual cell size; the caller adds a newline.
		return writeKittyImage(ctx, w, img, columns)
	case "iterm":
		return writeITermImage(ctx, w, img, columns)
	case "iterm-auto":
		// xterm's IIP renderer fits auto/auto within its current viewport.
		// An explicit width overrides its height limit, and OS window metadata
		// does not reliably describe VS Code's cell size (including on Windows).
		return writeITermOutput(ctx, w, func(destination io.Writer) error {
			return writeITermImage(ctx, destination, img, 0)
		})
	case "blocks":
		cellWidth, cellHeight := 1, 2
		if file, ok := w.(*os.File); ok {
			cellWidth, cellHeight = CellSize(file.Fd())
		}
		return writeColorBlocks(destination, img, columns, cellWidth, cellHeight)
	case "font":
		return writeImageFont(ctx, w, img, columns)
	default:
		return errors.New("unsupported terminal image protocol")
	}
}

// WriteProgress uses the same renderer as a final image, while leaving capacity
// for one final font preview. Sharp progress keeps immutable private cache files
// because changing their glyphs would change earlier terminal scrollback.
func WriteProgress(ctx context.Context, w io.Writer, img image.Image, protocol string, columns int) error {
	if protocol != "font" {
		return Write(ctx, w, img, protocol, columns)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if img == nil || img.Bounds().Empty() {
		return errors.New("cannot display an empty image")
	}
	return writeImageFontReserved(ctx, w, img, columns, maxFontPreviewColumns*imagefont.MaxFrameRows)
}

func writeColorBlocks(out io.Writer, img image.Image, columns, cellWidth, cellHeight int) error {
	if columns < 1 {
		columns = 80
	}
	// Each character represents two vertical samples. Account for physical cell
	// proportions so square source pixels are not stretched by font/line spacing.
	// Keep the ratio integral so exact half-cell boundaries do not shift through
	// floating-point rounding. Caller-bounded image/cell dimensions fit in int64.
	numerator := int64(img.Bounds().Dy()) * int64(columns) * 2 * int64(cellWidth)
	denominator := int64(img.Bounds().Dx()) * int64(cellHeight)
	height := max(1, int((numerator+denominator/2)/denominator))
	preview := image.NewRGBA(image.Rect(0, 0, columns, height))
	background := color.RGBA{24, 24, 24, 255}
	draw.Draw(preview, preview.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	draw.ApproxBiLinear.Scale(preview, preview.Bounds(), img, img.Bounds(), draw.Over, nil)
	for y := 0; y < height; y += 2 {
		var row strings.Builder
		for x := 0; x < columns; x++ {
			top := ansi.Convert256(preview.At(x, y))
			bottom := ansi.Convert256(background)
			if y+1 < height {
				bottom = ansi.Convert256(preview.At(x, y+1))
			}
			fmt.Fprintf(&row, "\x1b[38;5;%d;48;5;%dm▀", top, bottom)
		}
		row.WriteString("\x1b[0m")
		if y+2 < height {
			row.WriteByte('\n')
		}
		if _, err := io.WriteString(out, row.String()); err != nil {
			return err
		}
	}
	return nil
}

type contextWriter struct {
	context context.Context
	writer  io.Writer
}

func (w contextWriter) Write(data []byte) (int, error) {
	if err := w.context.Err(); err != nil {
		return 0, err
	}
	n, err := w.writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, errors.Join(err, w.context.Err())
}

// Encode into a checked writer: kitty.EncodeGraphics buffers the entire PNG
// without checking cancellation. Reuse Charm's options/framing after encoding.
func writeKittyImage(ctx context.Context, out io.Writer, img image.Image, columns int) (err error) {
	var encoded bytes.Buffer
	if err := imagefont.EncodePNG(ctx, &png.Encoder{}, &encoded, img); err != nil {
		return err
	}
	return writeKittyOutput(ctx, out, func(destination io.Writer) error {
		return writeKittyFrames(ctx, destination, &encoded, columns)
	})
}

func writeKittyFrames(ctx context.Context, out io.Writer, encoded *bytes.Buffer, columns int) (err error) {
	options := (&kitty.Options{Action: kitty.TransmitAndPut, Transmission: kitty.Direct, Format: kitty.PNG, Quite: 2, Columns: columns}).Options()
	destination := contextWriter{ctx, out}
	incomplete := false
	defer func() {
		if incomplete {
			// ST closes an APC, but not a chunked upload. Finish that upload
			// quietly so a later image cannot be appended to its partial PNG.
			// Do not delete images: earlier placements belong to scrollback.
			// Keep cancellation: an arbitrary writer may block in cleanup.
			// Native terminal workers have a separate bounded transport reset.
			_, cleanupErr := io.WriteString(destination, "\x1b_Gq=2,m=0;\x1b\\")
			err = errors.Join(err, cleanupErr)
		}
	}()
	first := true
	for encoded.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		opts := []string{"q=2"}
		if first {
			opts = append([]string(nil), options...)
		}
		rawChunk := kitty.MaxChunkSize / 4 * 3
		if framed, ok := out.(interface{ kittyFrameSize() int }); ok {
			// Include the first frame's options and ST in the transport budget.
			// An empty payload omits the semicolon. Measure a nonempty frame.
			header := len(ansi.KittyGraphics([]byte{'x'}, append(opts, "m=1")...)) - 1
			rawChunk = min(rawChunk, (framed.kittyFrameSize()-header)/4*3)
			if rawChunk <= 0 {
				return errors.New("Kitty frame options exceed the transport budget")
			}
		}
		data := encoded.Next(rawChunk)
		payload := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
		base64.StdEncoding.Encode(payload, data)
		if encoded.Len() > 0 {
			opts = append(opts, "m=1")
		} else {
			opts = append(opts, "m=0")
		}
		frame := ansi.KittyGraphics(payload, opts...)
		n, writeErr := io.WriteString(destination, frame)
		if n > 0 {
			incomplete = n < len(frame) || encoded.Len() > 0
		}
		if writeErr != nil {
			if n > 0 && n < len(frame) {
				// Each chunk owns one APC. A partial write can leave it open;
				// close it before the deferred final chunk, retaining all errors.
				terminator := "\x1b\\"
				if frame[n-1] == '\x1b' {
					// Complete an accepted ESC rather than doubling it, which
					// can leave a literal backslash in the terminal's text.
					terminator = "\\"
				}
				_, cleanupErr := io.WriteString(destination, terminator)
				return errors.Join(writeErr, cleanupErr)
			}
			return writeErr
		}
		first = false
	}
	return ctx.Err()
}
