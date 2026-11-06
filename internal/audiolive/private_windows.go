//go:build windows

package audiolive

import (
	"crypto/rand"
	"os"
	"path/filepath"

	"github.com/openai/openai-cli/internal/windowsfile"
)

func privateDirectory() (string, error) {
	path := filepath.Join(os.TempDir(), "openai-audio-"+rand.Text())
	file, err := windowsfile.CreatePrivateDirectory(path)
	if err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
func privateFile(root *os.Root, name string) (*os.File, error) {
	return windowsfile.CreatePrivate(root, name)
}
