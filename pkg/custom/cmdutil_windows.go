//go:build windows

package custom

import "os"

func streamOutputOSSpecific(label string, generateOutput func(w *os.File) error) error {
	// We have a trick with sockets that we use when possible on Unix-like systems. Those APIs aren't
	// available on Windows, so we fall back to using pipes.
	return streamToPagerWithPipe(label, generateOutput)
}
