package imagepreview

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderTextAspectAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		size          Size
		columns, rows int
	}{
		{"square", 80, 80, Size{}, 34, 17},
		{"portrait", 20, 80, Size{Columns: 120, Rows: 44}, 19, 37},
		{"landscape", 160, 40, Size{Columns: 120, Rows: 44}, 118, 15},
		{"large terminal", 80, 80, Size{Columns: 400, Rows: 200}, 88, 44},
		{"small terminal", 80, 80, Size{Columns: 12, Rows: 8}, 2, 1},
		{"one cell", 80, 80, Size{Columns: 1, Rows: 1}, 1, 1},
		{"pixel geometry", 80, 80, Size{100, 40, 1000, 400}, 33, 33},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, original := writeTestImage(t, "png", tc.width, tc.height)
			for _, colored := range []bool{true, false} {
				var output bytes.Buffer
				if err := RenderText(context.Background(), &output, path, tc.size, colored); err != nil {
					t.Fatal(err)
				}
				plain := output.String()
				if colored {
					plain = regexp.MustCompile(`\x1b\[(38;5;[0-9]+|48;5;[0-9]+|0)m`).ReplaceAllString(plain, "")
					if !strings.HasSuffix(output.String(), "\x1b[0m\r\n") {
						t.Fatal("color output must reset before the shell prompt")
					}
				} else {
					for _, ch := range plain {
						if !strings.ContainsRune("\r\n@%#*+=-:. ", ch) {
							t.Fatalf("monochrome preview contains non-ASCII-art character %q", ch)
						}
					}
				}
				if strings.ContainsRune(plain, '\x1b') {
					t.Fatal("preview contained an unexpected terminal control sequence")
				}
				lines := strings.Split(strings.TrimSuffix(plain, "\r\n"), "\r\n")
				if len(lines) != tc.rows {
					t.Fatalf("got %d rows, want %d", len(lines), tc.rows)
				}
				for _, line := range lines {
					if got := utf8.RuneCountInString(strings.TrimPrefix(line, "\r")); got != tc.columns {
						t.Fatalf("got %d columns, want %d", got, tc.columns)
					}
				}
				assertFileUnchanged(t, path, original)
			}
		})
	}
}

func TestRenderTextAntialiasing(t *testing.T) {
	// A one-pixel checkerboard must become neutral gray, not aliased black or
	// white patches. Sparse point sampling cannot reconstruct this correctly.
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for y := range 256 {
		for x := range 256 {
			v := uint8(255 * ((x + y) % 2))
			img.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "checkerboard.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RenderText(context.Background(), &output, path, Size{Columns: 20, Rows: 15}, true); err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`\x1b\[[34]8;5;([0-9]+)m`).FindAllStringSubmatch(output.String(), -1)
	if len(matches) == 0 {
		t.Fatal("preview has no colors")
	}
	for _, match := range matches {
		index, _ := strconv.Atoi(match[1])
		for _, channel := range paletteRGB(index) {
			if channel < 123 || channel > 133 {
				t.Fatalf("checkerboard aliased into palette color %d (%v)", index, paletteRGB(index))
			}
		}
	}
	assertFileUnchanged(t, path, encoded.Bytes())
}

func TestQuadrantCellImprovesEdgesAndPreservesHalfBlocks(t *testing.T) {
	black, white := textRGB{0, 0, 0}, textRGB{255, 255, 255}
	for _, pixels := range [][4]textRGB{
		{white, black, black, white}, // diagonal detail needs quadrant blocks
		{black, white, black, white}, // vertical edge needs horizontal resolution
		{black, black, white, white}, // retain existing horizontal half blocks
		{white, white, white, white}, // no error or texture on a flat region
	} {
		if got := quadrantError(pixels); got != 0 {
			t.Fatalf("exactly representable cell lost detail: %v, error %d", pixels, got)
		}
	}
	random := rand.New(rand.NewSource(42))
	for range 1000 {
		var pixels [4]textRGB
		for i := range pixels {
			pixels[i] = textRGB{random.Intn(256), random.Intn(256), random.Intn(256)}
		}
		halfError := 0
		for row := range 2 {
			a, b := pixels[row*2], pixels[row*2+1]
			index := paletteColor((a[0]+b[0]+1)/2, (a[1]+b[1]+1)/2, (a[2]+b[2]+1)/2)
			halfError += colorError(a, paletteRGB(index)) + colorError(b, paletteRGB(index))
		}
		if got := quadrantError(pixels); got > halfError {
			t.Fatalf("quadrant error %d exceeds half-block error %d for %v", got, halfError, pixels)
		}
	}
}

func quadrantError(pixels [4]textRGB) int {
	var expanded [64]textRGB
	for i := range expanded {
		expanded[i] = pixels[i%8/4+i/8/4*2]
	}
	return fittedCellError(expanded, false) / 16
}

func fittedCellError(pixels [64]textRGB, trueColor bool) int {
	glyph, foreground, background := fitTextCell(pixels, trueColor)
	var mask uint64
	for _, shape := range textShapes {
		if glyph == shape.glyph {
			mask = shape.mask
			break
		}
	}
	for density, shade := range []rune{'░', '▒', '▓'} {
		if glyph == shade {
			for channel := range foreground {
				background[channel] = ((density+1)*foreground[channel] + (3-density)*background[channel] + 2) / 4
			}
		}
	}
	error := 0
	for i, pixel := range pixels {
		c := background
		if mask&(1<<i) != 0 {
			c = foreground
		}
		error += colorError(pixel, c)
	}
	return error
}

func TestTextCellThinEdgesAndSmoothColors(t *testing.T) {
	for _, trueColor := range []bool{false, true} {
		for _, horizontal := range []bool{false, true} {
			for thickness := 1; thickness < 8; thickness++ {
				var pixels [64]textRGB
				for i := range pixels {
					foreground := i%8 < thickness
					if horizontal {
						foreground = i/8 >= 8-thickness
					}
					if foreground {
						pixels[i] = textRGB{255, 255, 255}
					}
				}
				glyph, _, _ := fitTextCell(pixels, trueColor)
				if strings.ContainsRune("░▒▓", glyph) || fittedCellError(pixels, trueColor) != 0 {
					t.Fatalf("lost %d/8 boundary, horizontal=%v, truecolor=%v, glyph=%c", thickness, horizontal, trueColor, glyph)
				}
			}
		}
	}
	var smooth [64]textRGB
	for i := range smooth {
		smooth[i] = textRGB{195, 155, 115} // midway between nearby cube entries
	}
	glyph, _, _ := fitTextCell(smooth, false)
	if !strings.ContainsRune("░▒▓", glyph) || fittedCellError(smooth, false) != 0 {
		t.Fatalf("smooth nonpalette color should use an accurate shade mixture, got %c", glyph)
	}
	for i := range smooth {
		smooth[i] = textRGB{193, 157, 121}
	}
	glyph, foreground, background := fitTextCell(smooth, true)
	if glyph != ' ' || foreground != smooth[0] || background != smooth[0] || fittedCellError(smooth, true) != 0 {
		t.Fatalf("truecolor introduced texture or quantization: %c, %v, %v", glyph, foreground, background)
	}
}

func TestRenderTextTrueColor(t *testing.T) {
	path, original := writeTestImage(t, "png", 48, 32)
	var output bytes.Buffer
	if err := RenderText(context.Background(), &output, path, Size{}, true, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\x1b[38;2;") || !strings.Contains(output.String(), "\x1b[48;2;") || strings.Contains(output.String(), ";5;") {
		t.Fatal("truecolor render did not use RGB foreground/background codes exclusively")
	}
	plain := regexp.MustCompile(`\x1b\[([34]8;2;[0-9]+;[0-9]+;[0-9]+|0)m`).ReplaceAllString(output.String(), "")
	if strings.ContainsRune(plain, '\x1b') {
		t.Fatal("truecolor render used unexpected control sequences")
	}
	assertFileUnchanged(t, path, original)
}

func TestRenderTextFormats(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "webp"} {
		t.Run(format, func(t *testing.T) {
			path, original := writeTestImage(t, format, 48, 32)
			var output bytes.Buffer
			if err := RenderText(context.Background(), &output, path, Size{}, true); err != nil || output.Len() == 0 {
				t.Fatalf("format did not render: %v", err)
			}
			assertFileUnchanged(t, path, original)
		})
	}
}

func TestRenderTextTransparencyAndPalette(t *testing.T) {
	transparent := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, transparent); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "transparent.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RenderText(context.Background(), &output, path, Size{}, true); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"255", "250"} {
		if !regexp.MustCompile(`\x1b\[[34]8;5;` + index + `m`).MatchString(output.String()) {
			t.Fatalf("transparent background lacks checkerboard color %s", index)
		}
	}
	transparent.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	r, g, b := textPixel(transparent, 0, 0)
	if r != 247 || g != 119 || b != 119 {
		t.Fatalf("incorrect alpha compositing: %d, %d, %d", r, g, b)
	}
	for _, tc := range []struct{ r, g, b, index int }{
		{0, 0, 0, 16}, {255, 255, 255, 231}, {255, 0, 0, 196}, {128, 128, 128, 244},
	} {
		if got := paletteColor(tc.r, tc.g, tc.b); got != tc.index {
			t.Fatalf("palette color %v = %d", tc, got)
		}
	}
	assertFileUnchanged(t, path, encoded.Bytes())
}

func TestRenderTextErrorsAndCancellation(t *testing.T) {
	path, original := writeTestImage(t, "png", 40, 40)
	for _, colored := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var output bytes.Buffer
		if err := RenderText(ctx, &output, path, Size{}, colored); !errors.Is(err, context.Canceled) || output.Len() != 0 {
			t.Fatalf("already-canceled render emitted output: %v", err)
		}
		ctx, cancel = context.WithCancel(context.Background())
		writer := &cancelWriter{cancel: cancel, after: 1}
		if err := RenderText(ctx, writer, path, Size{}, colored); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error lost: %v", err)
		}
		if colored && !strings.HasSuffix(writer.String(), "\x1b[0m\r\n") {
			t.Fatal("canceled preview did not restore terminal colors")
		}
		if !colored && strings.ContainsRune(writer.String(), '\x1b') {
			t.Fatal("canceled ASCII preview emitted an escape sequence")
		}
		if err := RenderText(context.Background(), shortWriter{}, path, Size{}, colored); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("short-write error lost: %v", err)
		}
		failure := errors.New("terminal disconnected")
		if err := RenderText(context.Background(), errorWriter{failure}, path, Size{}, colored); !errors.Is(err, failure) {
			t.Fatalf("write error lost: %v", err)
		}
	}
	bad := filepath.Join(t.TempDir(), "corrupt.png")
	if err := os.WriteFile(bad, []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, badPath := range []string{bad, t.TempDir(), filepath.Join(t.TempDir(), "missing.png")} {
		var output bytes.Buffer
		if err := RenderText(context.Background(), &output, badPath, Size{}, true); err == nil || output.Len() != 0 {
			t.Fatalf("preparation failure emitted output: %v", err)
		}
	}
	assertFileUnchanged(t, path, original)
}
