//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package autocomplete

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Atomic profile replacement would separate hard links that share settings.
// Inspect the opened file at each metadata check, including before replacement.
func checkPickerFileLinks(file *os.File) error {
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return errors.New("cannot inspect shell integration file links")
	}
	if info.Mode&unix.S_IFMT == unix.S_IFREG && info.Nlink != 1 {
		return errors.New("shell integration file has hard links; existing files were kept")
	}
	return nil
}
