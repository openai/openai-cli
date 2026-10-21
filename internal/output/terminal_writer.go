package output

import (
	"github.com/charmbracelet/x/term"
	"os"
)

// TerminalWriter preserves terminal detection when a viewer uses guarded output.
// Close retains ownership semantics by closing File; it is not a borrowed writer.
type TerminalWriter struct {
	Writer
	File *os.File
}

var _ term.File = TerminalWriter{}

func (w TerminalWriter) Fd() uintptr                { return w.File.Fd() }
func (w TerminalWriter) Read(p []byte) (int, error) { return w.File.Read(p) }
func (w TerminalWriter) Close() error               { return w.File.Close() }
