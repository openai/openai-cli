//go:build linux

package custom

import (
	"os"

	"golang.org/x/sys/unix"
)

const adminSetupInputSupported = true

func flushAdminSetupInput(input *os.File) error {
	return unix.IoctlSetInt(int(input.Fd()), unix.TCFLSH, unix.TCIFLUSH)
}
