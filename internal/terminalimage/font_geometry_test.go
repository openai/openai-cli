package terminalimage

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestImageFontTileGeometry(t *testing.T) {
	for _, pointSize := range []int{16, 32} {
		// These bounds are Terminal's supported 0.5…1.5 spacing factors.
		// Every cell dimension, including odd custom sizes, must round-trip.
		for cellWidth := pointSize / 4; cellWidth <= 3*pointSize/4; cellWidth++ {
			for cellHeight := pointSize / 2; cellHeight <= 3*pointSize/2; cellHeight++ {
				for _, leftover := range []bool{false, true} {
					size := Size{Columns: 120, Rows: 60, PixelWidth: cellWidth * 120, PixelHeight: cellHeight * 60}
					if leftover {
						size.PixelWidth += cellWidth - 1
						size.PixelHeight += cellHeight - 1
					}
					width, height, err := imageFontTileGeometry(size, float64(pointSize))
					if err != nil || width != cellWidth*32/pointSize || height != cellHeight*32/pointSize {
						t.Fatalf("font %d, cell %dx%d, leftover=%v: got %dx%d, %v", pointSize, cellWidth, cellHeight, leftover, width, height, err)
					}
				}
			}
		}

		for _, size := range []Size{{}, {Columns: 80, Rows: 24}} {
			width, height, err := imageFontTileGeometry(size, float64(pointSize))
			if err != nil || width != 16 || height != 32 {
				t.Fatalf("unknown dimensions at %dpt: got %dx%d, %v", pointSize, width, height, err)
			}
		}
	}
}

func TestImageFontTileGeometryShippedProfiles(t *testing.T) {
	// Values come from Terminal 2.15's shipped Initial Settings, not user prefs.
	// Missing spacing values inherit 1.0; Homebrew and Pro disable antialiasing.
	profiles := []struct {
		name       string
		widthScale float64
		antialias  bool
	}{
		{"Basic", 1.004032258064516, true},
		{"Clear Dark", 1, true},
		{"Clear Light", 1, true},
		{"Grass", 1, true},
		{"Homebrew", 1, false},
		{"Man Page", 1.004032, true},
		{"Novel", 1, true},
		{"Ocean", 0.995968, true},
		{"Pro", 0.995968, false},
		{"Red Sands", 1.004032, true},
		{"Silver Aerogel", 1.004032, true},
		{"Solid Colors", 1.004032, true},
	}
	for _, profile := range profiles {
		for _, pointSize := range []int{16, 32} {
			t.Run(fmt.Sprintf("%s/%dpt", profile.name, pointSize), func(t *testing.T) {
				advance := float64(pointSize) / 2 * profile.widthScale
				if !profile.antialias {
					advance += 0.15
				}
				cellWidth := int(math.Round(advance))
				size := Size{Columns: 120, Rows: 60, PixelWidth: cellWidth * 120, PixelHeight: pointSize * 60}
				width, height, err := imageFontTileGeometry(size, float64(pointSize))
				if err != nil || width != 16 || height != 32 {
					t.Fatalf("expected standard strike tiles; got %dx%d, %v", width, height, err)
				}
			})
		}
	}
}

func TestImageFontTileGeometryRejectsUnreliableMeasurements(t *testing.T) {
	for _, tc := range []struct {
		name string
		size Size
	}{
		{"width missing", Size{Columns: 80, Rows: 24, PixelHeight: 384}},
		{"height missing", Size{Columns: 80, Rows: 24, PixelWidth: 640}},
		{"columns missing", Size{Rows: 24, PixelWidth: 640, PixelHeight: 384}},
		{"rows missing", Size{Columns: 80, PixelWidth: 640, PixelHeight: 384}},
		{"negative width", Size{Columns: 80, Rows: 24, PixelWidth: -640, PixelHeight: 384}},
		{"negative height", Size{Columns: 80, Rows: 24, PixelWidth: 640, PixelHeight: -384}},
		{"negative columns without pixels", Size{Columns: -80, Rows: 24}},
		{"negative rows without pixels", Size{Columns: 80, Rows: -24}},
		{"width too small", Size{Columns: 80, Rows: 24, PixelWidth: 80 * 3, PixelHeight: 384}},
		{"width too large", Size{Columns: 80, Rows: 24, PixelWidth: 80 * 13, PixelHeight: 384}},
		{"height too small", Size{Columns: 80, Rows: 24, PixelWidth: 640, PixelHeight: 24 * 7}},
		{"height too large", Size{Columns: 80, Rows: 24, PixelWidth: 640, PixelHeight: 24 * 25}},
		{"inconsistent width", Size{Columns: 80, Rows: 24, PixelWidth: 680, PixelHeight: 384}},
		{"inconsistent height", Size{Columns: 80, Rows: 24, PixelWidth: 640, PixelHeight: 402}},
		{"ambiguous narrow window", Size{Columns: 1, Rows: 60, PixelWidth: 8, PixelHeight: 960}},
		{"ambiguous short window", Size{Columns: 80, Rows: 1, PixelWidth: 640, PixelHeight: 16}},
		{"huge extents", Size{Columns: 80, Rows: 24, PixelWidth: math.MaxInt, PixelHeight: math.MaxInt}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			width, height, err := imageFontTileGeometry(tc.size, 16)
			if err == nil || width != 0 || height != 0 {
				t.Fatalf("unreliable dimensions produced %dx%d, %v", width, height, err)
			}
			if !strings.Contains(err.Error(), "enlarge") || !strings.Contains(err.Error(), "retry") {
				t.Fatalf("missing actionable recovery: %v", err)
			}
		})
	}
}

func TestImageFontTileGeometryRejectsUnsupportedFontSize(t *testing.T) {
	for _, size := range []float64{0, -16, 12, 16.5, 24, 64, math.NaN(), math.Inf(1), math.Inf(-1)} {
		width, height, err := imageFontTileGeometry(Size{}, size)
		if err == nil || width != 0 || height != 0 || !strings.Contains(err.Error(), "inline setup") {
			t.Fatalf("unsupported font size %v: got %dx%d, %v", size, width, height, err)
		}
	}
}
