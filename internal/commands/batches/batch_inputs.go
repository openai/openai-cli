package batches

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func (w workflow) newBatchInputsCommand() *cli.Command {
	command := &cli.Command{
		Name: "inputs", Usage: "Find uploaded Batch input files or select one",
		Description:     "Lists files with purpose=batch, newest first. Search matches literal filename or ID text. Selection returns File metadata without creating a batch. Use --format-error text with --select and machine output. Selection excludes --quiet, --debug, and --transform-error.",
		HideHelpCommand: true, Suggest: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "search", Usage: "Case-insensitive filename or file ID text"},
			&cli.Int64Flag{Name: "max-items", Value: 100, Usage: "Maximum matching files to return; -1 returns all matches"},
			&cli.BoolFlag{Name: "select", Usage: "Select one file interactively; requires terminal stdin and stderr"},
		},
		Action: w.listBatchInputs,
	}
	command.Metadata = map[string]any{"help-content": clihelp.Content{
		Description: command.Description,
		Examples:    []clihelp.Example{{Description: "Find uploaded input files:", Command: "batches inputs --search requests.jsonl"}},
	}}
	return command
}

func (w workflow) listBatchInputs(ctx context.Context, cmd *cli.Command) (err error) {
	if cmd.Args().Len() != 0 {
		return batchError("Unexpected extra arguments. Use --search to find Batch input files.")
	}
	maximum := cmd.Int64("max-items")
	if maximum < -1 {
		return batchError("--max-items must be -1, zero, or positive.")
	}
	if cmd.Bool("select") {
		root := cmd.Root()
		if !batchInputSelectionAllowed(root, term.IsTerminal(os.Stdin.Fd()), term.IsTerminal(os.Stderr.Fd())) {
			return batchError("File selection requires terminal stdin and stderr without --quiet, --debug, or --transform-error. Use --format-error text with machine output, or omit --select.")
		}
		if maximum == 0 {
			return batchError("Use a positive --max-items value or -1 with --select.")
		}
	}
	ctx, stop := batchSignalContext(ctx)
	defer stop()
	defer func() {
		if ctx.Err() != nil {
			err = batchContextError(errors.Join(ctx.Err(), context.Cause(ctx), err))
		}
	}()
	options, err := w.options.RequestOptions(cmd, batchRequestInput{ctx: ctx, file: os.Stdin})
	if err != nil {
		return err
	}
	client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
	store, err := newBatchRecordStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.close()) }()
	files := &batchInputCollection{store: store}
	search := strings.ToLower(cmd.String("search"))
	fetchErr := files.fetch(ctx, maximum, func(after string) ([]byte, error) {
		var raw []byte
		opts := append(options[:len(options):len(options)], option.WithQuery("purpose", "batch"), option.WithQuery("order", "desc"), option.WithQuery("limit", "100"), option.WithResponseBodyInto(&raw))
		if after != "" {
			opts = append(opts, option.WithQuery("after", after))
		} else {
			opts = append(opts, option.WithQueryDel("after"))
		}
		_, err := client.Files.List(ctx, openai.FileListParams{}, opts...)
		return raw, err
	}, func(file gjson.Result) bool {
		return file.Get("purpose").Str == "batch" && (search == "" || strings.Contains(strings.ToLower(file.Get("filename").Str), search) || strings.Contains(strings.ToLower(file.Get("id").Str), search))
	})
	if cmd.Bool("select") {
		if fetchErr != nil {
			return fetchErr
		}
		if store.recordCount == 0 {
			return batchError("No matching Batch input files were found. Change --search or upload a file with files upload --purpose batch.")
		}
		selected, err := runStoredBatchInputPicker(ctx, files)
		if err != nil {
			return err
		}
		return w.batchShowJSON(ctx, cmd, selected, "inputs")
	}
	if fetchErr != nil && store.recordCount == 0 {
		return fetchErr
	}
	if fetchErr != nil {
		message := "Input file listing stopped. Displayed files may be incomplete."
		var apierr *openai.Error
		var workflowErr *WorkflowError
		if errors.As(fetchErr, &apierr) && w.options.APIErrorMessage != nil {
			message += "\n" + w.options.APIErrorMessage(cmd.Root(), fetchErr, apierr)
		} else if errors.As(fetchErr, &workflowErr) {
			message += "\n" + workflowErr.Error()
		}
		message += "\nRetry batches inputs with the same search, connection, and project settings."
		fetchErr = &WorkflowError{message: message, cause: fetchErr}
	}
	diagnostics := terminal.BorrowedWriter{Writer: batchDiagnosticWriter{ctx: ctx, file: os.Stderr}, File: os.Stderr}
	var renderErr error
	outputErr := writeBatchStdoutWithCleanup(ctx, cmd.Root().Writer, batchOutputCleanupGrace, func(out io.Writer) error {
		renderErr = w.options.ShowRecords(ctx, cmd, RecordOutput{
			Operation: "inputs", WriteJSON: files.writeJSON, TemporaryFile: store.temporaryFile,
		}, out, diagnostics)
		// Preserve already rendered records when a later storage read fails.
		return nil
	})
	return errors.Join(fetchErr, renderErr, outputErr)
}

func batchInputSelectionAllowed(root *cli.Command, inputTerminal, errorTerminal bool) bool {
	format := root.String("format")
	if root.IsSet("format-error") {
		format = root.String("format-error")
	}
	format = strings.ToLower(format)
	return inputTerminal && errorTerminal && root.String("transform-error") == "" && !root.Bool("quiet") && !root.Bool("debug") && (format == "" || format == "auto" || format == "text")
}
