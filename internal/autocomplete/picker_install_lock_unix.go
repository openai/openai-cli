//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package autocomplete

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func pickerInstallOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func tryPickerInstallLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
