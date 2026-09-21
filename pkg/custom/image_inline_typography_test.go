package custom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagepreview"
)

func TestImageInlineTypographyKeepsSizesAndReusesImages(t *testing.T) {
	for _, pointSize := range []int{12, 13, 14, 18, 24, 32} {
		t.Run(fmt.Sprint(pointSize), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "gallery")
			bridge := newFakeImageFontBridge(t)
			bridge.fontSize = float64(pointSize)
			services := currentImageFontServices(t, bridge, "/dev/ttys001")
			var output bytes.Buffer
			if err := setupCurrentImageFont(t.Context(), &output, dir, "/dev/ttys001", services); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), fmt.Sprintf("Keeping font: %q at %d pt.", "GoMono", pointSize)) {
				t.Fatalf("setup did not identify captured typography: %q", output.String())
			}
			path := imageInlineFixture(t, "sample.png", color.NRGBA{R: 220, G: 80, B: 10, A: 255})
			var previous string
			for i := 0; i < 2; i++ {
				output.Reset()
				if err := displayImageFont(t.Context(), &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 80}, services); err != nil {
					t.Fatal(err)
				}
				if bridge.fontSize != float64(pointSize) {
					t.Fatal("point size changed")
				}
				if !strings.HasPrefix(output.String(), "\U000f0000") {
					t.Fatal("preview overwrote original-font private characters")
				}
				if i == 1 && previous != output.String() {
					t.Fatal("repeated image mapping changed")
				}
				previous = output.String()
			}
			if imageInlineState(t, dir).ImageCount != 1 {
				t.Fatal("repeat did not reuse cached image")
			}
		})
	}
}

func TestImageInlineTypographyUnsupportedFontLeavesSelection(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			bridge := newFakeImageFontBridge(t)
			bridge.currentFont = "preferred-original"
			bridge.fontSize = 13
			services := currentImageFontServices(t, bridge, "/dev/ttys001")
			services.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) {
				if legacy {
					return imagefontmac.SourceFont{}, imagefontmac.ErrLegacyFont
				}
				source := imageTypographyFixture(t, 13)
				delete(source.Tables, "glyf")
				source.Tables["CFF "] = []byte{1, 0, 4, 4}
				return source, nil
			}
			services.preserve = func(context.Context, string, string, string, imagefontmac.ProfileStatus) error {
				t.Fatal("unsupported font mutated selection")
				return nil
			}
			var output bytes.Buffer
			err := setupCurrentImageFont(t.Context(), &output, filepath.Join(t.TempDir(), "gallery"), "/dev/ttys001", services)
			if err == nil || output.Len() != 0 || bridge.currentFont != "preferred-original" || bridge.fontSize != 13 {
				t.Fatalf("unsupported source not kept: %v", err)
			}
			if legacy && !errors.Is(err, imagefontmac.ErrLegacyFont) {
				t.Fatal(err)
			}
		})
	}
}

func TestImageInlineTypographyConcurrentChangeEmitsNoImage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	bridge.fontSize = 13
	services := currentImageFontServices(t, bridge, "/dev/ttys001")
	var output bytes.Buffer
	if err := setupCurrentImageFont(t.Context(), &output, dir, "/dev/ttys001", services); err != nil {
		t.Fatal(err)
	}
	before := imageInlineState(t, dir)
	path := imageInlineFixture(t, "sample.png", color.NRGBA{R: 220, A: 255})
	services.preserve = func(context.Context, string, string, string, imagefontmac.ProfileStatus) error {
		return errors.New("settings changed")
	}
	output.Reset()
	err := displayImageFont(t.Context(), &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 80}, services)
	if err == nil || output.Len() != 0 || imageInlineState(t, dir) != before {
		t.Fatalf("concurrent change printed or committed image: %v", err)
	}
}
