package batches

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/openai/openai-cli/internal/apiform"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// batchSubmission retains confirmed identities when a later operation fails.
// Uncertain applies to a failed mutation, not a failed receipt after creation.
type batchSubmission struct {
	Stage               string
	InputFileID         string
	Batch               *openai.Batch
	Rejected, Uncertain bool
}

func (s batchSubmission) batchID() string {
	if s.Batch != nil {
		return s.Batch.ID
	}
	return ""
}

// submitBatchRequest performs only the reviewed mutation. The caller prepares
// request fields and connection options, then presents the returned state.
// Exactly one input source is required. Neither mutation retries automatically.
func submitBatchRequest(ctx context.Context, client openai.Client, options []option.RequestOption, literalHeaders http.Header, localPath, inputFileID string, reviewedFile os.FileInfo) (state batchSubmission, err error) {
	state.Stage = "preparation"
	defer func() {
		if err != nil && (state.Stage == "upload" || state.Stage == "create") {
			state.Rejected = batchSubmissionAccessRejected(err)
			state.Uncertain = !state.Rejected
		}
	}()
	if (localPath == "") == (inputFileID == "") {
		return state, batchError("Choose a local JSONL file or an uploaded input file, not both.")
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if localPath != "" {
		upload, err := openBatchSubmissionFile(localPath, reviewedFile)
		if err != nil {
			return state, err
		}
		defer upload.Close()
		var multipart io.Closer
		uploadOptions, err := requestconfig.MultipartRequestOptions(map[string]any{"file": upload, "purpose": "batch"}, apiform.FormatBrackets, func(body io.Closer) { multipart = body })
		if err != nil {
			return state, err
		}
		defer multipart.Close()
		uploadOptions = append(append([]option.RequestOption(nil), options...), uploadOptions...)
		// Replacing JSON must not replace explicit literal HTTP headers.
		for name, values := range literalHeaders {
			for _, value := range values {
				uploadOptions = append(uploadOptions, option.WithHeader(name, value))
			}
		}
		uploadOptions = append(uploadOptions, option.WithMaxRetries(0))
		state.Stage = "upload"
		file, err := client.Files.New(ctx, openai.FileNewParams{}, uploadOptions...)
		if err != nil {
			return state, err
		}
		if file == nil || file.ID == "" {
			return state, batchError("The upload response did not contain a file ID.")
		}
		inputFileID = file.ID
	}
	state.InputFileID = inputFileID
	state.Stage = "create"
	createOptions := append(options[:len(options):len(options)], option.WithJSONSet("input_file_id", inputFileID), option.WithMaxRetries(0))
	batch, err := client.Batches.New(ctx, openai.BatchNewParams{}, createOptions...)
	if err != nil {
		return state, err
	}
	if batch == nil || batch.ID == "" {
		return state, batchError("The batch creation response did not contain a batch ID.")
	}
	state.Batch = batch
	state.Stage = "receipt"
	return state, nil
}

func batchSubmissionAccessRejected(err error) bool {
	var apierr *openai.Error
	// A rejection after a redirect can follow a completed mutation.
	return errors.As(err, &apierr) &&
		(apierr.StatusCode == http.StatusUnauthorized || apierr.StatusCode == http.StatusForbidden) &&
		apierr.Response != nil && apierr.Response.Request != nil && apierr.Response.Request.Response == nil
}
