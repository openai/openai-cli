//go:build !windows && !darwin

package autocomplete

import (
	"os"
	"path/filepath"
)

func pickerUnixFilePath(_ *os.File, path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
