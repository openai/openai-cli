//go:build darwin || linux

package custom

import (
	"context"
	"os/exec"
	"syscall"
)

func configureInteractiveImageDiagnostic(ctx context.Context, command *exec.Cmd) {
	if isolated, _ := ctx.Value(interactiveImageDiagnosticContextKey{}).(bool); isolated {
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
}
