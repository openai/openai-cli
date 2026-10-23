package app

import (
	"context"
	"io"
	"os"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/commands/images"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// ImageModelOptions binds discovery to application policy.
func ImageModelOptions() images.ModelOptions {
	return images.ModelOptions{
		RequestOptions:        FlagOptions,
		DefaultRequestOptions: GetDefaultRequestOptions,
		DiagnosticsAllowed:    outputDiagnosticsAllowed,
		HumanOutput: func(command *cli.Command) bool {
			root := command.Root()
			return root.String("transform") == "" && !root.Bool("raw-output") && resolvedOutputFormat(ShowJSONOpts{Format: root.String("format")}) == "text"
		},
		ShowData: func(ctx context.Context, command *cli.Command, obj gjson.Result, stderr io.Writer) error {
			root := command.Root()
			return ShowJSON(obj, ShowJSONOpts{
				Context: ctx, Format: root.String("format"), ExplicitFormat: root.IsSet("format"),
				RawOutput: root.Bool("raw-output"), Title: "Image models", Transform: root.String("transform"),
				Stdout: root.Writer, Stderr: stderr,
			})
		},
	}
}

func isSavedImageRoute(opts ShowJSONOpts, kind OutputKind) bool {
	if opts.OutputKind != kind || opts.Context == nil {
		return false
	}
	switch opts.Operation {
	case "(resource) images > (method) generate", "(resource) images > (method) edit", "(resource) images > (method) create_variation":
		return true
	default:
		return false
	}
}

// ImageSavingOptions binds image preparation to application request and diagnostic policy.
func ImageSavingOptions() images.SavingOptions {
	return images.SavingOptions{RequestOptions: FlagOptions, DiagnosticsAllowed: outputDiagnosticsAllowed, ErrorFormat: errorOutputFormat}
}

// ImagePreviewOptions binds local previews to application receipt and error policy.
func ImagePreviewOptions() images.PreviewOptions {
	return images.PreviewOptions{Receipt: writeOutputReceipt, SuggestedReceipt: writeSuggestedImageReceipt, ShowError: ShowCommandError}
}

// Keep image preference receipts on the existing optional diagnostic channel.
func writeSuggestedImageReceipt(ctx context.Context, command *cli.Command, message string, suggestions []string) error {
	if !outputDiagnosticsAllowed(ctx) {
		return nil
	}
	presenter := clihelp.NewSuggestedTextTo(clioutput.Writer{Context: ctx, Sink: os.Stderr}, os.Stderr, command)
	for _, suggestion := range suggestions {
		clihelp.SuggestedCommand(command, []io.Writer{presenter}, suggestion)
	}
	return presenter.WriteText(message)
}
