package batches

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type submissionRecovery struct {
	Stage       string `json:"stage"`
	InputFileID string `json:"input_file_id,omitempty"`
	BatchID     string `json:"batch_id,omitempty"`
	Message     string `json:"message"`
}

// SubmissionErrorValue retains recovery data alongside the original API error.
// Only the new submit workflow adds this envelope; existing commands stay unchanged.
func SubmissionErrorValue(err error, value gjson.Result) gjson.Result {
	var workflow *WorkflowError
	if !errors.As(err, &workflow) || workflow.submission == nil {
		return value
	}
	data, _ := json.Marshal(struct {
		Error    json.RawMessage     `json:"error"`
		Recovery *submissionRecovery `json:"batch_submission"`
	}{json.RawMessage(value.Raw), workflow.submission})
	return gjson.ParseBytes(data)
}

func (w workflow) newBatchSubmitCommand(create *cli.Command) *cli.Command {
	command := localcommands.CloneResourceCommand(create)
	command.Name = "submit"
	command.Aliases = nil
	command.Usage = "Upload a local JSONL file and create one batch"
	command.Description = "Uploads an existing JSONL request file and creates one batch. Prepare the request file first; this command does not write requests. Supply FILE or --file. When omitted, endpoint is inferred from the request URLs and completion-window defaults to 24h. Add --wait to follow completion, with --output-dir to save results. Request fields retain the same flags and JSON/YAML input as create. Explicit values override defaults. Use create --input-file-id for an existing file. Mutations never retry automatically."
	command.ArgsUsage = "[FILE]"
	command.Flags = nil
	for _, flag := range create.Flags {
		if field, ok := flag.(*requestflag.Flag[string]); ok && (field.Name == "endpoint" || field.Name == "completion-window") {
			copy := *field
			copy.Required = false
			if field.Name == "endpoint" {
				copy.Usage = "Endpoint for all requests; infer from the JSONL file when omitted"
			} else {
				copy.Usage = "Processing window; defaults to 24h when omitted"
			}
			flag = &copy
		}
		if flag.Names()[0] != "input-file-id" {
			command.Flags = append(command.Flags, flag)
		}
	}
	command.Flags = append(command.Flags,
		&cli.StringFlag{Name: "file", TakesFile: true, Usage: "Local JSONL path, as an alternative to FILE; stdin is reserved for request fields"},
		&cli.BoolFlag{Name: "wait", Usage: "Wait for completion; Ctrl+C stops local waiting, not the remote batch"},
		&cli.DurationFlag{Name: "poll-interval", Value: 10 * time.Second, Usage: "Delay between successful status checks with --wait"},
		&cli.DurationFlag{Name: "wait-timeout", Usage: "Local wait duration after creation, including downloads; 0 waits without a deadline"},
		&cli.StringFlag{Name: "output-dir", TakesFile: true, Usage: "With --wait, save available result files without overwriting files"},
	)
	command.Metadata["help-content"] = clihelp.Content{
		Description: command.Description,
		Examples: []clihelp.Example{
			{Description: "Upload the file and start a batch:", Command: "batches submit requests.jsonl"},
			{Description: "Upload, start, wait, and save results:", Command: "batches submit requests.jsonl --wait --output-dir ./results"},
		},
	}
	command.Action = w.submitBatch
	return command
}

func (w workflow) submitBatch(ctx context.Context, cmd *cli.Command) (err error) {
	path := cmd.String("file")
	if cmd.Args().Len() > 1 || cmd.Args().Len() == 1 && cmd.IsSet("file") {
		return batchError("Choose one input file: use FILE or --file, not both. No upload was attempted.")
	}
	if cmd.Args().Len() == 1 {
		path = cmd.Args().First()
	}
	if path == "" || path == "-" || path == "/dev/stdin" || path == "/dev/fd/0" {
		return batchError("Choose a local JSONL file: openai batches submit requests.jsonl. For stdin, use files upload, then batches create.")
	}
	if !cmd.Bool("wait") && (cmd.IsSet("output-dir") || cmd.IsSet("poll-interval") || cmd.IsSet("wait-timeout")) {
		return batchError("Add --wait to use --output-dir, --poll-interval, or --wait-timeout. No upload was attempted.")
	}
	if cmd.Duration("poll-interval") <= 0 || cmd.Duration("wait-timeout") < 0 {
		return batchError("--poll-interval must be positive, and --wait-timeout must be zero or positive. No upload was attempted.")
	}
	ctx, stop := batchSignalContext(ctx)
	defer stop()
	stopReset := context.AfterFunc(ctx, stop)
	defer stopReset()
	submission := batchSubmission{Stage: "preparation"}
	var waitState batchWaitState
	defer func() {
		if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), context.Cause(ctx), err)
		}
		if err == nil {
			return
		}
		if submission.Stage == "preparation" {
			err = batchContextError(err)
			return
		}
		var apierr *openai.Error
		isAPI := errors.As(err, &apierr)
		accessRejected := batchSubmissionAccessRejected(err)
		message := "File upload did not finish cleanly. No batch creation was attempted.\nThe file may exist. Check files list before repeating submit."
		if accessRejected {
			message = "File upload was rejected. No batch creation was attempted."
		}
		if submission.InputFileID != "" {
			message = "Uploaded input file: " + strconv.Quote(submission.InputFileID) + ".\nBatch creation is unconfirmed. The batch may already exist.\nCheck batches list with the same connection and project settings before creating another batch.\nIf no batch exists, reuse this input file with batches create --input-file-id."
			if accessRejected {
				message = "Uploaded input file: " + strconv.Quote(submission.InputFileID) + ".\nBatch creation was rejected.\nAfter correcting access, reuse this input file with batches create --input-file-id.\nReuse the same connection and project settings.\nDo not repeat submit."
			}
		}
		if submission.Batch != nil {
			message = "Batch created: " + strconv.Quote(submission.Batch.ID) + ".\nCould not display its receipt. Retrieve this batch with the same connection and project settings.\nDo not repeat submit."
			if submission.Stage == "wait" {
				message = "Batch created: " + strconv.Quote(submission.Batch.ID) + ".\nLocal waiting or result collection did not finish successfully.\nResume with batches wait using this batch ID and the same connection and project settings.\nChoose a new --output-dir if result files already exist. Do not repeat submit."
				if waitState.Stopped {
					message = "Batch created: " + strconv.Quote(submission.Batch.ID) + ".\nLast reported status: " + strconv.Quote(waitState.Status) + ".\nInspect status and validation errors with batches retrieve.\nRead available results with batches results; add --file error for request failures.\nKeep saved files. Use batches download only for files still needed.\nReuse this batch ID and the same connection and project settings.\nDo not repeat submit."
				}
			}
		}
		var detail *WorkflowError
		if waitState.Stopped && errors.Is(err, context.DeadlineExceeded) {
			message += "\nThe local operation timed out after receiving this status."
		} else if submission.Stage == "wait" && errors.As(err, &detail) && detail.Error() != "" && !errors.Is(err, context.Canceled) {
			message += "\n" + detail.Error()
		} else if isAPI && !errors.Is(err, context.Canceled) {
			message += "\n" + w.options.APIErrorMessage(cmd.Root(), err, apierr)
		} else if !errors.Is(err, context.Canceled) {
			var local *WorkflowError
			if errors.As(err, &local) && local.Error() != "" {
				message += "\n" + local.Error()
			}
		}
		code := 1
		var interruption *WorkflowError
		if errors.As(batchContextError(err), &interruption) {
			code = interruption.ExitCode()
		}
		err = &WorkflowError{message: message, cause: err, code: code,
			submission: &submissionRecovery{Stage: submission.Stage, InputFileID: submission.InputFileID, BatchID: submission.batchID(), Message: message}}
	}()
	options, body, err := w.options.CreateRequestOptions(cmd, batchRequestInput{ctx: ctx, file: os.Stdin})
	if err != nil {
		return err
	}
	if !body.IsObject() || body.Get("input_file_id").Exists() {
		return batchError("Submit uses the uploaded file. Remove input_file_id from request input, or use batches create.")
	}
	if !body.Get("completion_window").Exists() {
		options = append(options, option.WithJSONSet("completion_window", "24h"))
	} else if window := body.Get("completion_window"); window.Type != gjson.String || window.String() != "24h" {
		return batchError("Use --completion-window 24h. No upload was attempted.")
	}
	var snapshot os.FileInfo
	if !body.Get("endpoint").Exists() {
		var endpoint string
		endpoint, snapshot, err = inferBatchSubmitEndpoint(ctx, path)
		if err != nil {
			return err
		}
		options = append(options, option.WithJSONSet("endpoint", endpoint))
	} else if endpoint := body.Get("endpoint"); endpoint.Type != gjson.String || endpoint.String() == "" {
		return batchError("Set a nonempty --endpoint, or omit it to infer the endpoint from the JSONL file. No upload was attempted.")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	headers, err := requestconfig.Headers(cmd)
	if err != nil {
		return err
	}
	var destination *batchOutputDirectory
	var readOptions []option.RequestOption
	if cmd.Bool("wait") {
		// Preserve inherited connection, literal headers, and request logging.
		// Exclude creation fields and use an empty reader, never stdin again.
		readCommand := *cmd
		readCommand.Flags = nil
		readOptions, err = w.options.RequestOptions(&readCommand, strings.NewReader(""))
		if err != nil {
			return err
		}
		if cmd.IsSet("output-dir") {
			destination, err = prepareBatchOutputDirectory(ctx, cmd.String("output-dir"))
			if err != nil {
				return err
			}
		}
	}
	client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
	submission, err = submitBatchRequest(ctx, client, options, headers, path, "", snapshot)
	if err != nil {
		return err
	}
	if cmd.Bool("wait") {
		submission.Stage = "wait"
		waitCtx := ctx
		if timeout := cmd.Duration("wait-timeout"); timeout > 0 {
			var cancel context.CancelFunc
			waitCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		if w.options.HumanTerminal(waitCtx, cmd.Root()) {
			if err := w.batchCollectionReceipt(waitCtx, cmd, "Batch created: "+strconv.Quote(submission.Batch.ID)+". Waiting for completion."); err != nil {
				return err
			}
		}
		waitState, err = w.waitForPreparedBatch(waitCtx, cmd, readOptions, submission.Batch.ID, cmd.Duration("poll-interval"), destination)
		if waitCtx.Err() != nil {
			return batchContextError(errors.Join(waitCtx.Err(), context.Cause(waitCtx), err))
		}
		return err
	}
	ctx = context.WithValue(ctx, batchCreateContextKey{}, cmd)
	return w.batchShowJSON(ctx, cmd, gjson.Parse(submission.Batch.RawJSON()), "create")
}

func openBatchSubmissionFile(path string, reviewedFile os.FileInfo) (requestconfig.FileUpload, error) {
	source := requestconfig.FileInputSource{Option: "--file"}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return requestconfig.FileUpload{}, source.Failure("open", err)
	}
	info, err := file.Stat()
	if err != nil {
		return requestconfig.FileUpload{}, source.Failure("open", errors.Join(err, file.Close()))
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return requestconfig.FileUpload{}, batchError("Submit requires a regular local JSONL file. Use files upload for other input sources.")
	}
	if reviewedFile != nil && (!os.SameFile(reviewedFile, info) || reviewedFile.Size() != info.Size() || !reviewedFile.ModTime().Equal(info.ModTime())) {
		_ = file.Close()
		return requestconfig.FileUpload{}, batchError("The input file changed after review. Review the file again before creating the batch.")
	}
	upload, err := requestconfig.FileUploadFromFile(file)
	if err != nil {
		return requestconfig.FileUpload{}, source.Failure("open", err)
	}
	upload.Reader = &requestconfig.InputFileReader{Reader: upload.Reader, Source: source}
	return upload, nil
}
