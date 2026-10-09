//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package custom

import (
	"errors"
	"os"
	"syscall"
)

func tryProjectLinkLock(*os.File) (bool, error) {
	return false, errors.New("project linking is unavailable on this platform")
}

func projectLinkOpenFlags() int { return syscall.O_NONBLOCK }

func privateProjectLinkPermissions(info os.FileInfo) bool { return info.Mode().Perm()&0077 == 0 }
