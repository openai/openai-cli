//go:build !windows

package cmd

import (
	"os"

	"golang.org/x/sys/unix"
)

func duplicateFile(file *os.File) (*os.File, error) {
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), file.Name()), nil
}
