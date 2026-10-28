//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package imageprefs

import (
	"errors"
	"os"
)

func tryWriterLock(*os.File) (bool, error) {
	return false, errors.New("image preference locking is unavailable on this platform")
}
