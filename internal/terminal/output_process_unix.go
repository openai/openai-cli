//go:build darwin || linux

package terminal

import (
	"context"
	"os/exec"
	"syscall"
)

func configureOutputProcess(ctx context.Context, command *exec.Cmd) {
	if isolated, _ := ctx.Value(detachedOutputContextKey{}).(bool); isolated {
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
}
