//go:build darwin

package custom

import (
	"os"

	"golang.org/x/sys/unix"
)

func flushAdminSetupInput(input *os.File) error {
	// Darwin TIOCFLUSH takes an int pointer. FREAD selects input only.
	const flushInput = 1
	return unix.IoctlSetPointerInt(int(input.Fd()), unix.TIOCFLUSH, flushInput)
}
