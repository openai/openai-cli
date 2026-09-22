// Package terminalimage renders saved images using native graphics, Apple
// Terminal's opt-in image font, or a text approximation. It owns terminal
// detection and rendering; API requests and image saving belong to callers.
// The caller is responsible for selecting a supported, interactive terminal.
package terminalimage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"os"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Protocol string

const (
	ITerm2 Protocol = "iterm2"
	Kitty  Protocol = "kitty"
	// This is an optional-preview allocation budget, not an API payload or
	// saved-image limit. Full-resolution originals remain available on disk.
	maxPreviewPixels = 32 * 1024 * 1024
	// Kernel resampling also allocates by source height and destination width.
	// Bound each axis so a thin image cannot exhaust memory while being scaled.
	maxPreviewDimension = 16 * 1024
)

// Render displays a PNG thumbnail without changing the saved image. It prepares
// all image data before emitting escape sequences, and never reads terminal
// input or asks the terminal to open or download a file. An unknown terminal
// size uses a conservative 80-by-24 fallback. Pixel dimensions, when available,
// let Kitty fit both bounds; otherwise its width is bounded and height estimated.
// The cursor finishes at column one below the preview on success.
func Render(ctx context.Context, w io.Writer, path string, protocol Protocol, size Size) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if protocol != ITerm2 && protocol != Kitty {
		return errors.New("unsupported image preview protocol")
	}
	maxEdge := 1024
	if protocol == ITerm2 {
		// Even an incompressible RGBA PNG stays below a 1 MiB OSC after
		// base64 encoding; older receivers need no multipart support.
		maxEdge = 400
	}
	data, width, height, err := thumbnail(ctx, path, maxEdge)
	if err != nil {
		return err
	}
	columns, rows := previewSize(size.Columns, size.Rows)
	output := contextWriter{ctx: ctx, writer: w}
	var imageID uint32
	if protocol == Kitty {
		var randomID [4]byte
		if _, err := rand.Read(randomID[:]); err != nil {
			return err
		}
		imageID = max(1, binary.BigEndian.Uint32(randomID[:]))
	}
	// Close an interrupted control string before the caller prints a fallback.
	// Bypass cancellation only for this fixed, best-effort terminal reset.
	defer func() {
		if err != nil {
			_, _ = io.WriteString(w, "\x18\x1b\\")
			if protocol == Kitty {
				// A delete aborts an unfinished transfer. Target our image only;
				// never clear other images already visible in the terminal.
				_, _ = fmt.Fprintf(w, "\x1b_Ga=d,d=I,i=%d,q=2;\x1b\\", imageID)
			}
			_, _ = io.WriteString(w, "\r\n")
		}
	}()
	if protocol == ITerm2 {
		// OSC 1337: explicitly select inline mode; the default downloads files.
		// https://iterm2.com/documentation-images.html
		if _, err = fmt.Fprintf(output, "\r\x1b]1337;File=inline=1;size=%d;width=%d;height=%d;preserveAspectRatio=1:", len(data), columns, rows); err != nil {
			return err
		}
		encoder := base64.NewEncoder(base64.StdEncoding, output)
		if _, err = encoder.Write(data); err != nil {
			return err
		}
		if err = encoder.Close(); err != nil {
			return err
		}
		_, err = io.WriteString(output, "\x1b\\\r\n")
		return err
	}

	// Kitty preserves aspect ratio when only one of c/r is specified.
	// Native cursor advancement follows that actual placement, then CRLF puts
	// subsequent text below it without guessing its occupied row count.
	// https://sw.kovidgoyal.net/kitty/graphics-protocol/
	layout := kittyLayout(size, width, height, columns, rows)
	encoded := base64.StdEncoding.EncodeToString(data)
	if _, err = io.WriteString(output, "\r"); err != nil {
		return err
	}
	for first := true; len(encoded) > 0; first = false {
		n := min(len(encoded), 4096)
		more := 0
		if n < len(encoded) {
			more = 1
		}
		header := ""
		if first {
			header = fmt.Sprintf("a=T,t=d,f=100,i=%d,%s,", imageID, layout)
		}
		// Quiet mode prevents terminal replies from becoming shell input. Each
		// chunk is a complete APC; only q and m appear on continuation chunks.
		if _, err = fmt.Fprintf(output, "\x1b_G%sq=2,m=%d;%s\x1b\\", header, more, encoded[:n]); err != nil {
			return err
		}
		encoded = encoded[n:]
	}
	_, err = io.WriteString(output, "\r\n")
	return err
}

func kittyLayout(size Size, width, height, columns, rows int) string {
	aspect := float64(width) / float64(height)
	if size.Columns > 0 && size.Rows > 0 && size.PixelWidth > 0 && size.PixelHeight > 0 {
		cellWidth := float64(size.PixelWidth) / float64(size.Columns)
		cellHeight := float64(size.PixelHeight) / float64(size.Rows)
		if aspect <= float64(columns)*cellWidth/(float64(rows)*cellHeight) {
			return fmt.Sprintf("r=%d", rows)
		}
	} else {
		// Without pixel geometry, always constrain width to prevent clipping
		// with unusual font proportions. Estimate height with 2:1 cells; only
		// the vertical footprint is approximate, never the image aspect ratio.
		columns = min(columns, max(1, int(math.Floor(float64(rows)*2*aspect))))
	}
	return fmt.Sprintf("c=%d", columns)
}

func previewSize(columns, rows int) (int, int) {
	if columns <= 0 {
		columns = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return max(1, min(columns-2, 80)), max(1, min(rows/2, 20))
}

// loadPreviewImage validates dimensions before allocating decoded pixels.
// Both native graphics and text renderers share this preview-only guard.
func loadPreviewImage(ctx context.Context, path string) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open saved image for preview: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("image preview requires a regular file")
	}
	config, _, err := image.DecodeConfig(contextReader{ctx, file})
	if err != nil {
		return nil, fmt.Errorf("inspect saved image for preview: %w", err)
	}
	// Reject only the optional preview before decoding allocates source pixels.
	// Division avoids width*height overflow. The saved/API image is unaffected.
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxPreviewPixels/config.Height {
		return nil, errors.New("saved image exceeds the inline preview pixel budget; open the saved image to view it")
	}
	if config.Width > maxPreviewDimension || config.Height > maxPreviewDimension {
		return nil, errors.New("saved image exceeds the inline preview dimension budget; open the saved image to view it")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	source, _, err := image.Decode(contextReader{ctx, file})
	if err != nil {
		return nil, fmt.Errorf("decode saved image for preview: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return source, nil
}

func thumbnail(ctx context.Context, path string, maxEdge int) ([]byte, int, int, error) {
	source, err := loadPreviewImage(ctx, path)
	if err != nil {
		return nil, 0, 0, err
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, 0, 0, errors.New("image preview has empty dimensions")
	}
	// Bound only the thumbnail, never modify the full-resolution saved image.
	// Filter across source pixels so fine features do not alias or disappear.
	scale := math.Min(1, float64(maxEdge)/float64(max(width, height)))
	small := image.NewNRGBA(image.Rect(0, 0, max(1, int(math.Round(float64(width)*scale))), max(1, int(math.Round(float64(height)*scale)))))
	if scale < 1 {
		draw.CatmullRom.Scale(small, small.Bounds(), source, bounds, draw.Src, nil)
	} else {
		// Normalize even unscaled 16-bit images to bounded, 8-bit NRGBA; a
		// 400px RGBA64 PNG can otherwise exceed older OSC receiver limits.
		draw.Draw(small, small.Bounds(), source, bounds.Min, draw.Src)
	}
	var result bytes.Buffer
	if err := png.Encode(contextWriter{ctx, &result}, small); err != nil {
		return nil, 0, 0, fmt.Errorf("encode image preview: %w", err)
	}
	return result.Bytes(), small.Bounds().Dx(), small.Bounds().Dy(), ctx.Err()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
