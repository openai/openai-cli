package terminal

import (
	"io"
	"os"

	"github.com/charmbracelet/x/term"
)

// IsWriterTerminal recognizes the file and borrowed-file writers used by CLI output.
func IsWriterTerminal(w io.Writer) bool {
	switch v := w.(type) {
	case BorrowedWriter:
		return term.IsTerminal(v.File.Fd())
	case *os.File:
		return term.IsTerminal(v.Fd())
	default:
		return false
	}
}
