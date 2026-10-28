//go:build !windows

package commandsuggest

import (
	"errors"
	"os"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// OpenPickerInput owns a readable duplicate of the supplied physical terminal.
// Candidate stdin can be a pipe, and shell widgets may lack a controlling tty.
func OpenPickerInput(output *os.File) (*os.File, error) {
	if output == nil || !term.IsTerminal(output.Fd()) {
		return nil, errors.New("command search requires terminal input")
	}
	duplicate, err := unix.Dup(int(output.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(duplicate)
	input := os.NewFile(uintptr(duplicate), "command-picker-terminal")
	flags, err := unix.FcntlInt(input.Fd(), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE == unix.O_WRONLY {
		_ = input.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("command search requires a readable terminal")
	}
	return input, nil
}
