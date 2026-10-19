//go:build darwin

package admin

import (
	"os"

	"golang.org/x/sys/unix"
)

const adminSetupInputSupported = true

func flushAdminSetupInput(input *os.File) error {
	// Darwin TIOCFLUSH takes an int pointer. FREAD selects input only.
	const flushInput = 1
	return unix.IoctlSetPointerInt(int(input.Fd()), unix.TIOCFLUSH, flushInput)
}
