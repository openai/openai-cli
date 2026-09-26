//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package imagegallery

import (
	"errors"
	"os"
)

func lockFile(*os.File) error {
	return errors.New("image gallery locking is unsupported on this platform")
}
func unlockFile(*os.File) error { return nil }
