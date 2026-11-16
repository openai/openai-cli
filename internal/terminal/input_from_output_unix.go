//go:build !windows

package terminal

import (
	"errors"
	"os"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// OpenInputFromOutput returns an owned keyboard input for the supplied terminal.
// Stdin can be a pipe, and shell widgets may lack a controlling terminal.
func OpenInputFromOutput(output *os.File) (*os.File, error) {
	if output == nil || !term.IsTerminal(output.Fd()) {
		return nil, errors.New("output is not a terminal")
	}
	duplicate, err := unix.FcntlInt(output.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	input := os.NewFile(uintptr(duplicate), "terminal-input")
	flags, err := unix.FcntlInt(input.Fd(), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE == unix.O_WRONLY {
		_ = input.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("terminal output does not permit reading")
	}
	return input, nil
}
