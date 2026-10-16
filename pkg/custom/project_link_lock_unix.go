//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package custom

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func tryProjectLinkLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func projectLinkOpenFlags() int { return syscall.O_NONBLOCK | syscall.O_NOFOLLOW }

func privateProjectLinkPermissions(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0
}
