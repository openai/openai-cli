package terminalimage

import (
	"errors"
)

// imageFontTileGeometry returns bitmap tile pixels at the font's 32ppem strike.
// Apple Terminal puts AppKit logical points, not Retina pixels, into winsize's
// pixel fields. Its unpadded viewport can include less than one leftover cell.
// Inferring an integer cell from both counts and points preserves custom spacing
// without changing the selected Terminal profile.
func imageFontTileGeometry(size Size, pointSize float64) (width, height int, err error) {
	if pointSize != 16 && pointSize != 32 {
		return 0, 0, errors.New("sharp image previews require a 16 or 32 point font; run 'openai images inline setup', then retry")
	}
	geometryError := errors.New("cannot determine image cell spacing; enlarge this Terminal window, then retry the preview")
	if size.Columns < 0 || size.Rows < 0 || size.PixelWidth < 0 || size.PixelHeight < 0 {
		return 0, 0, geometryError
	}
	// Older terminals and geometry-free callers cannot report viewport extents.
	// Retain the original strike geometry only when both dimensions are unknown.
	if size.PixelWidth == 0 && size.PixelHeight == 0 {
		return 16, 32, nil
	}
	if size.Columns == 0 || size.Rows == 0 || size.PixelWidth == 0 || size.PixelHeight == 0 {
		return 0, 0, geometryError
	}
	points := int(pointSize)
	cellWidth := uniqueImageFontCell(size.Columns, size.PixelWidth, points/4, 3*points/4)
	cellHeight := uniqueImageFontCell(size.Rows, size.PixelHeight, points/2, 3*points/2)
	if cellWidth == 0 || cellHeight == 0 {
		return 0, 0, geometryError
	}
	return cellWidth * 32 / points, cellHeight * 32 / points, nil
}

func uniqueImageFontCell(count, extent, minimum, maximum int) int {
	match := 0
	for cell := minimum; cell <= maximum; cell++ {
		if extent/cell != count {
			continue
		}
		if match != 0 {
			return 0 // A very small window can fit multiple possible cell sizes.
		}
		match = cell
	}
	return match
}
