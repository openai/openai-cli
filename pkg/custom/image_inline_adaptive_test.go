package custom

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/openai/openai-cli/internal/imagepreview"
)

func TestImageInlineAdaptivePreviewPreservesImagesAcrossSpacingChanges(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	gallery, err := prepareImageFontGallery(ctx, dir, bridge.services())
	if err != nil {
		t.Fatal(err)
	}
	initial := gallery.State()
	if err := gallery.Close(); err != nil {
		t.Fatal(err)
	}
	bridge.currentFont = initial.PostScript
	source := imageInlineFixture(t, "saved-image.png", color.NRGBA{R: 255, G: 80, B: 25, A: 255})
	original := adaptiveImageFileBytes(t, source)
	initialFont := adaptiveImageFileBytes(t, initial.FontPath)
	var first imagegallery.State
	var firstText string
	var baseFont []byte
	var firstVariant string

	for _, tc := range []struct {
		name                  string
		pointSize             float64
		size                  imagepreview.Size
		tileWidth, tileHeight int
	}{
		{"16pt custom", 16, imagepreview.Size{Columns: 80, Rows: 40, PixelWidth: 809, PixelHeight: 860}, 20, 42},
		{"32pt custom", 32, imagepreview.Size{Columns: 80, Rows: 40, PixelWidth: 1538, PixelHeight: 1516}, 19, 37},
		{"standard measured", 16, imagepreview.Size{Columns: 80, Rows: 40, PixelWidth: 640, PixelHeight: 640}, 16, 32},
		{"repeat 16pt custom", 16, imagepreview.Size{Columns: 80, Rows: 40, PixelWidth: 800, PixelHeight: 840}, 20, 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge.fontSize = tc.pointSize
			bridge.calls = nil
			before := adaptiveImageFileBytes(t, filepath.Join(dir, "state.json"))
			var output bytes.Buffer
			bridge.onActivate = func(string) {
				if output.Len() != 0 || !bytes.Equal(before, adaptiveImageFileBytes(t, filepath.Join(dir, "state.json"))) {
					t.Fatal("preview published glyphs or metadata before activating its matching font")
				}
			}
			if err := displayImageFont(ctx, &output, dir, source, "/dev/ttys001", tc.size, bridge.services()); err != nil {
				t.Fatal(err)
			}
			state := imageInlineState(t, dir)
			if state.ImageCount != 1 || !containsImageGlyphs(output.String()) {
				t.Fatalf("preview did not publish one image: state=%+v glyphs=%q", state, output.String())
			}
			if len(bridge.calls) < 4 || bridge.calls[0] != "register" || bridge.calls[1] != "inspect" || bridge.calls[len(bridge.calls)-2] != "activate" || bridge.calls[len(bridge.calls)-1] != "inspect" {
				t.Fatalf("measured preview skipped activation or its final inspection: %v", bridge.calls)
			}
			for _, call := range bridge.calls {
				if call == "check" || call == "profile" || call == "open" {
					t.Fatalf("measured preview used an unmeasured check or changed profiles: %v", bridge.calls)
				}
			}
			gallery, err := imagegallery.Open(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer gallery.Close()
			revision, err := gallery.Prepare(ctx, source, 32)
			if err != nil {
				t.Fatal(err)
			}
			font, err := gallery.FontForGeometry(ctx, revision, tc.tileWidth, tc.tileHeight)
			if err != nil {
				t.Fatal(err)
			}
			if !revision.Existing || !font.Existing || font.PostScript != bridge.currentFont || !bridge.registered[font.FontPath] {
				t.Fatalf("wrong geometry font activated: expected=%+v active=%q registered=%v", font, bridge.currentFont, bridge.registered[font.FontPath])
			}
			if state.FontPath != revision.FontPath || state.PostScript != revision.PostScript || output.String() != revision.Text {
				t.Fatal("geometry adaptation replaced the base revision or its glyph mapping")
			}
			standard := tc.tileWidth == 16 && tc.tileHeight == 32
			if (font.FontPath == state.FontPath) != standard {
				t.Fatal("standard geometry must pass through; custom geometry must keep a separate font")
			}
			if firstText == "" {
				first, firstText = state, output.String()
				baseFont = adaptiveImageFileBytes(t, state.FontPath)
				firstVariant = font.PostScript
			} else if state != first || output.String() != firstText || !bytes.Equal(baseFont, adaptiveImageFileBytes(t, state.FontPath)) {
				t.Fatal("changing spacing changed image identity, glyphs, or the immutable base font")
			}
			if strings.HasPrefix(tc.name, "repeat") && font.PostScript != firstVariant {
				t.Fatal("returning to earlier spacing did not reuse its immutable font")
			}
			if !bytes.Equal(original, adaptiveImageFileBytes(t, source)) || !bytes.Equal(initialFont, adaptiveImageFileBytes(t, initial.FontPath)) {
				t.Fatal("preview changed the saved original or an earlier font revision")
			}
		})
	}
}

func TestImageInlineAdaptivePreviewRejectsUnreliableGeometryBeforePreparing(t *testing.T) {
	failure := errors.New("synthetic inspection denied")
	for _, tc := range []struct {
		name       string
		size       imagepreview.Size
		inspectErr error
	}{
		{"inspection failed", imagepreview.Size{Columns: 80, Rows: 40, PixelWidth: 640, PixelHeight: 640}, failure},
		{"inconsistent dimensions", imagepreview.Size{Columns: 80, Rows: 40, PixelWidth: 680, PixelHeight: 640}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "gallery")
			bridge := newFakeImageFontBridge(t)
			gallery, err := prepareImageFontGallery(ctx, dir, bridge.services())
			if err != nil {
				t.Fatal(err)
			}
			before := gallery.State()
			if err := gallery.Close(); err != nil {
				t.Fatal(err)
			}
			source := imageInlineFixture(t, "saved-image.png", color.NRGBA{R: 25, G: 80, B: 255, A: 255})
			original := adaptiveImageFileBytes(t, source)
			filesBefore := adaptiveImageGalleryFiles(t, dir)
			bridge.calls, bridge.inspectErr = nil, tc.inspectErr
			var output bytes.Buffer
			err = displayImageFont(ctx, &output, dir, source, "/dev/ttys001", tc.size, bridge.services())
			if err == nil || tc.inspectErr != nil && !errors.Is(err, tc.inspectErr) {
				t.Fatalf("measurement error lost: %v", err)
			}
			if output.Len() != 0 || !reflect.DeepEqual(bridge.calls, []string{"register", "inspect"}) {
				t.Fatalf("failed measurement reached preparation or activation: calls=%v output=%q", bridge.calls, output.String())
			}
			if imageInlineState(t, dir) != before || !reflect.DeepEqual(filesBefore, adaptiveImageGalleryFiles(t, dir)) {
				t.Fatal("failed measurement prepared an image, font, or metadata")
			}
			if !bytes.Equal(original, adaptiveImageFileBytes(t, source)) {
				t.Fatal("failed preview changed the saved original")
			}
		})
	}
}

func TestImageInlineAdaptivePreviewRechecksAfterActivationBeforePublishing(t *testing.T) {
	inspectionFailure := errors.New("synthetic final inspection failure")
	for _, change := range []string{"font size", "inspection failure"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "gallery")
			bridge := newFakeImageFontBridge(t)
			gallery, err := prepareImageFontGallery(ctx, dir, bridge.services())
			if err != nil {
				t.Fatal(err)
			}
			before := gallery.State()
			if err := gallery.Close(); err != nil {
				t.Fatal(err)
			}
			stateBytes := adaptiveImageFileBytes(t, filepath.Join(dir, "state.json"))
			bridge.currentFont = before.PostScript
			source := imageInlineFixture(t, "saved-image.png", color.NRGBA{R: 255, G: 160, B: 20, A: 255})
			original := adaptiveImageFileBytes(t, source)
			bridge.calls = nil
			bridge.onActivate = func(string) {
				if change == "font size" {
					// Simulate an Inspector change while native font work runs.
					// The viewport still reports the previously measured 16pt grid.
					bridge.fontSize = 32
				} else {
					bridge.inspectErr = inspectionFailure
				}
			}
			var output bytes.Buffer
			size := imagepreview.Size{Columns: 80, Rows: 40, PixelWidth: 640, PixelHeight: 640}
			err = displayImageFont(ctx, &output, dir, source, "/dev/ttys001", size, bridge.services())
			if err == nil {
				t.Fatal("preview accepted stale settings after native activation")
			}
			if change == "inspection failure" && !errors.Is(err, inspectionFailure) {
				t.Fatalf("final inspection error was lost: %v", err)
			}
			if change == "font size" && (!strings.Contains(err.Error(), "changed") || !strings.Contains(err.Error(), "retry")) {
				t.Fatalf("font change omitted recovery guidance: %v", err)
			}
			if output.Len() != 0 || !reflect.DeepEqual(bridge.calls, []string{"register", "inspect", "register", "activate", "inspect"}) {
				t.Fatalf("stale preview printed glyphs or missed final inspection: calls=%v output=%q", bridge.calls, output.String())
			}
			if after := imageInlineState(t, dir); after != before || after.ImageCount != 0 || !bytes.Equal(stateBytes, adaptiveImageFileBytes(t, filepath.Join(dir, "state.json"))) {
				t.Fatal("stale preview committed its prepared image or glyph allocation")
			}
			if !bytes.Equal(original, adaptiveImageFileBytes(t, source)) {
				t.Fatal("stale preview changed the saved original")
			}
		})
	}
}

func adaptiveImageFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func adaptiveImageGalleryFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files[path] = string(adaptiveImageFileBytes(t, path))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}
