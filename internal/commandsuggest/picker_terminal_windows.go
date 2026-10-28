//go:build windows

package commandsuggest

import (
	"errors"
	"os"

	"github.com/charmbracelet/x/term"
)

// Windows console input uses a separate handle from its output buffer.
func OpenPickerInput(output *os.File) (*os.File, error) {
	if output == nil || !term.IsTerminal(output.Fd()) {
		return nil, errors.New("command search requires terminal input")
	}
	return os.OpenFile("CONIN$", os.O_RDWR, 0)
}
