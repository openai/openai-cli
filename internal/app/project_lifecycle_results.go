package app

import (
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/listview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/tidwall/gjson"
)

func showProjectLifecycleResult(value gjson.Result, opts ShowJSONOpts) (bool, error) {
	opts.setDefaults()
	if resolvedOutputFormat(opts) != "text" || opts.Transform != "" || opts.RawOutput || quietOutput(opts.Context) {
		return false, nil
	}
	if opts.Operation == "(resource) admin.organization.projects > (method) retrieve" && opts.OutputKind == OutputResponse {
		if opts.ExplicitFormat || os.Getenv("CI") != "" || !terminal.AutomaticPresentation("auto", isTerminal(opts.Stdout) && isTerminal(os.Stdin), os.Getenv) {
			return false, nil
		}
		details, err := transformers.OrganizationProjectDetails(opts.Context, value)
		if err != nil || !details.Exists() {
			return err != nil, err
		}
		var width int
		if file, ok := opts.Stdout.(interface{ Fd() uintptr }); ok {
			width, _, _ = term.GetSize(file.Fd())
		}
		out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
		style := listview.NewFieldStyle(resourceColorProfile(opts), os.Getenv("COLORFGBG"))
		if err := writeProjectDetails(out, details, width, style); err != nil {
			return true, err
		}
		recordDefaultAPIResponse(opts)
		return true, opts.Context.Err()
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
	out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
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
