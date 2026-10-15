//go:build windows

package custom

import (
	"errors"
	"os"

	"github.com/openai/openai-cli/internal/windowsfile"
)

func createPrivateBatchDirectory(path string) (os.FileInfo, error) {
	directory, err := windowsfile.CreatePrivateDirectory(path)
	if err != nil {
		return nil, err
	}
	info, err := directory.Stat()
	return info, errors.Join(err, directory.Close())
}
