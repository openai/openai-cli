package cmd

import (
	"errors"
	"math"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagepreview"
)

// imageFontPreservedGeometry leaves the user's point size and source font
// metrics unchanged. Terminal's viewport extents are logical points; matching
// them to the cell counts measures the user's character and line spacing.
func imageFontPreservedGeometry(size imagepreview.Size, pointSize int, source imagefontmac.SourceFont) (imagefont.PreserveOptions, error) {
	invalid := errors.New("cannot determine this font's image geometry without changing your settings; select a supported font size and retry")
	if pointSize < 1 || pointSize > 1024 || source.PostScript == "" {
		return imagefont.PreserveOptions{}, invalid
	}
	for _, metric := range []float64{source.Ascent, source.Descent, source.Leading, source.Advance, source.LineHeight} {
		if math.IsNaN(metric) || math.IsInf(metric, 0) || math.Abs(metric) > 8191 {
			return imagefont.PreserveOptions{}, invalid
		}
	}
	if source.Advance <= 0 || source.Ascent < 0 || source.Descent < 0 || source.LineHeight <= 0 {
		return imagefont.PreserveOptions{}, invalid
	}
	// Terminal rounds these computations through float32. Replicate that detail
	// before rounding, particularly at font sizes where metrics approach an
	// integer point boundary. Custom vertical spacing does not shift its baseline.
	ascent := float64(float32(source.Ascent))
	descent := float64(float32(source.Descent))
	leading := float64(float32(source.Leading))
	lineHeight := float64(float32(source.LineHeight))
	baseHeight := max(math.Ceil(ascent)+math.Ceil(descent)+math.Ceil(leading), math.Ceil(lineHeight))
	baseline := math.Floor(leading) - math.Floor(float64(float32(-source.Descent+0.5)))
	// Terminal special-cases the Monaco family. The encoder compensates its
	// private clone's metrics to retain this native height/baseline calculation.
	if source.PostScript == "Monaco" {
		baseHeight = math.Ceil(lineHeight)
		baseline = math.Floor(float64(float32(source.Leading + source.Descent)))
	}
	if baseHeight < 1 || baseHeight > 4096 || baseline < -8191 || baseline > 8191 {
		return imagefont.PreserveOptions{}, invalid
	}
	advance := math.Round(source.Advance*2048) / 2048
	width, height := max(1, int(math.Round(advance))), int(baseHeight)
	geometryError := errors.New("cannot determine image cell spacing; enlarge this Terminal window, then retry the preview")
	if size.Columns < 0 || size.Rows < 0 || size.PixelWidth < 0 || size.PixelHeight < 0 {
		return imagefont.PreserveOptions{}, geometryError
	}
	if size.PixelWidth != 0 || size.PixelHeight != 0 {
		if size.Columns == 0 || size.Rows == 0 || size.PixelWidth == 0 || size.PixelHeight == 0 {
			return imagefont.PreserveOptions{}, geometryError
		}
		// Terminal permits spacing factors from 0.5 through 1.5 and adds 0.15
		// points before rounding width when font antialiasing is disabled.
		minimumWidth := max(1, int(math.Round(advance*0.5)))
		maximumWidth := min(4096, max(1, int(math.Round(advance*1.5+0.15))))
		minimumHeight := max(1, int(math.Ceil(baseHeight*0.5)))
		maximumHeight := min(4096, max(1, int(math.Ceil(baseHeight*1.5))))
		width = uniqueImageFontCell(size.Columns, size.PixelWidth, minimumWidth, maximumWidth)
		height = uniqueImageFontCell(size.Rows, size.PixelHeight, minimumHeight, maximumHeight)
		if width == 0 || height == 0 {
			return imagefont.PreserveOptions{}, geometryError
		}
	}
	return imagefont.PreserveOptions{
		Tables: source.Tables, SourcePostScript: source.PostScript, SourceName: source.LookupName, Variations: source.Variations, FamilyClass: source.FamilyClass,
		PointSize: pointSize, CellWidth: width, CellHeight: height, Baseline: int(baseline), TextHeight: int(baseHeight),
	}, nil
}
