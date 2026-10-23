package app

import (
	"context"
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/commands/models"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func configureModelsList(root *cli.Command) {
	models.Configure(root, models.Options{
		LoadingAllowed: func(command *cli.Command) bool {
			root := command.Root()
			format := strings.ToLower(root.String("format"))
			return (format == "" || format == "auto" || format == "text") &&
				root.String("transform") == "" && !root.Bool("raw-output") && !root.Bool("debug") &&
				errorOutputFormat(root) == "text" && root.String("transform-error") == "" && os.Getenv("CI") == "" &&
				term.IsTerminal(os.Stdin.Fd()) && isTerminal(os.Stdout) && isTerminal(os.Stderr)
		},
		StartFeedback: func(ctx context.Context, command *cli.Command) func() {
			if !commandAllowsOutputDiagnostics(command) {
				return func() {}
			}
			stop, _ := clioutput.StartLoadingFeedback(ctx, os.Stderr, "Loading models", "", clioutput.LoadingAnimationSupported(os.Getenv),
				clioutput.LoadingSpinner(os.Getenv, runtime.GOOS), func() (int, int) {
					width, height, _ := term.GetSize(os.Stderr.Fd())
					return width, height
				})
			return stop
		},
	})
}

func showModelsListSelection[T any](source jsonview.Iterator[T], maximum int64, opts ShowJSONOpts) (bool, error) {
	if opts.Operation != "(resource) models > (method) list" || opts.OutputKind != OutputPageItem {
		return false, nil
	}
	ctx, selected, handled, err := models.Select(opts.Context, source, maximum)
	if !handled || err != nil {
		return handled, err
	}
	opts.Context = ctx
	return true, ShowJSONIterator(selected, -1, opts)
}

func stopModelsListLoading(opts ShowJSONOpts) {
	if opts.Operation == "(resource) models > (method) list" {
		models.StopLoading(opts.Context)
	}
}

func renderModelsListTable(opts ShowJSONOpts, items []gjson.Result, width int) (string, bool, error) {
	return models.RenderTable(opts.Context, items, width, outputDiagnosticsAllowed(opts.Context))
}

// Models use the SDK's single-response Page, whose GetNextPage always returns
// nil. Check that concrete pager before collecting its already-loaded items.
// Cursor pagers never enter this path, even with misleading operation metadata.
func showModelsListViewer[T any](source jsonview.Iterator[T], iter *clioutput.Iterator[T], opts ShowJSONOpts) (bool, error) {
	if !models.IsListSource(source) {
		return false, nil
	}
	if opts.Operation != "(resource) models > (method) list" || opts.OutputKind != OutputPageItem ||
		opts.Format != "" && !strings.EqualFold(opts.Format, "auto") || opts.Transform != "" || opts.RawOutput ||
		os.Getenv("CI") != "" || os.Getenv("TERM") == "dumb" || !isTerminal(opts.Stdout) || !term.IsTerminal(os.Stdin.Fd()) {
		return false, nil
	}
	width, height, err := term.GetSize(opts.Stdout.(*os.File).Fd())
	if err != nil || width <= 0 || height <= 0 {
		return false, nil
	}
	if opts.Stdout == os.Stdout {
		return true, clioutput.StreamToStdout(func(*os.File) error { return models.WriteList(iter, listViewerOptions(opts), width, height) })
	}
	return true, models.WriteList(iter, listViewerOptions(opts), width, height)
}
