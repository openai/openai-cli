package imageopen

import (
	"context"
	"syscall"
	"time"
)

func launch(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// xdg-open may remain alive until its viewer closes. Detach the request from
	// this terminal, keep all streams off the CLI, and reap it without waiting
	// for the image window. Later CLI cancellation must not kill that viewer.
	command := viewerCommand(ctx, "xdg-open", path, true)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return startDetached(ctx, command, 500*time.Millisecond)
}
