package batches

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	requestconfig "github.com/openai/openai-cli/internal/request"
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
	command.Description = "Uploads --file with purpose=batch, then creates one batch. Request fields accept the same flags and JSON/YAML input as create. Use create --input-file-id for an existing file. Mutations never retry automatically."
	command.Flags = nil
	for _, flag := range create.Flags {
		if flag.Names()[0] != "input-file-id" {
			command.Flags = append(command.Flags, flag)
		}
	}
	command.Flags = append(command.Flags, &cli.StringFlag{Name: "file", Required: true, Usage: "Local JSONL path; stdin is reserved for request fields"})
	command.Metadata["help-content"] = clihelp.Content{
		Description: command.Description,
		Examples:    []clihelp.Example{{Description: "Upload and submit requests:", Command: "batches submit --file requests.jsonl --endpoint /v1/responses --completion-window 24h"}},
	}
	command.Action = w.submitBatch
	return command
}

func (w workflow) submitBatch(ctx context.Context, cmd *cli.Command) (err error) {
	if cmd.Args().Len() != 0 {
		return batchError("Use --file with a local JSONL path. Unexpected positional arguments were not submitted.")
	}
	path := cmd.String("file")
	if path == "" || path == "-" || path == "/dev/stdin" || path == "/dev/fd/0" {
		return batchError("Use a local JSONL path with --file. For stdin, use files upload, then batches create.")
	}
	ctx, stop := batchSignalContext(ctx)
	defer stop()
	stopReset := context.AfterFunc(ctx, stop)
	defer stopReset()
	stage, inputID, batchID := "preparation", "", ""
	defer func() {
		if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), context.Cause(ctx), err)
		}
		if err == nil {
			return
		}
		if stage == "preparation" {
			err = batchContextError(err)
			return
		}
		message := "File upload did not finish cleanly. No batch creation was attempted.\nThe file may exist. Check files list before repeating submit."
		if inputID != "" {
			message = "Uploaded input file: " + strconv.Quote(inputID) + ".\nBatch creation is unconfirmed. The batch may already exist.\nCheck batches list with the same connection and project settings before creating another batch.\nIf no batch exists, reuse this input file with batches create --input-file-id."
		}
		if batchID != "" {
			message = "Batch created: " + strconv.Quote(batchID) + ".\nCould not display its receipt. Retrieve this batch with the same connection and project settings.\nDo not repeat submit."
		}
		var apierr *openai.Error
		if errors.As(err, &apierr) && !errors.Is(err, context.Canceled) {
			message += "\n" + w.options.APIErrorMessage(cmd.Root(), err, apierr)
		}
		code := 1
		var interruption *WorkflowError
		if errors.As(batchContextError(err), &interruption) {
			code = interruption.ExitCode()
		}
		err = &WorkflowError{message: message, cause: err, code: code,
			submission: &submissionRecovery{Stage: stage, InputFileID: inputID, BatchID: batchID, Message: message}}
	}()
	options, body, err := w.options.CreateRequestOptions(cmd, batchRequestInput{ctx: ctx, file: os.Stdin})
	if err != nil {
		return err
	}
	if !body.IsObject() || body.Get("input_file_id").Exists() {
		return batchError("Submit uses the uploaded file. Remove input_file_id from request input, or use batches create.")
	}
	if endpoint := body.Get("endpoint"); endpoint.Type != gjson.String || endpoint.String() == "" {
		return batchError("Set --endpoint before uploading the input file.")
	}
	if window := body.Get("completion_window"); window.Type != gjson.String || window.String() != "24h" {
		return batchError("Set --completion-window 24h before uploading the input file.")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	upload, err := openBatchSubmissionFile(path)
	if err != nil {
		return err
	}
	defer upload.Close()
	var multipart io.Closer
	uploadOptions, err := requestconfig.MultipartRequestOptions(map[string]any{"file": upload, "purpose": "batch"}, apiform.FormatBrackets, func(body io.Closer) { multipart = body })
	if err != nil {
		return err
	}
	defer multipart.Close()
	uploadOptions = append(append([]option.RequestOption(nil), options...), uploadOptions...)
	// Replacing the JSON body must not replace explicit literal HTTP headers.
	headers, err := requestconfig.Headers(cmd)
	if err != nil {
		return err
	}
	for name, values := range headers {
		for _, value := range values {
			uploadOptions = append(uploadOptions, option.WithHeader(name, value))
		}
	}
	uploadOptions = append(uploadOptions, option.WithMaxRetries(0))
	client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
	stage = "upload"
	file, err := client.Files.New(ctx, openai.FileNewParams{}, uploadOptions...)
	if err != nil {
		return err
	}
	if file == nil || file.ID == "" {
		return batchError("The upload response did not contain a file ID.")
	}
	inputID = file.ID
	stage = "create"
	options = append(options, option.WithJSONSet("input_file_id", inputID), option.WithMaxRetries(0))
	batch, err := client.Batches.New(ctx, openai.BatchNewParams{}, options...)
	if err != nil {
		return err
	}
	if batch == nil || batch.ID == "" {
		return fmt.Errorf("batch creation response did not contain a batch ID")
	}
	batchID = batch.ID
	stage = "receipt"
	ctx = context.WithValue(ctx, batchCreateContextKey{}, cmd)
	return w.batchShowJSON(ctx, cmd, gjson.Parse(batch.RawJSON()), "create")
}

func openBatchSubmissionFile(path string) (requestconfig.FileUpload, error) {
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
	upload, err := requestconfig.FileUploadFromFile(file)
	if err != nil {
		return requestconfig.FileUpload{}, source.Failure("open", err)
	}
	upload.Reader = &requestconfig.InputFileReader{Reader: upload.Reader, Source: source}
	return upload, nil
}
