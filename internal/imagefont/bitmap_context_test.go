package imagefont

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"io"
	"testing"
	"time"

	"golang.org/x/image/draw"
)

func TestBitmapCancellationDuringPixels(t *testing.T) {
	for _, phase := range []string{"horizontal scale", "vertical scale", "uniform fill", "PNG rows", "PNG opacity"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			src := solid(image.Rect(7, 9, 519, 521), color.NRGBA{17, 82, 233, 128})
			progress := &cancelingBitmap{Image: src, cancel: cancel}
			var err error
			switch phase {
			case "horizontal scale":
				_, err = fit(ctx, progress, 511, 511)
			case "vertical scale":
				dst := &cancelingBitmapDestination{NRGBA: image.NewNRGBA(image.Rect(0, 0, 511, 511)), progress: progress}
				err = ScaleBitmap(ctx, dst, dst.Bounds(), src, src.Bounds())
			case "uniform fill":
				dst := &cancelingBitmapDestination{NRGBA: image.NewNRGBA(image.Rect(0, 0, 511, 511)), progress: progress}
				err = ScaleBitmap(ctx, dst, dst.Bounds(), image.NewUniform(color.NRGBA{17, 82, 233, 128}), src.Bounds())
			case "PNG rows":
				err = EncodePNG(ctx, &png.Encoder{}, io.Discard, progress)
			case "PNG opacity":
				for i := 3; i < len(src.Pix); i += 4 {
					src.Pix[i] = 255
				}
				err = EncodePNG(ctx, &png.Encoder{}, io.Discard, progress)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want cancellation", err)
			}
			if progress.pixels > 64+1024 {
				t.Fatalf("processed %d pixels after canceling at pixel 64", progress.pixels)
			}
		})
	}
}

type cancelingBitmap struct {
	image.Image
	cancel context.CancelFunc
	pixels int
}

func (m *cancelingBitmap) step() {
	m.pixels++
	if m.pixels == 64 {
		m.cancel()
	}
}

func (m *cancelingBitmap) At(x, y int) color.Color {
	m.step()
	return m.Image.At(x, y)
}

type cancelingBitmapDestination struct {
	*image.NRGBA
	progress *cancelingBitmap
}

func (m *cancelingBitmapDestination) Set(x, y int, c color.Color) {
	m.progress.step()
	m.NRGBA.Set(x, y, c)
}

func (m *cancelingBitmapDestination) SetRGBA64(x, y int, c color.RGBA64) {
	m.progress.step()
	m.NRGBA.SetRGBA64(x, y, c)
}

func TestBitmapScaleMatchesCatmullRom(t *testing.T) {
	for name, src := range bitmapSourceFormats() {
		t.Run(name, func(t *testing.T) {
			for _, size := range []image.Point{{31, 57}, {127, 93}, {63, 47}, {1, 1}, {2, 511}} {
				r := image.Rectangle{Min: image.Pt(3, 5), Max: size.Add(image.Pt(3, 5))}
				got, want := image.NewNRGBA(r), image.NewNRGBA(r)
				draw.CatmullRom.Scale(want, r, src, src.Bounds(), draw.Src, nil)
				if err := ScaleBitmap(context.Background(), got, r, src, src.Bounds()); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got.Pix, want.Pix) {
					for i := range got.Pix {
						if got.Pix[i] != want.Pix[i] {
							t.Fatalf("size %v byte %d = %d, want %d", size, i, got.Pix[i], want.Pix[i])
						}
					}
				}
			}
		})
	}
}

func TestBitmapScaleUniformMatchesDirectCopy(t *testing.T) {
	for _, alpha := range []uint8{0, 1, 64, 128, 255} {
		src := image.NewUniform(color.NRGBA{17, 82, 233, alpha})
		r := image.Rect(3, 5, 35, 29)
		got, want := image.NewNRGBA(r), image.NewNRGBA(r)
		dr, sr := image.Rect(5, 7, 37, 31), image.Rect(7, 9, 23, 19)
		draw.CatmullRom.Scale(want, dr, src, sr, draw.Src, nil)
		if err := ScaleBitmap(context.Background(), got, dr, src, sr); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("uniform alpha %d changed straight-alpha pixels", alpha)
		}
	}
}

func bitmapSourceFormats() map[string]image.Image {
	r := image.Rect(7, 9, 70, 56)
	nrgba := image.NewNRGBA(image.Rect(5, 6, 75, 60)).SubImage(r).(*image.NRGBA)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			nrgba.SetNRGBA(x, y, color.NRGBA{uint8(x*7 + y*11), uint8(x*13 + y*17), uint8(x*19 + y*23), [...]uint8{0, 1, 64, 127, 128, 254, 255}[(x+y)%7]})
		}
	}
	formats := map[string]image.Image{"NRGBA": nrgba, "RGBA": image.NewRGBA(r), "Gray": image.NewGray(r), "Gray16": image.NewGray16(r), "RGBA64": image.NewRGBA64(r), "NRGBA64": image.NewNRGBA64(r), "Alpha": image.NewAlpha(r), "generic": struct{ image.Image }{nrgba}}
	for _, src := range formats {
		if dst, ok := src.(stddraw.Image); ok && dst != nrgba {
			stddraw.Draw(dst, r, nrgba, r.Min, stddraw.Src)
		}
	}
	for name, ratio := range map[string]image.YCbCrSubsampleRatio{"YCbCr444": image.YCbCrSubsampleRatio444, "YCbCr420": image.YCbCrSubsampleRatio420, "YCbCr422": image.YCbCrSubsampleRatio422, "YCbCr440": image.YCbCrSubsampleRatio440} {
		src := image.NewYCbCr(r, ratio)
		for i := range src.Y {
			src.Y[i] = uint8(i*17 + i/63)
		}
		for i := range src.Cb {
			src.Cb[i], src.Cr[i] = uint8(i*19), uint8(i*23)
		}
		formats[name] = src
	}
	palette := image.NewPaletted(r, color.Palette{color.NRGBA{17, 31, 239, 255}, color.NRGBA{17, 31, 239, 255}, color.NRGBA{82, 79, 131, 128}, color.NRGBA{19, 83, 255, 0}})
	for i := range palette.Pix {
		palette.Pix[i] = uint8(i % 4)
	}
	formats["palette"] = palette
	return formats
}

func TestBitmapPNGMatchesEncoder(t *testing.T) {
	encoder := png.Encoder{BufferPool: &pngBufferPool{}}
	for name, src := range bitmapSourceFormats() {
		t.Run(name, func(t *testing.T) {
			var got, want bytes.Buffer
			if err := encoder.Encode(&want, src); err != nil {
				t.Fatal(err)
			}
			if err := EncodePNG(context.Background(), &encoder, &got, src); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want.Bytes()) {
				t.Fatal("PNG bytes differ from the existing encoder")
			}
		})
	}
}

func TestBitmapPNGLargeRowCancellationAndPoolReuse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Largest allowed strike area and width, generated without a 128 MiB fixture.
	// The PNG header has already been written when the pixel callback cancels.
	src := &cancelingBitmap{Image: constantBitmap{image.Rect(0, 0, 16384, 2048)}, cancel: cancel}
	encoder := png.Encoder{BufferPool: &pngBufferPool{}}
	start := time.Now()
	var encoded bytes.Buffer
	if err := EncodePNG(ctx, &encoder, &encoded, src); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
	if src.pixels > 64+1024 || encoded.Len() < 8 {
		t.Fatalf("pixels = %d, output bytes = %d; want bounded cancellation after the PNG header", src.pixels, encoded.Len())
	}
	encoded.Reset()
	want := solid(image.Rect(0, 0, 32, 16), color.NRGBA{91, 137, 243, 191})
	if err := EncodePNG(context.Background(), &encoder, &encoded, want); err != nil {
		t.Fatal(err)
	}
	var reference bytes.Buffer
	if err := png.Encode(&reference, want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Bytes(), reference.Bytes()) {
		t.Fatal("canceled encode contaminated the next pooled encoder result")
	}
}

type constantBitmap struct{ rect image.Rectangle }

func (m constantBitmap) Bounds() image.Rectangle { return m.rect }
func (m constantBitmap) ColorModel() color.Model { return color.NRGBAModel }
func (m constantBitmap) At(x, y int) color.Color { return color.NRGBA{19, 147, 225, 128} }

func TestBitmapCancellationBeforeAllocation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := panicBitmap{constantBitmap{image.Rect(0, 0, 1, 1)}}
	if img, err := fit(ctx, src, 16384, 2048); img != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("fit = %v, %v, want no allocation and cancellation", img, err)
	}
	if err := EncodePNG(ctx, &png.Encoder{}, io.Discard, src); !errors.Is(err, context.Canceled) {
		t.Fatalf("encode error = %v", err)
	}
}

func TestBitmapPropagatesUnrelatedPanics(t *testing.T) {
	for _, operation := range []string{"scale", "PNG image", "PNG writer"} {
		t.Run(operation, func(t *testing.T) {
			defer func() {
				if got := recover(); got != "unrelated bitmap panic" {
					t.Fatalf("panic = %v, want unrelated image/writer panic", got)
				}
			}()
			src := panicBitmap{constantBitmap{image.Rect(0, 0, 16, 16)}}
			switch operation {
			case "scale":
				_, _ = fit(context.Background(), src, 32, 32)
			case "PNG image":
				_ = EncodePNG(context.Background(), &png.Encoder{}, io.Discard, src)
			case "PNG writer":
				_ = EncodePNG(context.Background(), &png.Encoder{}, bitmapTestWriter(func([]byte) (int, error) { panic("unrelated bitmap panic") }), src.constantBitmap)
			}
		})
	}
}

type panicBitmap struct{ constantBitmap }

func (panicBitmap) At(x, y int) color.Color { panic("unrelated bitmap panic") }

type bitmapTestWriter func([]byte) (int, error)

func (w bitmapTestWriter) Write(data []byte) (int, error) { return w(data) }

func TestBitmapPNGWriterErrorsAndCancellation(t *testing.T) {
	for _, cancellation := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		writes := 0
		writeErr := errors.New("synthetic PNG output failure")
		writer := bitmapTestWriter(func(data []byte) (int, error) {
			writes++
			if cancellation {
				cancel()
				return len(data), nil
			}
			return 0, writeErr
		})
		err := EncodePNG(ctx, &png.Encoder{}, writer, constantBitmap{image.Rect(0, 0, 4096, 2048)})
		cancel()
		if cancellation {
			writeErr = context.Canceled
		}
		if !errors.Is(err, writeErr) || writes != 1 {
			t.Fatalf("cancel=%t: error = %v, writes = %d", cancellation, err, writes)
		}
	}
}
