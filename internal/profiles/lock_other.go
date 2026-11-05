//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package profiles

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(*os.File) (bool, error) {
	return false, errors.New("profile changes are unavailable on this platform")
}

func openFlags() int { return syscall.O_NONBLOCK }

func privatePermissions(info os.FileInfo) bool { return info.Mode().Perm()&0077 == 0 }
