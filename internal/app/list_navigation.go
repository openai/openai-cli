package app

import (
	"context"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/commands/models"
	"github.com/openai/openai-cli/internal/output/listview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/tidwall/gjson"
)

// InteractiveListOutput leaves explicit machine formats and redirected input or
// output on their existing iterator path. It never opens a controlling terminal.
func InteractiveListOutput(opts ShowJSONOpts) bool {
	// Keep the initial rollout within the public-command coverage. Other
	// generated page callbacks retain their existing iterator path.
	switch opts.Operation {
	case "(resource) files > (method) list", "(resource) batches > (method) list",
		"(resource) admin.organization.projects > (method) list":
	default:
		return false
	}
	opts.setDefaults()
	format := strings.ToLower(opts.Format)
	return (format == "" || format == "auto") && opts.OutputKind == OutputPageItem &&
		opts.Transform == "" && !opts.RawOutput && os.Getenv("CI") == "" &&
		os.Getenv("TERM") != "dumb" && isTerminal(opts.Stdout) && term.IsTerminal(os.Stdin.Fd())
}

// JSONListPage is implemented by SDK explicit API page types.
type JSONListPage[P any] = listview.JSONListPage[P]

// ShowJSONPages retains the lazy-page entrypoint with assembly presentation policy.
func ShowJSONPages[P JSONListPage[P]](first func(context.Context) (P, error), maximum int64, opts ShowJSONOpts, cursorField string) error {
	opts.setDefaults()
	return listview.ShowPages(first, maximum, listViewerOptions(opts), cursorField)
}

func listViewerOptions(opts ShowJSONOpts) listview.Options {
	result := listview.Options{
		Context: opts.Context, Stdout: opts.Stdout,
		RenderPage: func(items []gjson.Result, width int) (string, error) {
			return renderListNavigationPage(opts, items, width)
		},
		RenderPrint: func(items []gjson.Result) (string, error) {
			printOpts := opts
			if printOpts.Operation == "(resource) models > (method) list" {
				printOpts.RawOutput = true
			}
			return renderListNavigationLabels(printOpts, items)
		},
	}
	if opts.Operation == "(resource) models > (method) list" {
		result.Interrupt = models.Interrupt
		if opts.OutputKind == OutputPageItem {
			result.PrintHint = models.PrintHint
		}
	}
	return result
}

// Rendering only consumes loaded items. The table integration can replace this
// bounded presentation call without owning fetch timing or keyboard input.
func renderListNavigationPage(opts ShowJSONOpts, items []gjson.Result, width int) (string, error) {
	if err := opts.Context.Err(); err != nil {
		return "", err
	}
	if (opts.Format == "" || strings.EqualFold(opts.Format, "auto")) &&
		opts.Transform == "" && !opts.RawOutput {
		if opts.Operation == "(resource) models > (method) list" && opts.OutputKind == OutputPageItem {
			content, supported, err := renderModelsListTable(opts, items, width)
			if err != nil || supported {
				return content, err
			}
		}
		content, supported, err := listview.RenderTable(opts.Context, opts.Operation, items, width, outputDiagnosticsAllowed(opts.Context))
		if err != nil {
			return "", err
		}
		if err := opts.Context.Err(); err != nil {
			return "", err
		}
		if supported {
			return content, nil
		}
	}
	return renderListNavigationLabels(opts, items)
}

// Labeled output preserves values independently of compact table widths.
// RawOutput bypasses the default response and resource summaries.
func renderListNavigationLabels(opts ShowJSONOpts, items []gjson.Result) (string, error) {
	return listview.RenderLabels(items, selectOutputTransformer(opts, transformers.Select), readableOutputOptions(opts))
}
