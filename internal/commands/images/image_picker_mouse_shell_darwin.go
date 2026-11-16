//go:build darwin

package images

import (
	"context"
	"os"

	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"golang.org/x/sys/unix"
)

// Only an immediate shell caller with cooked, foreground terminal input qualifies.
func imagePickerNativeMouseShell(ctx context.Context, input, output *os.File) bool {
	if input == nil || output == nil || !imagePickerMouseShellEnvironment(os.Getenv) || cliterminal.ParentShell(ctx) == "" {
		return false
	}
	in, err := input.Stat()
	if err != nil {
		return false
	}
	out, err := output.Stat()
	if err != nil || !os.SameFile(in, out) {
		return false
	}
	group, err := unix.IoctlGetInt(int(input.Fd()), unix.TIOCGPGRP)
	if err != nil || group != unix.Getpgrp() {
		return false
	}
	state, err := unix.IoctlGetTermios(int(input.Fd()), unix.TIOCGETA)
	return err == nil && state.Lflag&(unix.ICANON|unix.ECHO) == unix.ICANON|unix.ECHO
}
