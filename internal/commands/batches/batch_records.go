package batches

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/openai/openai-cli/internal/terminal"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

func (w workflow) newBatchResultsCommand() *cli.Command {
	command := &cli.Command{
		Name: "results", Usage: "Inspect batch input, output, or error records",
		Description:     "Reads JSONL records without changing the batch. Records use private temporary storage. JSON and text output stream from that storage; transforms and explore/pretty/YAML output load the selected document into memory. Use --custom-id to select fewer records. Use --format explore for the JSON browser. Matching uses custom_id, never file order. Missing results can be pending or in the other result file. Duplicate IDs remain grouped without inventing pairs. Use batches download for exact file bytes.",
		HideHelpCommand: true, Suggest: true,
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: "batch-id", PathParam: "batch_id", Required: true},
			&cli.StringFlag{Name: "file", Value: "output", Usage: "Select output, error, or input records"},
			&cli.StringFlag{Name: "custom-id", Usage: "Show records with this exact custom_id"},
			&cli.BoolFlag{Name: "match-input", Usage: "Match output or error records to inputs; include unmatched inputs"},
		},
		Action: w.handleBatchResults,
	}
	command.Metadata = map[string]any{"help-content": clihelp.Content{
		Description: command.Description,
		Examples:    []clihelp.Example{{Description: "Inspect one result with its input:", Command: "batches results batch_example --custom-id request-1 --match-input --format explore"}},
	}}
	return command
}

func (w workflow) handleBatchResults(ctx context.Context, command *cli.Command) (err error) {
	selection := command.String("file")
	if selection != "output" && selection != "error" && selection != "input" {
		return batchError("Choose --file output, error, or input.")
	}
	if selection == "input" && command.Bool("match-input") {
		return batchError("Use --match-input with --file output or --file error.")
	}
	if command.IsSet("custom-id") && command.String("custom-id") == "" {
		return batchError("Choose a nonempty --custom-id, or omit it to inspect all records.")
	}
	ctx, stop := batchSignalContext(ctx)
	stopReset := context.AfterFunc(ctx, stop)
	defer func() {
		contextErr := errors.Join(ctx.Err(), context.Cause(ctx))
		stopReset()
		stop()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = &WorkflowError{message: "Reading batch records timed out. Retry batches results to inspect the same batch.", code: 124, cause: errors.Join(err, contextErr)}
		} else if contextErr != nil {
			err = batchContextError(errors.Join(err, contextErr))
		}
	}()
	options, err := w.batchRequestOptions(ctx, command)
	if err != nil {
		return err
	}
	client := openai.NewClient(w.options.DefaultRequestOptions(command)...)
	batch, err := client.Batches.Get(ctx, command.String("batch-id"), options...)
	if err != nil {
		return err
	}
	if batch == nil {
		return batchError("The API returned no batch object. Retrieve the batch again to check its file IDs.")
	}
	fileID := batch.OutputFileID
	if selection == "input" {
		fileID = batch.InputFileID
	} else if selection == "error" {
		fileID = batch.ErrorFileID
	}
	if fileID == "" {
		return batchError("The batch has no selected file available. Retrieve the batch to check its status and file IDs.")
	}
	if command.Bool("match-input") && batch.InputFileID == "" {
		return batchError("The batch has no input file available for matching. Omit --match-input to inspect results alone.")
	}
	store, err := newBatchRecordStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.close()) }()
	filter := command.String("custom-id")
	match := command.Bool("match-input")
	if err := readBatchRecordsFile(ctx, client, options, fileID, selection, filter, false, match, store); err != nil {
		return err
	}
	if match {
		if err := readBatchRecordsFile(ctx, client, options, batch.InputFileID, "input", filter, true, true, store); err != nil {
			return err
		}
	}
	diagnostics := terminal.BorrowedWriter{Writer: batchDiagnosticWriter{ctx: ctx, file: os.Stderr}, File: os.Stderr}
	return writeBatchStdoutWithCleanup(ctx, command.Root().Writer, batchOutputCleanupGrace, func(out io.Writer) error {
		return w.options.ShowRecords(ctx, command, RecordOutput{
			WriteJSON:     func(writer io.Writer) error { return store.writeJSON(writer, batch.ID, selection, fileID, match) },
			TemporaryFile: store.temporaryFile,
			BatchID:       batch.ID, File: selection, FileID: fileID,
			Visit: func(visit func(RecordItem) error) error { return store.visitRecords(selection, match, visit) },
		}, out, diagnostics)
	})
}

func readBatchRecordsFile(ctx context.Context, client openai.Client, options []option.RequestOption, fileID, selection, filter string, input, match bool, store *batchRecordStore) (err error) {
	response, err := client.Files.Content(ctx, fileID, options...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	err = store.readJSONL(response.Body, selection, filter, input, match)
	var workflowErr *WorkflowError
	if err != nil && !errors.As(err, &workflowErr) && ctx.Err() == nil {
		return &WorkflowError{message: "Could not read the batch file completely. Retry results or download the selected file.", cause: err}
	}
	return err
}

type batchRecordContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r batchRecordContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	// Limit work per cancellation check, not the accepted payload size.
	if len(data) > 32*1024 {
		data = data[:32*1024]
	}
	n, err := r.source.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		err = batchRecordStorageError(err)
	}
	return n, err
}
