package batches

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func (w workflow) newBatchResultsCommand() *cli.Command {
	command := &cli.Command{
		Name: "results", Usage: "Inspect batch input, output, or error records",
		Description:     "Reads JSONL records without changing the batch. Use --format explore for the JSON browser. Matching uses custom_id, never file order. Missing results can be pending or in the other result file. Duplicate IDs remain grouped without inventing pairs. Use batches download for exact file bytes.",
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

type batchRecord struct {
	id  string
	raw json.RawMessage
}

type batchRecordMatch struct {
	CustomID string            `json:"custom_id,omitempty"`
	Match    string            `json:"match"`
	Issues   []string          `json:"issues,omitempty"`
	Inputs   []json.RawMessage `json:"inputs"`
	Results  []json.RawMessage `json:"results"`
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
	filter := command.String("custom-id")
	records, err := readBatchRecordsFile(ctx, client, options, fileID, selection, filter)
	if err != nil {
		return err
	}
	var data any
	if command.Bool("match-input") {
		inputs, err := readBatchRecordsFile(ctx, client, options, batch.InputFileID, "input", filter)
		if err != nil {
			return err
		}
		data, err = matchBatchRecords(ctx, inputs, records, filter)
		if err != nil {
			return err
		}
	} else {
		selected := make([]json.RawMessage, 0, len(records))
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return err
			}
			if filter == "" || record.id == filter {
				selected = append(selected, record.raw)
			}
		}
		data = selected
	}
	result, err := json.Marshal(struct {
		BatchID string `json:"batch_id"`
		File    string `json:"file"`
		FileID  string `json:"file_id"`
		Data    any    `json:"data"`
	}{batch.ID, selection, fileID, data})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.batchShowJSON(ctx, command, gjson.ParseBytes(result), "results")
}

func readBatchRecordsFile(ctx context.Context, client openai.Client, options []option.RequestOption, fileID, selection, filter string) (records []batchRecord, err error) {
	response, err := client.Files.Content(ctx, fileID, options...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	return readBatchRecords(ctx, response.Body, selection, filter)
}

// ReadSlice avoids Scanner's line limit. Check cancellation between chunks and
// while validating large JSON values; file downloads remain byte-preserving.
func readBatchRecords(ctx context.Context, source io.Reader, selection, filter string) ([]batchRecord, error) {
	reader := bufio.NewReader(source)
	records := make([]batchRecord, 0)
	for lineNumber := 1; ; lineNumber++ {
		var line []byte
		var readErr error
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var chunk []byte
			chunk, readErr = reader.ReadSlice('\n')
			line = append(line, chunk...)
			if !errors.Is(readErr, bufio.ErrBufferFull) {
				break
			}
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, &WorkflowError{message: "Could not read the batch file completely. Retry results or download the selected file.", cause: readErr}
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var object struct{}
			decoder := json.NewDecoder(batchRecordContextReader{ctx, bytes.NewReader(line)})
			parseErr := decoder.Decode(&object)
			var extra any
			if parseErr == nil && decoder.Decode(&extra) != io.EOF {
				parseErr = errors.New("extra JSON value")
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if parseErr != nil || line[0] != '{' {
				return nil, batchError(fmt.Sprintf("The %s file contains invalid JSONL on line %d. Each nonempty line must contain one JSON object. Download the file to inspect it.", selection, lineNumber))
			}
			idFields := 0
			var id string
			gjson.ParseBytes(line).ForEach(func(key, value gjson.Result) bool {
				if key.String() == "custom_id" {
					idFields++
					if value.Type == gjson.String {
						id = value.Str
					}
				}
				return ctx.Err() == nil
			})
			if idFields > 1 {
				return nil, batchError(fmt.Sprintf("The %s file repeats custom_id on line %d. Download the file to inspect the ambiguous record.", selection, lineNumber))
			}
			if filter == "" || id == filter {
				records = append(records, batchRecord{id: id, raw: line})
			}
		}
		if errors.Is(readErr, io.EOF) {
			return records, ctx.Err()
		}
	}
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
	return r.source.Read(data)
}

func matchBatchRecords(ctx context.Context, inputs, results []batchRecord, filter string) ([]batchRecordMatch, error) {
	byInputID := make(map[string][]json.RawMessage)
	byResultID := make(map[string][]json.RawMessage)
	for _, group := range []struct {
		records []batchRecord
		index   map[string][]json.RawMessage
	}{{inputs, byInputID}, {results, byResultID}} {
		for _, record := range group.records {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.id != "" {
				group.index[record.id] = append(group.index[record.id], record.raw)
			}
		}
	}
	matched := make([]batchRecordMatch, 0, len(results))
	seen := make(map[string]bool)
	for _, result := range results {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if (filter != "" && result.id != filter) || (result.id != "" && seen[result.id]) {
			continue
		}
		if result.id == "" {
			matched = append(matched, describeBatchRecordMatch("", nil, []json.RawMessage{result.raw}))
		} else {
			matched = append(matched, describeBatchRecordMatch(result.id, byInputID[result.id], byResultID[result.id]))
			seen[result.id] = true
		}
	}
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if (filter != "" && input.id != filter) || (input.id != "" && seen[input.id]) {
			continue
		}
		if input.id == "" {
			matched = append(matched, describeBatchRecordMatch("", []json.RawMessage{input.raw}, nil))
		} else {
			matched = append(matched, describeBatchRecordMatch(input.id, byInputID[input.id], nil))
			seen[input.id] = true
		}
	}
	return matched, nil
}

func describeBatchRecordMatch(id string, inputs, results []json.RawMessage) batchRecordMatch {
	if inputs == nil {
		inputs = []json.RawMessage{}
	}
	if results == nil {
		results = []json.RawMessage{}
	}
	row := batchRecordMatch{CustomID: id, Match: "matched", Inputs: inputs, Results: results}
	switch {
	case id == "":
		row.Match = "missing_custom_id"
		if len(inputs) > 0 {
			row.Issues = []string{"missing_input_custom_id"}
		} else {
			row.Issues = []string{"missing_result_custom_id"}
		}
	case len(inputs) == 0:
		row.Match, row.Issues = "missing_input", []string{"missing_input"}
	case len(results) == 0:
		row.Match, row.Issues = "missing_result", []string{"missing_result"}
	}
	if id != "" && len(inputs) > 1 {
		row.Match = "ambiguous"
		row.Issues = append(row.Issues, "duplicate_input_custom_id")
	}
	if id != "" && len(results) > 1 {
		row.Match = "ambiguous"
		row.Issues = append(row.Issues, "duplicate_result_custom_id")
	}
	return row
}
