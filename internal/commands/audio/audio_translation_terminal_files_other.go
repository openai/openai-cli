//go:build !windows

package audio

import "os"

func terminalTranslationDirectory(parent, prefix string) (string, error) {
	return os.MkdirTemp(parent, prefix)
}
