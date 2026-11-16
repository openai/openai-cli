//go:build windows

package terminal

import (
	"errors"
	"os"

	"github.com/charmbracelet/x/term"
)

// OpenInputFromOutput returns an owned input handle for the attached console.
// Windows uses separate handles for console input and output.
func OpenInputFromOutput(output *os.File) (*os.File, error) {
	if output == nil || !term.IsTerminal(output.Fd()) {
		return nil, errors.New("output is not a terminal")
	}
	return os.OpenFile("CONIN$", os.O_RDWR, 0)
}
