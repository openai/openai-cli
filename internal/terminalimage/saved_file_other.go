//go:build !unix

package terminalimage

import "os"

// These platforms do not support Unix FIFO open semantics. The opened file is
// still checked before reading so devices and named pipes cannot be decoded.
func openSavedImage(path string) (*os.File, error) {
	return os.Open(path)
}
