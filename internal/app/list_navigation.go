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
	// The caller establishes that this is a read-only list. Generated iterators
	// can also yield page items from mutations, which retain their own routing.
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
	// Additional hints must not consume space meant for data rows.
	diagnostics = diagnostics && opts.Operation == "(resource) models > (method) list"
	result := listview.Options{
		Context: opts.Context, Stdout: opts.Stdout,
		Style:                style,
		CallerHandlesSignals: opts.CallerHandlesSignals,
		RenderPage: func(items []gjson.Result, width int) (string, error) {
			return renderListNavigationPage(opts, items, width, diagnostics, style)
		},
	}
	if opts.OutputKind == OutputPageItem && (opts.Format == "" || strings.EqualFold(opts.Format, "auto")) && opts.Transform == "" && !opts.RawOutput {
		result.Singular, result.Plural = listRangeNames(opts.Operation, opts.Title)
		result.RenderTable = func(items []gjson.Result, width int) (string, bool, error) {
			return listview.RenderRecordTable(opts.Context, opts.Operation, items, width, style)
		}
		result.RenderRecordLabels = func(item gjson.Result, _ bool) (string, error) {
			return renderListNavigationLabels(opts, []gjson.Result{item}, false)
		}
		if opts.Operation == "(resource) models > (method) list" {
			models.ConfigureRecordView(&result)
			if diagnostics {
				result.Hint = "Details: --format json"
			}
		}
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

// Generated list methods name the results explicitly (list_files) or inherit
// the final resource component. A handwritten list can supply its command path.
func listRangeNames(operation, title string) (singular, plural string) {
	name := ""
	if transformers.IsResourceListOperation(operation) {
		resource, method, _ := strings.Cut(strings.TrimPrefix(operation, "(resource) "), " > (method) ")
		if method == "list" {
			name = resource[strings.LastIndexByte(resource, '.')+1:]
		} else {
			name = strings.TrimPrefix(method, "list_")
		}
	} else if path := strings.Fields(title); len(path) > 1 && path[len(path)-1] == "list" {
		name = strings.ReplaceAll(path[len(path)-2], "-", "_")
	}
	if name == "external_storage" {
		return "Storage configuration", "Storage configurations"
	}
	if !strings.HasSuffix(name, "s") || strings.ContainsAny(name, " :") {
		return "Result", "Results"
	}
	words := strings.Split(name, "_")
	for i, word := range words {
		if word == "" || strings.Trim(word, "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
			return "Result", "Results"
		}
		if word == "api" {
			words[i] = "API"
		}
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	plural = strings.Join(words, " ")
	switch {
	case strings.HasSuffix(plural, "ches"):
		singular = strings.TrimSuffix(plural, "es")
	case strings.HasSuffix(plural, "ies"):
		singular = strings.TrimSuffix(plural, "ies") + "y"
	default:
		singular = strings.TrimSuffix(plural, "s")
	}
	return singular, plural
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
