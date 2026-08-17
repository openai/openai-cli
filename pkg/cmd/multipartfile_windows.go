//go:build windows

package cmd

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func createReplaySnapshotFile() (*os.File, error) {
	placeholder, err := os.CreateTemp("", "openai-multipart-upload-*")
	if err != nil {
		return nil, err
	}
	path := placeholder.Name()
	if err := placeholder.Close(); err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(
		path,
		os.O_RDWR|os.O_CREATE|os.O_EXCL|windows.O_FILE_FLAG_DELETE_ON_CLOSE,
		0o600,
	)
	if err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	return file, nil
}

func duplicateFile(file *os.File) (*os.File, error) {
	process, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(
		process,
		windows.Handle(file.Fd()),
		process,
		&duplicate,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(duplicate), file.Name()), nil
}
