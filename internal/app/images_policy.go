package app

import (
	"context"
	"io"

	"github.com/openai/openai-cli/internal/commands/images"
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
	return images.PreviewOptions{Receipt: writeOutputReceipt, ShowError: ShowCommandError}
}
