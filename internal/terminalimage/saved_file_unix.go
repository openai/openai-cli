//go:build unix

package terminalimage

import (
	"os"

	"golang.org/x/sys/unix"
)

func openSavedImage(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY, 0)
}
