package images

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"runtime"

	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// Feedback starts after request preparation, so it never changes stdin reading
// or validation. It owns no request/output writers and cannot replace a failure.
func runWithImageLoading(ctx context.Context, command *cli.Command, plan *imageOutputPlan, next cli.ActionFunc, opts SavingOptions) (err error) {
	root := command.Root()
	out := imageCommandWriter(command)
	if plan == nil || !cliterminal.IsWriterTerminal(out) ||
		root.Bool("debug") || opts.ErrorFormat(root) != "text" || root.String("transform-error") != "" {
		return next(ctx, command)
	}
	feedback := os.Stderr
	if plan.loadingPrompt != "" {
		// Keep the visible picker prompt on its UI surface, outside diagnostics.
		feedback = out.(*os.File) // isTerminal above requires an *os.File.
	}
	if !cliterminal.IsWriterTerminal(feedback) {
		return next(ctx, command)
	}
	label := "Generating image"
	switch command.Name {
	case "edit":
		label = "Editing image"
	case "create-variation":
		label = "Creating image variation"
	}
	parent := ctx
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt)
	// Restore normal signal handling once cancellation starts. A second Ctrl-C
	// can still terminate the process if a terminal write or cleanup is blocked.
	stopReset := context.AfterFunc(ctx, stopSignals)
	stopLoading := func() {}
	advance := func(clioutput.LoadingStage) {}
	if opts.DiagnosticsAllowed(ctx) {
		stopLoading, advance = clioutput.StartLoadingFeedback(ctx, feedback, label, plan.loadingPrompt, clioutput.LoadingAnimationSupported(os.Getenv), clioutput.LoadingSpinner(os.Getenv, runtime.GOOS), func() (int, int) {
			width, height, _ := term.GetSize(feedback.Fd())
			return width, height
		})
	}
	plan.stopLoading = stopLoading
	plan.loadingStage = advance
	defer func() {
		stopLoading()
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
	return next(ctx, command)
}

// Keep the cancellation cause and any saving guidance while using the shell's
// conventional Ctrl-C status. Ordinary API/local errors keep their own status.
type imageLoadingInterrupt struct{ error }

func (e *imageLoadingInterrupt) Unwrap() error { return e.error }
func (e *imageLoadingInterrupt) ExitCode() int { return 130 }

func (p *imageOutputPlan) stopLoadingFeedback() {
	if p.stopLoading != nil {
		p.stopLoading()
	}
}

func (p *imageOutputPlan) setLoadingStage(stage clioutput.LoadingStage) {
	if p != nil && p.loadingStage != nil {
		p.loadingStage(stage)
	}
}
