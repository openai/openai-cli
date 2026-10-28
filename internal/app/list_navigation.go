package app

import (
	"context"
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/commands/models"
	"github.com/openai/openai-cli/internal/output/listview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
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
	eligible := (format == "" || format == "auto") && opts.OutputKind == OutputPageItem &&
		opts.Transform == "" && !opts.RawOutput && os.Getenv("CI") == "" &&
		os.Getenv("TERM") != "dumb" && isTerminal(opts.Stdout) && term.IsTerminal(os.Stdin.Fd())
	if !eligible {
		return false
	}
	stdout, ok := opts.Stdout.(*os.File)
	if !ok {
		return false
	}
	width, height, err := term.GetSize(stdout.Fd())
	return err == nil && width > 0 && height > 0
}

// JSONListPage is implemented by SDK explicit API page types.
type JSONListPage[P any] = listview.JSONListPage[P]

// ShowJSONPages retains the lazy-page entrypoint with assembly presentation policy.
func ShowJSONPages[P JSONListPage[P]](first func(context.Context) (P, error), maximum int64, opts ShowJSONOpts, cursorField string) error {
	opts.setDefaults()
	return listview.ShowPages(first, maximum, listViewerOptions(opts), cursorField)
}

func listViewerOptions(opts ShowJSONOpts) listview.Options {
	style := listNavigationStyle(opts)
	diagnostics := outputDiagnosticsAllowed(opts.Context)
	// A standalone format hint must not consume a pager step after all records
	// already fit. Explicit output paths retain their existing diagnostics.
	switch opts.Operation {
	case "(resource) files > (method) list", "(resource) batches > (method) list",
		"(resource) admin.organization.projects > (method) list":
		diagnostics = false
	}
	result := listview.Options{
		Context: opts.Context, Stdout: opts.Stdout,
		Style:                style,
		CallerHandlesSignals: opts.CallerHandlesSignals,
		RenderPage: func(items []gjson.Result, width int) (string, error) {
			return renderListNavigationPage(opts, items, width, diagnostics, style)
		},
	}
	if opts.CallerHandlesSignals {
		result.Interrupt = func(err error) error { return &listCommandInterrupt{cause: err, code: 130} }
	}
	if opts.Operation == "(resource) models > (method) list" {
		result.Interrupt = models.Interrupt
	}
	return result
}

// Rendering only consumes loaded items. The table integration can replace this
// bounded presentation call without owning fetch timing or keyboard input.
func renderListNavigationPage(opts ShowJSONOpts, items []gjson.Result, width int, diagnostics bool, style listview.Style) (string, error) {
	if err := opts.Context.Err(); err != nil {
		return "", err
	}
	if (opts.Format == "" || strings.EqualFold(opts.Format, "auto")) &&
		opts.Transform == "" && !opts.RawOutput {
		if opts.Operation == "(resource) models > (method) list" && opts.OutputKind == OutputPageItem {
			content, supported, err := models.RenderTable(opts.Context, items, width, diagnostics, style)
			if err != nil || supported {
				return content, err
			}
		}
		content, supported, err := listview.RenderTable(opts.Context, opts.Operation, items, width, diagnostics, style)
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
	return renderListNavigationLabels(opts, items, diagnostics)
}

// Match help's environment-only color detection without querying stdin or tmux.
func listNavigationStyle(opts ShowJSONOpts) listview.Style {
	profile := colorprofile.NoTTY
	if isTerminal(opts.Stdout) && (opts.Format == "" || strings.EqualFold(opts.Format, "auto")) &&
		os.Getenv("TERM") != "dumb" && os.Getenv("NO_COLOR") == "" &&
		os.Getenv("FORCE_COLOR") != "0" && os.Getenv("CLICOLOR") != "0" {
		profile = colorprofile.Env(os.Environ())
		if runtime.GOOS == "windows" && os.Getenv("TERM") == "" && os.Getenv("WT_SESSION") == "" {
			profile = colorprofile.NoTTY
		}
	}
	return listview.NewStyle(profile, cliterminal.DarkBackground(os.Getenv("COLORFGBG")))
}

// Labeled output preserves values independently of compact table widths.
// RawOutput bypasses the default response and resource summaries.
func renderListNavigationLabels(opts ShowJSONOpts, items []gjson.Result, diagnostics bool) (string, error) {
	readable := readableOutputOptions(opts)
	readable.DiagnosticsAllowed = diagnostics
	return listview.RenderLabels(items, selectOutputTransformer(opts, transformers.Select), readable)
}
