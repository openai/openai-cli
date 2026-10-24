//go:build !windows

package commandsuggest

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func privateSink(fd int) io.WriteCloser {
	// Own only the duplicate. Invalid configuration must not close a caller's FD.
	duplicate, err := unix.Dup(fd)
	if err != nil {
		return nil
	}
	unix.CloseOnExec(duplicate)
	file := os.NewFile(uintptr(duplicate), "command-suggestions")
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
