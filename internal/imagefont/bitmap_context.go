package imagefont

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"io"

	"golang.org/x/image/draw"
)

// ScaleBitmap scales pixels with Catmull-Rom interpolation, replacing dr in
// dst. Cancellation stops both filter passes before returning to the caller.
func ScaleBitmap(ctx context.Context, dst draw.Image, dr image.Rectangle, src image.Image, sr image.Rectangle) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	progress := &bitmapProgress{ctx: ctx}
	defer progress.recoverCancellation(&err)
	kernel := *draw.CatmullRom
	kernel.At = func(t float64) float64 {
		progress.check()
		return draw.CatmullRom.At(t)
	}
	kernel.Scale(bitmapDestination{Image: dst, progress: progress}, dr, bitmapSource{Image: src, progress: progress}, sr, draw.Src, nil)
	return ctx.Err()
}

// EncodePNG checks cancellation while scanning pixels and writing compressed
// data, including highly compressible images that rarely flush their output.
// The PNG library still filters and compresses each scanline between callbacks;
// font bitmaps bound their width before calling this helper.
func EncodePNG(ctx context.Context, encoder *png.Encoder, dst io.Writer, src image.Image) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	progress := &bitmapProgress{ctx: ctx}
	defer progress.recoverCancellation(&err)
	wrapped := bitmapSource{Image: src, progress: progress}
	var pixels image.Image = wrapped
	if palette, ok := src.(image.PalettedImage); ok {
		pixels = bitmapPalette{bitmapSource: wrapped, palette: palette}
	}
	err = encoder.Encode(bitmapWriter{ctx: ctx, Writer: dst}, pixels)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// The scaler and PNG encoder have no error-returning pixel callback. A private
// sentinel stops them synchronously, including PNG's initial opacity scan and
// both scaling passes. Only this call's sentinel is recovered; image and writer
// panics still propagate. No worker continues using pixels after cancellation.
type bitmapProgress struct {
	ctx    context.Context
	pixels uint32
}

func (p *bitmapProgress) check() {
	if p.pixels%1024 == 0 && p.ctx.Err() != nil {
		panic(p)
	}
	p.pixels++
}

func (p *bitmapProgress) recoverCancellation(err *error) {
	if r := recover(); r != nil {
		if r != p {
			panic(r)
		}
		*err = p.ctx.Err()
	}
}

// Embedding the Image interface deliberately hides concrete pixel fast paths
// and Opaque, which could otherwise scan an entire image without a check.
type bitmapSource struct {
	image.Image
	progress *bitmapProgress
}

func (m bitmapSource) At(x, y int) color.Color {
	m.progress.check()
	return m.Image.At(x, y)
}

func (m bitmapSource) RGBA64At(x, y int) color.RGBA64 {
	m.progress.check()
	if src, ok := m.Image.(image.RGBA64Image); ok {
		return src.RGBA64At(x, y)
	}
	r, g, b, a := m.Image.At(x, y).RGBA()
	return color.RGBA64{uint16(r), uint16(g), uint16(b), uint16(a)}
}

type bitmapDestination struct {
	draw.Image
	progress *bitmapProgress
}

// Preserve palette indices, including duplicate colors and transparent entries.
type bitmapPalette struct {
	bitmapSource
	palette image.PalettedImage
}

func (m bitmapPalette) ColorIndexAt(x, y int) uint8 {
	m.progress.check()
	return m.palette.ColorIndexAt(x, y)
}

func (m bitmapDestination) Set(x, y int, c color.Color) {
	m.progress.check()
	m.Image.Set(x, y, c)
}

func (m bitmapDestination) SetRGBA64(x, y int, c color.RGBA64) {
	m.progress.check()
	if dst, ok := m.Image.(draw.RGBA64Image); ok {
		dst.SetRGBA64(x, y, c)
	} else {
		m.Image.Set(x, y, c)
	}
}

func (m bitmapDestination) RGBA64At(x, y int) color.RGBA64 {
	r, g, b, a := m.Image.At(x, y).RGBA()
	return color.RGBA64{uint16(r), uint16(g), uint16(b), uint16(a)}
}

type bitmapWriter struct {
	ctx context.Context
	io.Writer
}

func (w bitmapWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.Writer.Write(data)
}
