//go:build windows

package binaryparam

import (
	"io"
	"os"
)

func CancellableFile(file *os.File, info os.FileInfo) (io.ReadCloser, error) {
	return file, nil
}
