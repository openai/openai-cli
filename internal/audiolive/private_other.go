//go:build !windows

package audiolive

import "os"

func privateDirectory() (string, error) { return os.MkdirTemp("", "openai-audio-") }
func privateFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}
