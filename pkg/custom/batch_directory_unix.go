//go:build !windows

package custom

import (
	"errors"
	"os"
)

func createPrivateBatchDirectory(path string) (os.FileInfo, error) {
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, err
	}
	created, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !created.IsDir() {
		return created, errors.New("new output directory changed")
	}
	// Mkdir applies umask. Restore owner access only for our new directory.
	if err := os.Chmod(path, 0700); err != nil {
		return created, err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return created, err
	}
	if !current.IsDir() || !os.SameFile(created, current) {
		return created, errors.New("new output directory changed")
	}
	return created, nil
}
