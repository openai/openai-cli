package imagepreview

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRenderITerm2(t *testing.T) {
	path, original := writeTestImage(t, "png", 48, 32)
	var output bytes.Buffer
	if err := Render(context.Background(), &output, path, ITerm2, Size{Columns: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	raw := output.String()
	header, payload, ok := strings.Cut(raw, ":")
	if !ok || !strings.HasPrefix(header, "\r\x1b]1337;File=") || !strings.Contains(header, ";width=78;height=12;preserveAspectRatio=1") {
		t.Fatalf("unexpected iTerm2 header: %q", header)
	}
	if !strings.Contains(header, "inline=1;") || strings.Contains(header, "name=") {
		t.Fatalf("preview must use inline display with no untrusted name: %q", header)
	}
	if !strings.HasSuffix(payload, "\x1b\\\r\n") {
		t.Fatal("preview must close the control string and leave a new line")
	}
	decoded := decodePNG(t, strings.TrimSuffix(payload, "\x1b\\\r\n"))
	if decoded.Bounds().Dx() != 48 || decoded.Bounds().Dy() != 32 {
		t.Fatalf("small image dimensions changed: %v", decoded.Bounds())
	}
	if color.NRGBAModel.Convert(decoded.At(17, 8)) != testPixel(17, 8) {
		t.Fatalf("small PNG pixel changed: %v", decoded.At(17, 8))
	}
	assertFileUnchanged(t, path, original)
}

func TestRenderKittyChunking(t *testing.T) {
	path, original := writeTestImage(t, "png", 400, 260)
	var output bytes.Buffer
	if err := Render(context.Background(), &output, path, Kitty, Size{Columns: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	headers, payloads := kittyChunks(t, output.String())
	if len(payloads) < 2 {
		t.Fatal("fixture must exercise multiple graphics chunks")
	}
	first := headers[0]
	if id, err := strconv.ParseUint(first["i"], 10, 32); err != nil || id == 0 {
		t.Fatalf("preview needs a nonzero ID for targeted cancellation: %v", first)
	}
	for _, expected := range []string{"a=T", "t=d", "f=100", "q=2"} {
		key, value, _ := strings.Cut(expected, "=")
		if first[key] != value {
			t.Fatalf("first chunk lacks %s: %v", expected, first)
		}
	}
	for i, payload := range payloads {
		if len(payload) > 4096 || len(payload)%4 != 0 {
			t.Fatalf("chunk %d has invalid base64 length %d", i, len(payload))
		}
		more := "1"
		if i == len(payloads)-1 {
			more = "0"
		}
		if headers[i]["m"] != more || headers[i]["q"] != "2" {
			t.Fatalf("invalid continuation/quiet flags: %v", headers[i])
		}
		if i > 0 && len(headers[i]) != 2 {
			t.Fatalf("continuation repeats metadata: %v", headers[i])
		}
	}
	decoded := decodePNG(t, strings.Join(payloads, ""))
	if decoded.Bounds().Dx() != 400 || decoded.Bounds().Dy() != 260 {
		t.Fatalf("wrong transmitted image dimensions: %v", decoded.Bounds())
	}
	assertFileUnchanged(t, path, original)
}

func TestRenderITerm2HighEntropyThumbnail(t *testing.T) {
	// Incompressible alpha data exercises the worst-case OSC payload size.
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	_, _ = rand.New(rand.NewSource(23)).Read(img.Pix)
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "alpha.png")
	if err := os.WriteFile(path, original.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Render(context.Background(), &output, path, ITerm2, Size{Columns: 160, Rows: 60}); err != nil {
		t.Fatal(err)
	}
	if output.Len() >= 1<<20 {
		t.Fatalf("OSC too large for older receivers: %d", output.Len())
	}
	_, encoded, _ := strings.Cut(output.String(), ":")
	decoded := decodePNG(t, strings.TrimSuffix(encoded, "\x1b\\\r\n"))
	if decoded.Bounds() != image.Rect(0, 0, 400, 400) {
		t.Fatalf("thumbnail bounds = %v", decoded.Bounds())
	}
	if _, _, _, alpha := decoded.At(200, 200).RGBA(); alpha == 65535 {
		t.Fatal("preview unexpectedly flattened transparency")
	}
	assertFileUnchanged(t, path, original.Bytes())
}

func TestRenderITerm2Unscaled16BitPNG(t *testing.T) {
	img := image.NewNRGBA64(image.Rect(0, 0, 400, 400))
	_, _ = rand.New(rand.NewSource(47)).Read(img.Pix)
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	if original.Len() < 1<<20 {
		t.Fatal("16-bit fixture must exceed an old OSC receiver's size limit")
	}
	path := filepath.Join(t.TempDir(), "16bit.png")
	if err := os.WriteFile(path, original.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Render(context.Background(), &output, path, ITerm2, Size{Columns: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	if output.Len() >= 1<<20 {
		t.Fatalf("16-bit source exceeded OSC limit: %d", output.Len())
	}
	_, encoded, _ := strings.Cut(output.String(), ":")
	payload, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(encoded, "\x1b\\\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if payload[24] != 8 {
		t.Fatalf("PNG thumbnail depth = %d, want 8", payload[24])
	}
	decoded, err := png.Decode(bytes.NewReader(payload))
	if err != nil || decoded.Bounds() != image.Rect(0, 0, 400, 400) {
		t.Fatalf("invalid unscaled thumbnail: %v", err)
	}
	assertFileUnchanged(t, path, original.Bytes())
}

func TestRenderFormatsAndThumbnail(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "webp"} {
		for _, protocol := range []Protocol{ITerm2, Kitty} {
			t.Run(format+"/"+string(protocol), func(t *testing.T) {
				path, original := writeTestImage(t, format, 1600, 800)
				var output bytes.Buffer
				if err := Render(context.Background(), &output, path, protocol, Size{Columns: 100, Rows: 40}); err != nil {
					t.Fatal(err)
				}
				var payload string
				if protocol == Kitty {
					_, chunks := kittyChunks(t, output.String())
					payload = strings.Join(chunks, "")
				} else {
					_, payload, _ = strings.Cut(output.String(), ":")
					payload = strings.TrimSuffix(payload, "\x1b\\\r\n")
				}
				decoded := decodePNG(t, payload)
				wantWidth, wantHeight := 1024, 512
				if protocol == ITerm2 {
					wantWidth, wantHeight = 400, 200
				}
				if format == "webp" {
					wantWidth, wantHeight = 1, 1
				}
				if decoded.Bounds().Dx() != wantWidth || decoded.Bounds().Dy() != wantHeight {
					t.Fatalf("preview dimensions = %v, want %dx%d", decoded.Bounds(), wantWidth, wantHeight)
				}
				assertFileUnchanged(t, path, original)
			})
		}
	}
}

func TestRenderKittyAspectAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		size          Size
		axis          string
		bound         int
	}{
		{"portrait", 20, 80, Size{100, 40, 800, 640}, "r", 20},
		{"landscape", 160, 20, Size{100, 40, 800, 640}, "c", 80},
		{"small terminal", 20, 80, Size{20, 10, 160, 160}, "r", 5},
		{"unknown size", 160, 20, Size{}, "c", 78},
		{"tiny terminal", 20, 80, Size{Columns: 1, Rows: 1}, "c", 1},
		{"narrow cells", 1536, 1024, Size{70, 40, 490, 680}, "c", 68},
		{"wide cells", 1536, 1024, Size{70, 40, 1400, 400}, "r", 20},
		{"unknown pixels portrait", 20, 80, Size{Columns: 100, Rows: 40}, "c", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := writeTestImage(t, "png", tc.width, tc.height)
			var output bytes.Buffer
			if err := Render(context.Background(), &output, path, Kitty, tc.size); err != nil {
				t.Fatal(err)
			}
			headers, _ := kittyChunks(t, output.String())
			if headers[0][tc.axis] != strconv.Itoa(tc.bound) {
				t.Fatalf("bounding axis = %v, want %s=%d", headers[0], tc.axis, tc.bound)
			}
			if headers[0]["c"] != "" && headers[0]["r"] != "" {
				t.Fatal("specifying both axes stretches images in Kitty")
			}
			if tc.size.PixelWidth > 0 && tc.size.PixelHeight > 0 {
				cellWidth := float64(tc.size.PixelWidth) / float64(tc.size.Columns)
				cellHeight := float64(tc.size.PixelHeight) / float64(tc.size.Rows)
				_, payloads := kittyChunks(t, output.String())
				bounds := decodePNG(t, strings.Join(payloads, "")).Bounds()
				aspect := float64(bounds.Dx()) / float64(bounds.Dy())
				displayWidth, displayHeight := float64(tc.bound)*cellWidth, float64(tc.bound)*cellWidth/aspect
				if tc.axis == "r" {
					displayHeight = float64(tc.bound) * cellHeight
					displayWidth = displayHeight * aspect
				}
				maxColumns, maxRows := previewSize(tc.size.Columns, tc.size.Rows)
				if displayWidth > float64(maxColumns)*cellWidth || displayHeight > float64(maxRows)*cellHeight {
					t.Fatalf("preview clips: %.2fx%.2f pixels in %dx%d cells", displayWidth, displayHeight, maxColumns, maxRows)
				}
			}
		})
	}
}

func TestRenderPreviewBudgetPreservesSavedImage(t *testing.T) {
	_, small := writeTestImage(t, "png", 1, 1)
	for _, size := range []uint32{65536, 1 << 28} {
		// A valid huge IHDR with tiny backing data must be rejected by the
		// preview-only allocation policy before attempting a pixel decode.
		original := bytes.Clone(small)
		binary.BigEndian.PutUint32(original[16:20], size)
		binary.BigEndian.PutUint32(original[20:24], size)
		binary.BigEndian.PutUint32(original[29:33], crc32.ChecksumIEEE(original[12:29]))
		path := filepath.Join(t.TempDir(), "huge.png")
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		for _, protocol := range []Protocol{ITerm2, Kitty} {
			var output bytes.Buffer
			err := Render(context.Background(), &output, path, protocol, Size{Columns: 80, Rows: 24})
			if err == nil || !strings.Contains(err.Error(), "preview pixel budget") {
				t.Fatalf("%d-pixel-wide image should skip optional preview before allocation: %v", size, err)
			}
			if output.Len() != 0 {
				t.Fatal("skipped preview emitted terminal control bytes")
			}
			assertFileUnchanged(t, path, original)
		}
	}
}

func TestRenderPreviewDimensionBudgetPreservesSavedImage(t *testing.T) {
	_, originalPixel := writeTestImage(t, "png", 1, 1)
	for _, dimensions := range [][2]uint32{
		{maxPreviewDimension + 1, 1}, {1, maxPreviewDimension + 1},
		{maxPreviewPixels, 1}, {1, maxPreviewPixels},
	} {
		// These thin images pass the total-pixel budget. Their valid IHDR must
		// be rejected before decoding or allocating a kernel's working buffers.
		original := bytes.Clone(originalPixel)
		binary.BigEndian.PutUint32(original[16:20], dimensions[0])
		binary.BigEndian.PutUint32(original[20:24], dimensions[1])
		binary.BigEndian.PutUint32(original[29:33], crc32.ChecksumIEEE(original[12:29]))
		path := filepath.Join(t.TempDir(), "thin.png")
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"iterm2", "kitty", "color text", "ASCII"} {
			var output bytes.Buffer
			var err error
			if mode == "iterm2" || mode == "kitty" {
				err = Render(t.Context(), &output, path, Protocol(mode), Size{})
			} else {
				err = RenderText(t.Context(), &output, path, Size{}, mode == "color text")
			}
			if err == nil || !strings.Contains(err.Error(), "preview dimension budget") {
				t.Fatalf("%s preview of %dx%d image did not skip before allocation: %v", mode, dimensions[0], dimensions[1], err)
			}
			if output.Len() != 0 {
				t.Fatal("skipped preview emitted terminal output")
			}
			assertFileUnchanged(t, path, original)
		}
	}
	// The boundary itself remains usable for legitimate panoramic images.
	for _, dimensions := range [][2]int{{maxPreviewDimension, 1}, {1, maxPreviewDimension}} {
		path, original := writeTestImage(t, "png", dimensions[0], dimensions[1])
		var output bytes.Buffer
		if err := RenderText(t.Context(), &output, path, Size{}, true); err != nil {
			t.Fatalf("dimension at the preview boundary was rejected: %v", err)
		}
		if output.Len() == 0 {
			t.Fatal("boundary image produced no preview")
		}
		assertFileUnchanged(t, path, original)
	}
}

func TestTerminalSizeForNonTerminal(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "regular-file-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if size := TerminalSize(file.Fd()); size != (Size{}) {
		t.Fatalf("regular file has a terminal size: %+v", size)
	}
}

func TestRenderPreparationFailureWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-image.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\ncorrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []Protocol{ITerm2, Kitty, "unknown"} {
		var output bytes.Buffer
		if err := Render(context.Background(), &output, path, protocol, Size{Columns: 80, Rows: 24}); err == nil {
			t.Fatal("expected error")
		}
		if output.Len() != 0 {
			t.Fatalf("preparation failure emitted terminal output: %q", output.String())
		}
	}
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing.png")} {
		var output bytes.Buffer
		if err := Render(context.Background(), &output, path, Kitty, Size{Columns: 80, Rows: 24}); err == nil || output.Len() != 0 {
			t.Fatalf("invalid input should fail before output: %v %q", err, output.String())
		}
	}
}

func TestRenderCancellationAndWriterErrors(t *testing.T) {
	path, original := writeTestImage(t, "png", 400, 260)
	for _, protocol := range []Protocol{ITerm2, Kitty} {
		t.Run(string(protocol), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var output bytes.Buffer
			if err := Render(ctx, &output, path, protocol, Size{Columns: 80, Rows: 24}); !errors.Is(err, context.Canceled) || output.Len() != 0 {
				t.Fatalf("already-canceled render: %v, %q", err, output.String())
			}
			ctx, cancel = context.WithCancel(context.Background())
			defer cancel()
			afterWrites := 1 // iTerm2: leave an OSC header awaiting its payload.
			if protocol == Kitty {
				afterWrites = 2 // Kitty: CR followed by the first complete chunk.
			}
			writer := &cancelWriter{cancel: cancel, after: afterWrites}
			if err := Render(ctx, writer, path, protocol, Size{Columns: 80, Rows: 24}); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation during terminal write: %v", err)
			}
			if !strings.Contains(writer.String(), "\x18\x1b\\") || !strings.HasSuffix(writer.String(), "\r\n") {
				t.Fatal("cancellation must terminate a partially written control sequence")
			}
			if protocol == Kitty && !strings.Contains(writer.String(), "\x1b_Ga=d,d=I,i=") {
				t.Fatal("Kitty cancellation must abort its own incomplete upload")
			}
			if protocol == Kitty && !strings.Contains(writer.String(), "q=2,m=1;") {
				t.Fatal("cancellation fixture must stop a real multi-chunk upload")
			}
			if err := Render(context.Background(), shortWriter{}, path, protocol, Size{Columns: 80, Rows: 24}); !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("short writer result: %v", err)
			}
			failure := errors.New("terminal write failed")
			if err := Render(context.Background(), errorWriter{failure}, path, protocol, Size{Columns: 80, Rows: 24}); !errors.Is(err, failure) {
				t.Fatalf("writer error not preserved: %v", err)
			}
			assertFileUnchanged(t, path, original)
		})
	}
}

func kittyChunks(t *testing.T, raw string) ([]map[string]string, []string) {
	t.Helper()
	if !strings.HasPrefix(raw, "\r\x1b_G") || !strings.HasSuffix(raw, "\x1b\\\r\n") {
		t.Fatal("Kitty preview must start at column one and end below the image")
	}
	raw = strings.TrimSuffix(strings.TrimPrefix(raw, "\r"), "\r\n")
	var headers []map[string]string
	var payloads []string
	for raw != "" {
		if !strings.HasPrefix(raw, "\x1b_G") {
			t.Fatalf("invalid Kitty chunk prefix: %.20q", raw)
		}
		chunk, rest, ok := strings.Cut(strings.TrimPrefix(raw, "\x1b_G"), "\x1b\\")
		if !ok {
			t.Fatal("unterminated Kitty chunk")
		}
		header, payload, ok := strings.Cut(chunk, ";")
		if !ok {
			t.Fatal("missing Kitty payload separator")
		}
		fields := map[string]string{}
		for _, field := range strings.Split(header, ",") {
			key, value, ok := strings.Cut(field, "=")
			if !ok || fields[key] != "" {
				t.Fatalf("invalid/duplicate Kitty metadata %q", field)
			}
			fields[key] = value
		}
		headers = append(headers, fields)
		payloads = append(payloads, payload)
		raw = rest
	}
	return headers, payloads
}

func decodePNG(t *testing.T, encoded string) image.Image {
	t.Helper()
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("terminal payload is not a complete PNG: %v", err)
	}
	return decoded
}

func testPixel(x, y int) color.NRGBA {
	return color.NRGBA{R: byte(x*71 + y*13), G: byte(x*23 + y*97), B: byte(x ^ y), A: 255}
}

func writeTestImage(t *testing.T, format string, width, height int) (string, []byte) {
	t.Helper()
	var data bytes.Buffer
	if format == "webp" {
		// A synthetic, single-pixel WebP. The standard library has no encoder.
		pixel, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
		if err != nil {
			t.Fatal(err)
		}
		data.Write(pixel)
	} else {
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				img.SetNRGBA(x, y, testPixel(x, y))
			}
		}
		var err error
		if format == "jpeg" {
			err = jpeg.Encode(&data, img, &jpeg.Options{Quality: 85})
		} else {
			err = png.Encode(&data, img)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "preview."+format)
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path, data.Bytes()
}

func assertFileUnchanged(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatalf("preview changed or removed the saved image: %v", err)
	}
}

type cancelWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
	after  int
	writes int
}

func (w *cancelWriter) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	w.writes++
	if w.writes >= w.after {
		w.cancel()
	}
	return n, err
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }
