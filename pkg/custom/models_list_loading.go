package custom

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/urfave/cli/v3"
)

type modelsListLoadingKey struct{}

// The generated action receives the complete SDK response before presentation.
// Keep feedback active through body reads and retries, then join its worker
// before any result or viewer can write to the same terminal.
func runWithModelsListLoading(ctx context.Context, command *cli.Command, next cli.ActionFunc) (err error) {
	root := command.Root()
	format := strings.ToLower(root.String("format"))
	if format != "" && format != "auto" && format != "text" ||
		root.String("transform") != "" || root.Bool("raw-output") || root.Bool("debug") || root.Bool("quiet") ||
		errorOutputFormat(root) != "text" || root.String("transform-error") != "" || os.Getenv("CI") != "" ||
		!term.IsTerminal(os.Stdin.Fd()) || !isTerminal(os.Stdout) || !isTerminal(os.Stderr) {
		return next(ctx, command)
	}
	parent := ctx
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt)
	stopReset := context.AfterFunc(ctx, stopSignals)
	stop := startLoadingFeedback(ctx, os.Stderr, "Loading models", loadingAnimationSupported(os.Getenv),
		imageLoadingSpinner(os.Getenv, runtime.GOOS), func() int {
			width, _, _ := term.GetSize(os.Stderr.Fd())
			return width
		})
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
			err = &modelsListLoadingInterrupt{err}
		}
	}()
	return next(ctx, command)
}

type modelsListLoadingInterrupt struct{ error }

func (e *modelsListLoadingInterrupt) Unwrap() error { return e.error }
func (e *modelsListLoadingInterrupt) ExitCode() int { return 130 }

func stopModelsListLoading(opts ShowJSONOpts) {
	if opts.Operation != "(resource) models > (method) list" {
		return
	}
	if stop, ok := opts.Context.Value(modelsListLoadingKey{}).(func()); ok {
		stop()
	}
}
