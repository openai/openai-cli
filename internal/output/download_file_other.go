//go:build !windows

package output

import (
	"os"
	"syscall"
)

// CreatePrivateDownloadFile exclusively creates private download staging inside root.
func CreatePrivateDownloadFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

// OpenDownloadDestination opens an existing output without following its final
// symlink or modifying its contents. nonblock protects regular-file inspection
// against a concurrent substitution with a FIFO.
func OpenDownloadDestination(path string, nonblock bool) (*os.File, error) {
	flags := os.O_WRONLY | syscall.O_NOFOLLOW
	if nonblock {
		flags |= syscall.O_NONBLOCK
	}
	return os.OpenFile(path, flags, 0)
}
