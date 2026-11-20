//go:build windows

package audio

import (
	"crypto/rand"
	"github.com/openai/openai-cli/internal/windowsfile"
	"os"
	"path/filepath"
)

func terminalTranslationDirectory(parent, prefix string) (string, error) {
	if parent == "" {
		parent = os.TempDir()
	}
	path := filepath.Join(parent, prefix+rand.Text())
	file, err := windowsfile.CreatePrivateDirectory(path)
	if err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
