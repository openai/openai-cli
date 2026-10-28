package app

import (
	"context"
	"io"
	"os"

	"github.com/openai/openai-cli/internal/apiquery"
	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func configureBatchCommands(root *cli.Command) {
	batchcommands.Configure(root, batchcommands.Options{
		DefaultRequestOptions: GetDefaultRequestOptions,
		RequestOptions: func(cmd *cli.Command, stdin io.Reader) ([]option.RequestOption, error) {
			return flagOptionsWithStdin(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, false, stdin)
		},
		ShowResult: func(ctx context.Context, cmd *cli.Command, batch gjson.Result, operation string, out, diagnostics io.Writer) error {
			root := cmd.Root()
			return ShowJSON(batch, ShowJSONOpts{
				CallerHandlesSignals: true,
				Context:              ctx, Operation: "(resource) batches > (method) " + operation, OutputKind: OutputResponse,
				Format: root.String("format"), ExplicitFormat: root.IsSet("format"), RawOutput: root.Bool("raw-output"),
				Transform: root.String("transform"), Title: "batches " + operation, Stdout: out, Stderr: diagnostics,
			})
		},
		HumanTerminal:   batchHumanTerminal,
		APIErrorMessage: readableAPIErrorMessage,
	})
}

func batchHumanTerminal(ctx context.Context, root *cli.Command) bool {
	return outputDiagnosticsAllowed(ctx) && isTerminal(root.Writer) && isTerminal(os.Stderr) && !root.Bool("debug") &&
		!root.Bool("raw-output") && root.String("transform") == "" &&
		root.String("transform-error") == "" && errorOutputFormat(root) == "text" &&
		resolvedOutputFormat(ShowJSONOpts{Format: root.String("format")}) == "text"
}

func showBatchNextCommand(batch gjson.Result, opts ShowJSONOpts) error {
	opts.setDefaults()
	return batchcommands.ShowNextCommand(batch, batchcommands.HintOptions{
		Context: opts.Context, Route: opts.Operation, Response: opts.OutputKind == OutputResponse,
		Terminal: isTerminal(opts.Stdout) && isTerminal(opts.Stderr), Transform: opts.Transform,
		RawOutput: opts.RawOutput, Format: resolvedOutputFormat(opts), Stderr: opts.Stderr,
		HumanTerminal: batchHumanTerminal,
	})
}
