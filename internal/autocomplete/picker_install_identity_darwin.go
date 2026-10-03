//go:build darwin

package autocomplete

import (
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func pickerUnixFilePath(file *os.File, _ string) (string, error) {
	// F_GETPATH requires MAXPATHLEN bytes and returns the actual filesystem
	// spelling, preserving distinct names on case-sensitive volumes too.
	var path [1024]byte
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, file.Fd(), unix.F_GETPATH, uintptr(unsafe.Pointer(&path[0])))
	runtime.KeepAlive(file)
	if errno != 0 {
		return "", errors.New("cannot inspect shell startup file identity")
	}
	return unix.ByteSliceToString(path[:]), nil
}
