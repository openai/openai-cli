package batches

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
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
	endpointSet                                                   bool
	csv                                                           *batchBrowserCSV
}

func newBatchBrowserDraft() *batchBrowserDraft {
	directory, _ := os.Getwd()
	return &batchBrowserDraft{endpoint: string(openai.BatchNewParamsEndpointV1Responses), folder: directory}
}

func batchBrowserEndpoints() []batchBrowserChoice {
	var choices []batchBrowserChoice
	for _, endpoint := range batchSubmitEndpoints {
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

func parseBatchBrowserMetadata(value string) (map[string]batchBrowserMetadataString, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var metadata map[string]batchBrowserMetadataString
	if err := json.Unmarshal([]byte(value), &metadata); err != nil || metadata == nil {
		return nil, batchError(`Metadata must be a JSON object with text values, such as {"scenario":"sentiment"}.`)
	}
	return metadata, nil
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
	metadata, err := parseBatchBrowserMetadata(d.metadata)
	if err != nil {
		return nil, err
	}
	if metadata != nil {
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
	if d.csv != nil && d.inputID == "" {
		return filepath.Base(d.csv.path) + " (CSV)"
	}
	if d.inputID != "" {
		if d.inputName != "" {
			return d.inputName + " (uploaded)"
		}
		return d.inputID + " (uploaded)"
	}
	if d.path != "" {
		return filepath.Base(d.path)
	}
	return "Browse or type a file path"
}

func (w workflow) createBatchInBrowser(ctx context.Context, cmd *cli.Command, client openai.Client, options []option.RequestOption, draft *batchBrowserDraft, show func(context.Context, *batchBrowserScreen) error) (gjson.Result, error) {
	notice := ""
	reviewNext := false
	focus := "input"
	pathEntry := ""
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
		settings := batchBrowserChoice{value: "endpoint", title: "Endpoint", setting: draft.endpoint, detail: "All requests in the batch use one endpoint."}
		if draft.csv != nil {
			settings = batchBrowserChoice{value: "csv-settings", title: "CSV setup", setting: draft.csv.column, detail: "Edit the input column, task, or model."}
		} else if !draft.endpointSet && draft.inputID == "" {
			settings.setting = "From JSONL file"
		}
		choices := []batchBrowserChoice{{value: "input", title: "Input file", setting: draft.sourceLabel(), detail: source}}
		if draft.path != "" || draft.inputID != "" {
			choices = append(choices, batchBrowserChoice{value: "review", title: "Review and create"})
		}
		choices = append(choices, settings,
			batchBrowserChoice{value: "metadata", title: "Metadata", detail: `Optional JSON object, such as {"scenario":"sentiment"}.`},
			batchBrowserChoice{value: "expires", title: "Output expiration", detail: "When output and error files expire after creation."},
		)
		m := newBatchBrowserFlow("create", "Create batch", "Completion window: 24 hours\nFolder: "+batchBrowserFolderLabel(draft.folder), choices)
		for i, row := range choices {
			if row.value == focus {
				m.index = i
			}
		}
		m.flow.draft = draft
		m.updateSettingLabels()
		m.restoreFilePrompt()
		m.input.SetValue(pathEntry)
		pathEntry = ""
		m.notice, notice = notice, ""
		if reviewNext {
			m.chosen = "review"
			reviewNext = false
		} else if err := show(ctx, m); err != nil {
			return gjson.Result{}, err
		}
		switch m.chosen {
		case "back":
			return gjson.Result{}, nil
		case "input":
			focus = "input"
			var path string
			var err error
			if value := m.input.Value(); value != "" {
				var folder bool
				path, folder, err = batchBrowserTypedLocation(value, draft.folder)
				if err != nil {
					pathEntry, notice = value, err.Error()
					continue
				}
				if folder {
					draft.folder, path = path, ""
				}
			}
			for path == "" {
				var uploaded bool
				path, uploaded, err = browseBatchBrowserFolder(ctx, &draft.folder, "", show)
				if err != nil {
					return gjson.Result{}, err
				}
				if !uploaded {
					break
				}
				id, name, err := chooseBatchBrowserUploadedFile(ctx, client, options, show)
				if err != nil {
					return gjson.Result{}, err
				}
				if id != "" {
					draft.csv = nil
					draft.inputID, draft.inputName, draft.path = id, name, ""
					if !draft.endpointSet {
						draft.endpoint = string(openai.BatchNewParamsEndpointV1Responses)
					}
					reviewNext = true
					break
				}
			}
			if path == "" {
				continue
			}
			format := strings.ToLower(filepath.Ext(path))
			if format != ".csv" && format != ".jsonl" {
				kind := newBatchBrowserFlow("menu", "Choose file format", filepath.Base(path), []batchBrowserChoice{
					{value: ".jsonl", title: "JSONL requests", detail: "One prepared API request per line."},
					{value: ".csv", title: "CSV spreadsheet", detail: "Use one text column for the batch requests."},
				})
				if err := show(ctx, kind); err != nil {
					return gjson.Result{}, err
				}
				if kind.chosen == "back" {
					pathEntry = path
					continue
				}
				format = kind.chosen
			}
			if format == ".csv" {
				reviewNext, notice, err = editBatchBrowserCSV(ctx, draft, path, true, show)
				if err != nil {
					return gjson.Result{}, err
				}
			} else {
				draft.csv = nil
				draft.path, draft.inputID, draft.inputName = path, "", ""
				draft.folder = batchBrowserFileFolder(path)
				reviewNext = true
			}
		case "csv-settings":
			var err error
			reviewNext, notice, err = editBatchBrowserCSV(ctx, draft, draft.csv.path, false, show)
			if err != nil {
				return gjson.Result{}, err
			}
		case "endpoint":
			endpoints := batchBrowserEndpoints()
			current := draft.endpoint
			if draft.inputID == "" {
				endpoints = append([]batchBrowserChoice{{value: "auto", title: "From JSONL file"}}, endpoints...)
				if !draft.endpointSet {
					current = "auto"
				}
			}
			choices := newBatchBrowserFlow("menu", "Endpoint", "Match the request URLs in your input file.", endpoints)
			for i, choice := range choices.choices {
				if choice.value == current {
					choices.index = i
				}
			}
			if err := show(ctx, choices); err != nil {
				return gjson.Result{}, err
			}
			if choices.chosen == "auto" {
				draft.endpointSet = false
			} else if choices.chosen != "back" {
				draft.endpoint = choices.chosen
				draft.endpointSet = true
			}
			focus = "endpoint"
		case "review":
			_, err := parseBatchBrowserMetadata(draft.metadata)
			if err != nil {
				return gjson.Result{}, err
			}
			headers, err := requestconfig.Headers(cmd)
			if err != nil {
				return gjson.Result{}, err
			}
			var snapshot *batchSubmissionSnapshot
			var info batchJSONLInfo
			if draft.path != "" {
				snapshot, err = batchBrowserWork(ctx, "Preparing input file", show, func(workCtx context.Context) (*batchSubmissionSnapshot, error) {
					if draft.csv == nil {
						input, err := prepareBatchSubmissionSnapshot(workCtx, draft.path)
						if err != nil || draft.endpointSet {
							return input, err
						}
						reader := batchRecordContextReader{workCtx, io.NewSectionReader(input.file, 0, input.Size())}
						info, err = inspectBatchJSONL(workCtx, reader)
						if err != nil {
							if message := WorkflowErrorMessage(err); strings.HasPrefix(message, "Cannot determine the batch endpoint:") {
								notice = strings.Replace(message, "Check the request endpoint, or provide --endpoint explicitly for a custom endpoint.", "Check the request URL in the file.", 1)
								return nil, input.close()
							}
							return nil, errors.Join(err, input.close())
						}
						draft.endpoint = info.endpoint
						return input, nil
					}
					snapshot, e := draft.csv.prepare(workCtx)
					if msg := batchBrowserCSVNotice(e); msg != "" {
						notice = msg
						return nil, nil
					}
					return snapshot, e
				})
				if err != nil {
					if snapshot != nil {
						err = errors.Join(err, snapshot.close())
					}
					return gjson.Result{}, err
				}
				if snapshot == nil && notice != "" {
					focus = "input"
					continue
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
				body, err := draft.body()
				if err != nil {
					return state, false, err
				}
				review := newBatchBrowserFlow("confirm", "Review batch", "Enter submits one batch. Esc returns to editing.", nil)
				review.flow.submit = true
				review.flow.lines = batchBrowserReview(cmd, draft, reviewed, info.requests)
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
				focus = "review"
				continue
			}
			if state.InputFileID != "" {
				draft.inputID, draft.path = state.InputFileID, ""
			}
			if state.Batch != nil && state.Batch.ID != "" {
				batch := gjson.Parse(state.Batch.RawJSON())
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

func batchBrowserReview(cmd *cli.Command, draft *batchBrowserDraft, snapshot os.FileInfo, requests int) []string {
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
	var lines []string
	args := []string{"batches", "create", "--input-file-id", draft.inputID}
	if draft.path != "" {
		lines = append(lines, "Input file: "+filepath.Base(draft.path), "Folder: "+batchBrowserFolderLabel(batchBrowserFileFolder(draft.path)))
		if requests > 0 {
			lines = append(lines, fmt.Sprintf("Requests: %d", requests))
		}
		if snapshot != nil {
			if draft.csv != nil {
				lines = append(lines, fmt.Sprintf("Prepared JSONL: %d bytes", snapshot.Size()))
			} else {
				lines = append(lines, fmt.Sprintf("Size: %d bytes", snapshot.Size()))
			}
		}
		args = []string{"batches", "submit", "--file", draft.path}
	} else {
		lines = append(lines, "Uploaded file: "+draft.inputID)
	}
	if csv := draft.csv; csv != nil {
		lines = append(lines, fmt.Sprintf("Requests: %d", csv.preview.Count), "Input column: "+csv.column,
			"Task: "+csv.instructions, "Model: "+csv.model, "First input: "+csv.preview.FirstInput, "")
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
	lines = append(lines, "", "Project: "+project, "Connection: "+connection,
		"", "Creating a batch can incur API charges.", "Leaving the browser does not cancel the remote batch.")
	if shell := filepath.Base(os.Getenv("SHELL")); terminal.ShellQuoter(shell) != nil && (draft.csv == nil || draft.inputID != "") {
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
