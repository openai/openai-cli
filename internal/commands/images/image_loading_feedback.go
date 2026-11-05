package images

import (
	"context"
	"os"
	"runtime"

	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// Feedback starts after request preparation, so it never changes stdin reading
// or validation. It owns no request/output writers and cannot replace a failure.
func runWithImageLoading(ctx context.Context, command *cli.Command, plan *imageOutputPlan, next cli.ActionFunc, opts SavingOptions) error {
	if workspace, _ := ctx.Value(imagePickerWorkspaceKey{}).(*imagePickerWorkspace); workspace != nil && plan != nil {
		plan.loadingStage = func(stage clioutput.LoadingStage) { workspace.send(imagePickerWorkspaceStage(stage)) }
		return next(ctx, command)
	}
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
	defer stopLoading()
	return next(ctx, command)
}

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
