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
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/iterm2"
	"github.com/charmbracelet/x/ansi/kitty"
	"golang.org/x/image/draw"
)

// Write displays img at the requested width in terminal cells, preserving its
// aspect ratio. The caller selects the protocol and supplies a terminal writer.
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
		return writeKittyImage(ctx, destination, img, columns)
	case "iterm":
		var encoded bytes.Buffer
		if err := png.Encode(contextWriter{ctx, &encoded}, img); err != nil {
			return err
		}
		width := iterm2.Auto
		if columns > 0 {
			width = iterm2.Cells(columns)
		}
		_, err := io.WriteString(destination, ansi.ITerm2(iterm2.File{
			Inline: true, Width: width, Height: iterm2.Auto,
			Content: []byte(base64.StdEncoding.EncodeToString(encoded.Bytes())),
		}))
		return err
	case "blocks":
		if columns < 1 {
			columns = 80
		}
		height := max(1, img.Bounds().Dy()*columns/img.Bounds().Dx())
		preview := image.NewRGBA(image.Rect(0, 0, columns, height))
		draw.Draw(preview, preview.Bounds(), image.NewUniform(color.RGBA{24, 24, 24, 255}), image.Point{}, draw.Src)
		draw.ApproxBiLinear.Scale(preview, preview.Bounds(), img, img.Bounds(), draw.Over, nil)
		for y := 0; y < height; y += 2 {
			var row strings.Builder
			for x := 0; x < columns; x++ {
				top := ansi.Convert256(preview.At(x, y))
				bottom := ansi.Convert256(preview.At(x, min(y+1, height-1)))
				fmt.Fprintf(&row, "\x1b[38;5;%d;48;5;%dm▀", top, bottom)
			}
			row.WriteString("\x1b[0m")
			if y+2 < height {
				row.WriteByte('\n')
			}
			if _, err := io.WriteString(destination, row.String()); err != nil {
				return err
			}
		}
		return nil
	case "font":
		return writeImageFont(ctx, w, img, columns)
	default:
		return errors.New("unsupported terminal image protocol")
	}
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
func writeKittyImage(ctx context.Context, out io.Writer, img image.Image, columns int) error {
	var encoded bytes.Buffer
	if err := png.Encode(contextWriter{ctx, &encoded}, img); err != nil {
		return err
	}
	options := (&kitty.Options{Action: kitty.TransmitAndPut, Transmission: kitty.Direct, Format: kitty.PNG, Quite: 2, Columns: columns}).Options()
	const rawChunk = kitty.MaxChunkSize / 4 * 3
	first := true
	for encoded.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		data := encoded.Next(rawChunk)
		payload := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
		base64.StdEncoding.Encode(payload, data)
		opts := []string{"q=2"}
		if first {
			opts = append([]string(nil), options...)
		}
		if encoded.Len() > 0 {
			opts = append(opts, "m=1")
		} else {
			opts = append(opts, "m=0")
		}
		if _, err := io.WriteString(out, ansi.KittyGraphics(payload, opts...)); err != nil {
			return err
		}
		first = false
	}
	return ctx.Err()
}
