//go:build windows

package output

import (
	"errors"
	"os"

	"github.com/openai/openai-cli/internal/windowsfile"
)

// CreatePrivateDownloadFile exclusively creates private download staging inside root.
func CreatePrivateDownloadFile(root *os.Root, name string) (*os.File, error) {
	file, err := windowsfile.CreatePrivate(root, name)
	switch {
	case errors.Is(err, windowsfile.ErrInvalidName):
		return nil, errors.New("download staging requires a local filename")
	case errors.Is(err, windowsfile.ErrOwner):
		return nil, errors.New("cannot inspect download file owner")
	case errors.Is(err, windowsfile.ErrPermissions):
		return nil, errors.New("cannot prepare download file permissions")
	}
	return file, err
}
