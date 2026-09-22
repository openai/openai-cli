package terminalimage

import "github.com/charmbracelet/x/term"

// Size describes the whole terminal viewport, in cells and (when known) pixels.
// Pixel dimensions are optional; zero means the terminal did not provide them.
type Size struct {
	Columns, Rows           int
	PixelWidth, PixelHeight int
}

// TerminalSize reads the output terminal geometry without queries or stdin.
// If the descriptor is not a terminal, it returns an unknown (zero) size.
func TerminalSize(fd uintptr) Size {
	size := terminalPixelSize(fd)
	if size.Columns > 0 && size.Rows > 0 {
		return size
	}
	if columns, rows, err := term.GetSize(fd); err == nil {
		size.Columns, size.Rows = columns, rows
	}
	return size
}
