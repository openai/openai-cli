package custom

import (
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

func showProjectLifecycleResult(value gjson.Result, opts ShowJSONOpts) (bool, error) {
	opts.setDefaults()
	if resolvedOutputFormat(opts) != "text" || opts.Transform != "" || opts.RawOutput || quietOutput(opts.Context) {
		return false, nil
	}
	receipt, supported, err := transformers.ProjectLifecycle(opts.Context, value, transformers.Route{
		Operation: opts.Operation, OutputKind: opts.OutputKind,
	})
	if err != nil {
		return true, err
	}
	if !supported {
		return false, nil
	}
	out := outputWriter{ctx: opts.Context, out: opts.Stdout}
	id := readable.Text(jsonview.SanitizeTerminalString(receipt.ID))
	if err := readable.WriteText(out, "Project "+id+" "+receipt.Action+"."); err != nil {
		return true, err
	}
	if err := readable.Write(out, receipt.Details); err != nil {
		return true, err
	}
	if receipt.Action == "archived" {
		return true, writeOutputHint(opts, "Archived projects cannot be used or updated.\nArchive is not a delete operation.")
	}
	return true, opts.Context.Err()
}
