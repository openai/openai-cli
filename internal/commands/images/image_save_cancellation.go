package images

import (
	"context"
	"errors"
	"os"
	"os/signal"

	"github.com/urfave/cli/v3"
)

// Saving needs cancellation even when loading feedback cannot be displayed.
// Explicit API output and the picker's signal ownership keep their own paths.
func runWithImageSaving(ctx context.Context, command *cli.Command, plan *imageOutputPlan, next cli.ActionFunc, opts SavingOptions) (err error) {
	workspace, _ := ctx.Value(imagePickerWorkspaceKey{}).(*imagePickerWorkspace)
	if plan == nil || workspace != nil {
		return runWithImageLoading(ctx, command, plan, next, opts)
	}
	parent := ctx
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt)
	// Restore normal signal handling once cancellation starts. A second Ctrl-C
	// can still terminate the process if a terminal write or cleanup is blocked.
	stopReset := context.AfterFunc(ctx, stopSignals)
	defer func() {
		stopReset()
		interrupted := ctx.Err() != nil && parent.Err() == nil
		stopSignals()
		var exit cli.ExitCoder
		if interrupted && (err == nil || errors.Is(err, context.Canceled)) && !errors.As(err, &exit) {
			if err == nil {
				err = ctx.Err()
			}
			err = &imageLoadingInterrupt{err}
		}
	}()
	return runWithImageLoading(ctx, command, plan, next, opts)
}

// Keep the cancellation cause and any saving guidance while using the shell's
// conventional Ctrl-C status. Ordinary API/local errors keep their own status.
type imageLoadingInterrupt struct{ error }

func (e *imageLoadingInterrupt) Unwrap() error { return e.error }
func (e *imageLoadingInterrupt) ExitCode() int { return 130 }
