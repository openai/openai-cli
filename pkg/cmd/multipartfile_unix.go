//go:build !windows

package cmd

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func createReplaySnapshotFile() (*os.File, error) {
	file, err := os.CreateTemp("", "openai-multipart-upload-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(file.Name()); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func duplicateFile(file *os.File) (*os.File, error) {
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), file.Name()), nil
}
