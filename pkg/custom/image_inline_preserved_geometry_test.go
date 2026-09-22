package custom

import (
	"math"
	"strconv"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagepreview"
)

func TestImageFontPreservedGeometryOriginalSizes(t *testing.T) {
	// Menlo's exact metrics are fractional even at integer point sizes. These
	// values exercise the line rounding that caused seams in fixed-size strikes.
	for _, points := range []int{12, 13, 14, 15, 18, 24} {
		t.Run(strconv.Itoa(points), func(t *testing.T) {
			s := float64(points)
			source := imagefontmac.SourceFont{PostScript: "Menlo-Regular", Tables: map[string][]byte{"test": {1, 2, 3}}, Ascent: s * 1901 / 2048, Descent: s * 483 / 2048, Advance: s * 1233 / 2048, LineHeight: math.Ceil(s * 2384 / 2048)}
			baseHeight := int(max(math.Ceil(source.Ascent)+math.Ceil(source.Descent), source.LineHeight))
			for _, factor := range []float64{0.5, 0.8, 1, 1.3, 1.5} {
				width := max(1, int(math.Round(source.Advance*factor)))
				height := max(1, int(math.Ceil(float64(baseHeight)*factor)))
				size := imagepreview.Size{Columns: 100, Rows: 50, PixelWidth: 100*width + width - 1, PixelHeight: 50*height + height - 1}
				got, err := imageFontPreservedGeometry(size, points, source)
				if err != nil {
					t.Fatalf("spacing %g: %v", factor, err)
				}
				if got.PointSize != points || got.CellWidth != width || got.CellHeight != height || got.Baseline != int(-math.Floor(-source.Descent+0.5)) {
					t.Fatalf("unexpected geometry %+v", got)
				}
				if got.SourcePostScript != source.PostScript || &got.Tables["test"][0] != &source.Tables["test"][0] {
					t.Fatal("lost immutable source face")
				}
			}
		})
	}
}

func TestImageFontPreservedGeometryWithoutViewport(t *testing.T) {
	source := imagefontmac.SourceFont{PostScript: "CustomFace", Ascent: 11.7, Descent: 3.2, Leading: 1.1, Advance: 7.8, LineHeight: 18}
	got, err := imageFontPreservedGeometry(imagepreview.Size{}, 14, source)
	if err != nil {
		t.Fatal(err)
	}
	if got.PointSize != 14 || got.CellWidth != 8 || got.CellHeight != 18 || got.Baseline != 4 {
		t.Fatalf("unexpected nominal geometry %+v", got)
	}
}

func TestImageFontPreservedGeometryKeepsMonacoLayout(t *testing.T) {
	for _, tt := range []struct {
		point                                int
		ascent, descent, leading, lineHeight float64
		height, baseline                     int
	}{
		{12, 12, 4, 0, 16, 16, 4},
		{13, 13.1, 4.2, 0, 17, 17, 4},
		{18, 18.2, 5.7, 0.5, 25, 25, 6},
	} {
		source := imagefontmac.SourceFont{PostScript: "Monaco", Ascent: tt.ascent, Descent: tt.descent, Leading: tt.leading, Advance: 8, LineHeight: tt.lineHeight}
		got, err := imageFontPreservedGeometry(imagepreview.Size{}, tt.point, source)
		if err != nil {
			t.Fatal(err)
		}
		if got.CellHeight != tt.height || got.Baseline != tt.baseline {
			t.Fatalf("Monaco %dpt: %+v", tt.point, got)
		}
	}
}

func TestImageFontPreservedGeometryRejectsAmbiguousOrInvalid(t *testing.T) {
	source := imagefontmac.SourceFont{PostScript: "Menlo-Regular", Ascent: 12, Descent: 4, Advance: 8, LineHeight: 16}
	for _, size := range []imagepreview.Size{
		{Columns: 1, Rows: 1, PixelWidth: 12, PixelHeight: 24},
		{Columns: 100, Rows: 50, PixelWidth: 800},
		{Columns: 100, Rows: 50, PixelWidth: 1, PixelHeight: 1},
		{Columns: -1},
	} {
		if _, err := imageFontPreservedGeometry(size, 16, source); err == nil {
			t.Fatalf("accepted invalid geometry %+v", size)
		}
	}
	for _, points := range []int{0, -1, 1025} {
		if _, err := imageFontPreservedGeometry(imagepreview.Size{}, points, source); err == nil {
			t.Fatalf("accepted point size %d", points)
		}
	}
	for _, invalid := range []float64{0, -1, math.NaN(), math.Inf(1), 1e30} {
		bad := source
		bad.Advance = invalid
		if _, err := imageFontPreservedGeometry(imagepreview.Size{}, 16, bad); err == nil {
			t.Fatalf("accepted advance %g", invalid)
		}
	}
}

func TestImageFontPreservedGeometryKeepsBundledLookupName(t *testing.T) {
	source := imagefontmac.SourceFont{PostScript: "SFMono-RegularItalic", LookupName: "SF Mono Regular Italic", Tables: map[string][]byte{"test": {1}}, Ascent: 12, Descent: 4, Advance: 8, LineHeight: 16}
	got, err := imageFontPreservedGeometry(imagepreview.Size{}, 13, source)
	if err != nil || got.SourcePostScript != source.PostScript || got.SourceName != source.LookupName {
		t.Fatalf("bundled lookup identity lost: %+v %v", got, err)
	}
}
