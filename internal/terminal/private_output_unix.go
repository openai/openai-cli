//go:build !windows

package terminal

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// OpenPrivateOutput duplicates a writable, empty, private regular file supplied by the shell.
func OpenPrivateOutput(fd int, name string) *os.File {
	// Own only the duplicate. Invalid configuration must not close a caller's FD.
	duplicate, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	file := os.NewFile(uintptr(duplicate), name)
	flags, err := unix.FcntlInt(uintptr(duplicate), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE == unix.O_RDONLY {
		_ = file.Close()
		return nil
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 0 {
		_ = file.Close()
		return nil
	}
	position, err := file.Seek(0, io.SeekCurrent)
	if err != nil || position != 0 {
		_ = file.Close()
		return nil
	}
	return file
}
