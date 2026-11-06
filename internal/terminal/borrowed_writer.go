package terminal

import (
	"io"
	"os"
)

// BorrowedWriter redirects writes while preserving a borrowed terminal's identity.
// Closing the adapter never closes the caller's file.
type BorrowedWriter struct {
	io.Writer
	File *os.File
}

func (w BorrowedWriter) Fd() uintptr                { return w.File.Fd() }
func (w BorrowedWriter) Read(p []byte) (int, error) { return w.File.Read(p) }
func (w BorrowedWriter) Close() error               { return nil }
