package app

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/commands/models"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

func configureModelsList(root *cli.Command) {
	models.Configure(root, models.Options{
		ShowPages: showInteractiveModelsList,
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

// Explicit formats and bounded selection retain their existing single-response ordering.
func showInteractiveModelsList(ctx context.Context, cmd *cli.Command) (bool, error) {
	root := cmd.Root()
	opts := ShowJSONOpts{
		Context: ctx, Operation: "(resource) models > (method) list", OutputKind: OutputPageItem,
		Format: root.String("format"), RawOutput: root.Bool("raw-output"), Transform: root.String("transform"),
		Title: "models list",
	}
	if root.IsSet("format") || cmd.IsSet("max-items") && cmd.Value("max-items").(int64) != -1 || !InteractiveListOutput(opts) {
		return false, nil
	}
	if args := cmd.Args().Slice(); len(args) != 0 {
		return true, fmt.Errorf("Unexpected extra arguments: %v", args)
	}
	options, err := FlagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, false)
	if err != nil {
		return true, err
	}
	client := openai.NewClient(GetDefaultRequestOptions(cmd)...)
	opts.setDefaults()
	return true, runInteractiveList(opts, func(opts ShowJSONOpts) error {
		return models.ShowPages(listViewerOptions(opts), client, options, outputDiagnosticsAllowed(opts.Context))
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
		return true, clioutput.StreamToStdout(func(*os.File) error {
			return models.WriteList(iter, listViewerOptions(opts), width, height, outputDiagnosticsAllowed(opts.Context))
		})
	}
	return true, models.WriteList(iter, listViewerOptions(opts), width, height, outputDiagnosticsAllowed(opts.Context))
}
