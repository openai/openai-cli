package terminalimage

// CellSize reads the text cell dimensions from existing window metadata without
// sending terminal queries or consuming input. Only their ratio is significant.
// When dimensions are missing or ambiguous, use the conventional 1-by-2 estimate.
func CellSize(fd uintptr) (width, height int) {
	return cellSize(readFontViewport(fd))
}

func cellSize(size fontViewport) (width, height int) {
	if size.Columns <= 0 || size.Rows <= 0 || size.PixelWidth <= 0 || size.PixelHeight <= 0 {
		return 1, 2
	}
	width = uniqueFontCell(size.Columns, size.PixelWidth, 1, size.PixelWidth/size.Columns)
	height = uniqueFontCell(size.Rows, size.PixelHeight, 1, size.PixelHeight/size.Rows)
	if width == 0 || height == 0 {
		return 1, 2
	}
	return width, height
}
