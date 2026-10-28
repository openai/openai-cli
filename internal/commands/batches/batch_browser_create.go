package batches

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type batchBrowserDraft struct {
	path, inputID, inputName, endpoint, metadata, folder, blocked string
	expires                                                       int64
}

func newBatchBrowserDraft() *batchBrowserDraft {
	directory, _ := os.Getwd()
	return &batchBrowserDraft{endpoint: string(openai.BatchNewParamsEndpointV1Responses), folder: directory}
}

func batchBrowserEdit(ctx context.Context, title, help, value string, show func(context.Context, *batchBrowserScreen) error) (string, bool, error) {
	m := newBatchBrowserFlow("entry", title, help, nil)
	m.input.Prompt = "Value: "
	m.input.SetValue(value)
	if err := show(ctx, m); err != nil {
		return value, false, err
	}
	return m.input.Value(), m.chosen != "back", nil
}

func batchBrowserEndpoints() []batchBrowserChoice {
	var choices []batchBrowserChoice
	for _, endpoint := range []openai.BatchNewParamsEndpoint{
		openai.BatchNewParamsEndpointV1Responses, openai.BatchNewParamsEndpointV1ChatCompletions,
		openai.BatchNewParamsEndpointV1Embeddings, openai.BatchNewParamsEndpointV1Completions,
		openai.BatchNewParamsEndpointV1Moderations, openai.BatchNewParamsEndpointV1ImagesGenerations,
		openai.BatchNewParamsEndpointV1ImagesEdits, openai.BatchNewParamsEndpointV1Videos,
	} {
		choices = append(choices, batchBrowserChoice{value: string(endpoint), title: string(endpoint)})
	}
	return choices
}

type batchBrowserMetadataString string

func (s *batchBrowserMetadataString) UnmarshalJSON(data []byte) error {
	// A plain string accepts JSON null. Check every value, including repeated keys.
	if data[0] != '"' {
		return errors.New("expected a JSON string")
	}
	return json.Unmarshal(data, (*string)(s))
}

func (d *batchBrowserDraft) body() ([]byte, error) {
	fields := map[string]any{"endpoint": d.endpoint, "completion_window": "24h"}
	validEndpoint := false
	for _, choice := range batchBrowserEndpoints() {
		validEndpoint = validEndpoint || d.endpoint == choice.value
	}
	if !validEndpoint {
		return nil, batchError("Choose a supported endpoint.")
	}
	if strings.TrimSpace(d.metadata) != "" {
		var metadata map[string]batchBrowserMetadataString
		if err := json.Unmarshal([]byte(d.metadata), &metadata); err != nil || metadata == nil {
			return nil, batchError("Metadata must be a JSON object with text values, such as {\"scenario\":\"sentiment\"}.")
		}
		fields["metadata"] = metadata
	}
	if d.expires != 0 {
		if d.expires < 3600 || d.expires > 2592000 {
			return nil, batchError("Choose output expiration from 3600 to 2592000 seconds.")
		}
		fields["output_expires_after"] = map[string]any{"anchor": "created_at", "seconds": d.expires}
	}
	return json.Marshal(fields)
}

func (d *batchBrowserDraft) sourceLabel() string {
	if d.inputID != "" {
		if d.inputName != "" {
			return d.inputName + " (uploaded)"
		}
		return d.inputID + " (uploaded)"
	}
	if d.path != "" {
		return filepath.Base(d.path)
	}
	return "Choose a file"
}

func (w workflow) createBatchInBrowser(ctx context.Context, cmd *cli.Command, client openai.Client, options []option.RequestOption, draft *batchBrowserDraft, show func(context.Context, *batchBrowserScreen) error) (gjson.Result, error) {
	for {
		if draft.blocked != "" {
			m := newBatchBrowserFlow("menu", "Submission needs checking", draft.blocked, []batchBrowserChoice{
				{value: "back", title: "Return to batches"},
				{value: "reset", title: "Start a new draft", detail: "Only continue after checking whether the previous submission succeeded."},
			})
			if err := show(ctx, m); err != nil {
				return gjson.Result{}, err
			}
			if m.chosen != "reset" {
				return gjson.Result{}, nil
			}
			confirm := newBatchBrowserFlow("confirm", "Start a new draft", "", nil)
			confirm.flow.lines = []string{draft.blocked, "", "Confirm only after checking existing files and batches.", "This clears the draft. It does not delete remote files or batches."}
			if err := show(ctx, confirm); err != nil {
				return gjson.Result{}, err
			}
			if confirm.chosen == "confirm" {
				*draft = *newBatchBrowserDraft()
			}
			continue
		}
		source := draft.path
		if draft.inputID != "" {
			source = "Uploaded file: " + draft.inputID
		}
		m := newBatchBrowserFlow("menu", "Create batch", "Completion window: 24 hours", []batchBrowserChoice{
			{value: "input", title: "Input file    " + draft.sourceLabel(), detail: source},
			{value: "endpoint", title: "Endpoint      " + draft.endpoint, detail: "All requests in the file must use this endpoint."},
			{value: "advanced", title: "Advanced", detail: "Metadata and output expiration"},
			{value: "review", title: "Review and create", detail: "Review the file and settings before submitting."},
		})
		if err := show(ctx, m); err != nil {
			return gjson.Result{}, err
		}
		switch m.chosen {
		case "back":
			return gjson.Result{}, nil
		case "input":
			sources := newBatchBrowserFlow("menu", "Choose input", "Each line in the JSONL file contains one API request.", []batchBrowserChoice{
				{value: "local", title: "Browse local files", detail: "Choose a JSONL file from this computer."},
				{value: "uploaded", title: "Choose an uploaded file", detail: "Reuse a file already uploaded for batches."},
			})
			if err := show(ctx, sources); err != nil {
				return gjson.Result{}, err
			}
			if sources.chosen == "local" {
				path, err := chooseBatchBrowserLocalFile(ctx, draft.folder, show)
				if err != nil {
					return gjson.Result{}, err
				}
				if path != "" {
					draft.path, draft.inputID, draft.inputName = path, "", ""
					draft.folder = filepath.Dir(path)
				}
			} else if sources.chosen == "uploaded" {
				id, name, err := chooseBatchBrowserUploadedFile(ctx, client, options, show)
				if err != nil {
					return gjson.Result{}, err
				}
				if id != "" {
					draft.inputID, draft.inputName, draft.path = id, name, ""
				}
			}
		case "endpoint":
			choices := newBatchBrowserFlow("menu", "Endpoint", "Match the request URLs in your input file.", batchBrowserEndpoints())
			for i, choice := range choices.choices {
				if choice.value == draft.endpoint {
					choices.index = i
				}
			}
			if err := show(ctx, choices); err != nil {
				return gjson.Result{}, err
			}
			if choices.chosen != "back" {
				draft.endpoint = choices.chosen
			}
		case "advanced":
			if err := editBatchBrowserAdvanced(ctx, draft, show); err != nil {
				return gjson.Result{}, err
			}
		case "review":
			body, err := draft.body()
			if err != nil {
				return gjson.Result{}, err
			}
			headers, err := requestconfig.Headers(cmd)
			if err != nil {
				return gjson.Result{}, err
			}
			var snapshot *batchSubmissionSnapshot
			if draft.path != "" {
				snapshot, err = batchBrowserWork(ctx, "Preparing input file", show, func(workCtx context.Context) (*batchSubmissionSnapshot, error) {
					return prepareBatchSubmissionSnapshot(workCtx, draft.path)
				})
				if err != nil {
					if snapshot != nil {
						err = errors.Join(err, snapshot.close())
					}
					return gjson.Result{}, err
				}
			} else if draft.inputID == "" {
				return gjson.Result{}, batchError("Choose an input file before reviewing this batch.")
			}
			state, confirmed, err := func() (state batchSubmission, confirmed bool, err error) {
				var reviewed os.FileInfo
				if snapshot != nil {
					defer func() { err = errors.Join(err, snapshot.close()) }()
					reviewed = snapshot
				}
				review := newBatchBrowserFlow("confirm", "Review batch", "Ctrl+S creates one batch. Esc returns to editing.", nil)
				review.flow.lines = batchBrowserReview(cmd, draft, reviewed)
				if err := show(ctx, review); err != nil {
					return state, false, err
				}
				if review.chosen != "confirm" {
					return state, false, nil
				}
				opts := append(options[:len(options):len(options)], option.WithRequestBody("application/json", body))
				for name, values := range headers {
					for _, value := range values {
						opts = append(opts, option.WithHeader(name, value))
					}
				}
				path, inputID := draft.path, draft.inputID
				state, err = batchBrowserWork(ctx, "Creating batch", show, func(workCtx context.Context) (batchSubmission, error) {
					return submitBatchRequest(workCtx, client, opts, headers, path, inputID, snapshot)
				})
				return state, true, err
			}()
			if !confirmed {
				if err != nil {
					return gjson.Result{}, err
				}
				continue
			}
			if state.InputFileID != "" {
				draft.inputID, draft.path = state.InputFileID, ""
			}
			if state.Batch != nil && state.Batch.ID != "" {
				batch := gjson.Parse(state.Batch.RawJSON())
				*draft = *newBatchBrowserDraft()
				if err != nil {
					return batch, w.browserSubmissionError(cmd, state, err)
				}
				return batch, nil
			}
			if err != nil {
				if state.Uncertain {
					draft.blocked = "The submission may have succeeded. Check existing batches before creating another batch."
					if state.Stage == "upload" {
						draft.blocked = "The upload may have succeeded. Check uploaded files before uploading it again."
					}
				}
				return gjson.Result{}, w.browserSubmissionError(cmd, state, err)
			}
		}
	}
}

func editBatchBrowserAdvanced(ctx context.Context, draft *batchBrowserDraft, show func(context.Context, *batchBrowserScreen) error) error {
	for {
		expiration := "Server default"
		if draft.expires != 0 {
			expiration = fmt.Sprintf("%d seconds after file creation", draft.expires)
		}
		m := newBatchBrowserFlow("menu", "Advanced settings", "Optional settings", []batchBrowserChoice{
			{value: "metadata", title: "Metadata", detail: draft.metadata},
			{value: "expires", title: "Output expiration", detail: expiration},
		})
		if err := show(ctx, m); err != nil {
			return err
		}
		if m.chosen == "back" {
			return nil
		}
		if m.chosen == "metadata" {
			value, apply, err := batchBrowserEdit(ctx, "Metadata", "Optional JSON object. Leave empty to omit.", draft.metadata, show)
			if err != nil {
				return err
			}
			if apply {
				draft.metadata = value
			}
		} else if m.chosen == "expires" {
			choices := newBatchBrowserFlow("menu", "Output expiration", "Time after the output/error file is created.", []batchBrowserChoice{
				{value: "0", title: "Server default"}, {value: "3600", title: "1 hour"}, {value: "86400", title: "1 day"},
				{value: "604800", title: "7 days"}, {value: "2592000", title: "30 days"}, {value: "custom", title: "Custom duration"},
			})
			if err := show(ctx, choices); err != nil {
				return err
			}
			value := choices.chosen
			if value == "back" {
				continue
			}
			if value == "custom" {
				var apply bool
				var err error
				value, apply, err = batchBrowserEdit(ctx, "Custom expiration", "Seconds, from 3600 (1 hour) to 2592000 (30 days).", strconv.FormatInt(draft.expires, 10), show)
				if err != nil {
					return err
				}
				if !apply {
					continue
				}
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n != 0 && (n < 3600 || n > 2592000) {
				return batchError("Choose output expiration from 3600 to 2592000 seconds.")
			}
			draft.expires = n
		}
	}
}

func batchBrowserReview(cmd *cli.Command, draft *batchBrowserDraft, snapshot os.FileInfo) []string {
	root := cmd.Root()
	project := root.String("project")
	if !root.IsSet("project") && project == "" {
		project = os.Getenv("OPENAI_PROJECT_ID")
		for line := range strings.SplitSeq(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
			if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "OpenAI-Project") {
				project = strings.TrimSpace(value)
			}
		}
	}
	if headers, err := requestconfig.Headers(cmd); err == nil {
		if values, ok := headers["Openai-Project"]; ok && len(values) > 0 {
			project = values[len(values)-1]
		}
	}
	if project == "" {
		project = "Key's default project"
	}
	base := cmd.String("base-url")
	if base == "" {
		base = os.Getenv("OPENAI_BASE_URL")
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	connection := "Configured connection"
	if parsed, err := url.Parse(base); err == nil && parsed.Host != "" {
		connection = parsed.Scheme + "://" + parsed.Host
	}
	lines := []string{"Project: " + project, "Connection: " + connection, ""}
	args := []string{"batches", "create", "--input-file-id", draft.inputID}
	if draft.path != "" {
		lines = append(lines, "Input file: "+draft.path)
		if snapshot != nil {
			lines = append(lines, fmt.Sprintf("Size: %d bytes", snapshot.Size()))
		}
		args = []string{"batches", "submit", "--file", draft.path}
	} else {
		lines = append(lines, "Uploaded file: "+draft.inputID)
	}
	lines = append(lines, "Endpoint: "+draft.endpoint, "Completion window: 24 hours")
	args = append(args, "--endpoint", draft.endpoint, "--completion-window", "24h")
	if draft.expires == 0 {
		lines = append(lines, "Output expiration: Server default")
	} else {
		lines = append(lines, fmt.Sprintf("Output expiration: %d seconds after file creation", draft.expires))
		args = append(args, "--output-expires-after", fmt.Sprintf(`{"anchor":"created_at","seconds":%d}`, draft.expires))
	}
	if strings.TrimSpace(draft.metadata) != "" {
		lines = append(lines, "Metadata: "+draft.metadata)
		args = append(args, "--metadata", draft.metadata)
	}
	lines = append(lines, "", "Creating a batch can incur API charges.", "Leaving the browser does not cancel the remote batch.")
	if shell := filepath.Base(os.Getenv("SHELL")); terminal.ShellQuoter(shell) != nil {
		lines = append(lines, "", "Command arguments (reuse this session's connection settings):", terminal.FormatCommand(args, shell))
	}
	return lines
}

func (w workflow) browserSubmissionError(cmd *cli.Command, state batchSubmission, err error) error {
	message := "No batch was created. Review the input and try again."
	if state.Stage == "upload" {
		message = "Upload did not finish cleanly. No batch creation was attempted. Check uploaded files before trying again."
	}
	if state.Stage == "create" {
		message = "Batch creation is unconfirmed. Check the batch list before creating another batch."
	}
	if state.Rejected {
		message = "The API rejected access. Correct the key or project permissions, then review the draft again."
	}
	if state.InputFileID != "" {
		message += "\nUploaded input file: " + state.InputFileID + "."
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			message += " Check batches list before manually reusing this file with batches create."
		} else {
			message += " The draft will reuse this file."
		}
	}
	if state.batchID() != "" {
		message = "Batch created: " + state.batchID() + ". Reopen this batch; do not submit it again."
	}
	var apierr *openai.Error
	var local *WorkflowError
	var input *requestconfig.InputFileError
	if errors.As(err, &input) {
		message += "\nCould not " + input.Operation + " the input file. Check the path and file permissions."
	} else if errors.As(err, &apierr) && w.options.APIErrorMessage != nil {
		message += "\n" + w.options.APIErrorMessage(cmd.Root(), err, apierr)
	} else if errors.As(err, &local) {
		message += "\n" + local.Error()
	}
	return &WorkflowError{message: message, cause: err, submission: &submissionRecovery{Stage: state.Stage, InputFileID: state.InputFileID, BatchID: state.batchID(), Message: message}}
}
