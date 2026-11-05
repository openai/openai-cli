package models

import (
	"context"
	"errors"
	"os"
	"os/signal"

	"github.com/urfave/cli/v3"
)

type modelsListLoadingKey struct{}

// The generated action receives the complete SDK response before presentation.
// Keep feedback active through body reads and retries, then join its worker
// before any result or viewer can write to the same terminal.
func runWithModelsListLoading(ctx context.Context, command *cli.Command, next cli.ActionFunc, policy Options) (err error) {
	if policy.LoadingAllowed == nil || !policy.LoadingAllowed(command) {
		return next(ctx, command)
	}
	parent := ctx
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt)
	stopReset := context.AfterFunc(ctx, stopSignals)
	stop := func() {}
	if policy.StartFeedback != nil {
		stop = policy.StartFeedback(ctx, command)
	}
	ctx = context.WithValue(ctx, modelsListLoadingKey{}, stop)
	defer func() {
		stop()
		stopReset()
		interrupted := ctx.Err() != nil && parent.Err() == nil
		stopSignals()
		var exit cli.ExitCoder
		if interrupted && (err == nil || errors.Is(err, context.Canceled)) && !errors.As(err, &exit) {
			if err == nil {
				err = ctx.Err()
			}
			err = &LoadingInterrupt{err}
		}
	}()
	return next(ctx, command)
}

// LoadingInterrupt retains cancellation and the command interrupt exit status.
type LoadingInterrupt struct{ error }

func (e *LoadingInterrupt) Unwrap() error { return e.error }
func (e *LoadingInterrupt) ExitCode() int { return 130 }

// StopLoading joins loading feedback before results write to the terminal.
func StopLoading(ctx context.Context) {
	if stop, ok := ctx.Value(modelsListLoadingKey{}).(func()); ok {
		stop()
	}
}

// Interrupt preserves the models command's process status after viewer cancellation.
func Interrupt(err error) error { return &LoadingInterrupt{err} }
