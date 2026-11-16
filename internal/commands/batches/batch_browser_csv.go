package batches

import (
	"context"
	"errors"
	"slices"

	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
)

type batchBrowserCSV struct {
	path, column, instructions, model string
	header                            []string
	preview                           batchCSVPreview
}

func (csv *batchBrowserCSV) prepare(ctx context.Context) (*batchSubmissionSnapshot, error) {
	file, preview, err := prepareBatchCSV(ctx, csv.path, csv.column, csv.model, csv.instructions)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(csv.header, preview.Header) {
		return nil, errors.Join(invalidBatchCSV("The CSV headings changed. Open CSV setup and choose the input column again.", nil), file.close())
	}
	csv.preview = preview
	return file, nil
}

func batchBrowserCSVNotice(err error) string {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ""
	}
	if source, ok := soleBatchCSVFailure(err).(*batchCSVSourceError); ok {
		return source.Error()
	}
	return ""
}

func editBatchBrowserCSV(ctx context.Context, draft *batchBrowserDraft, path string, reimport bool, show func(context.Context, *batchBrowserScreen) error) (completed bool, notice string, err error) {
	csv := batchBrowserCSV{path: path}
	if draft.csv != nil && draft.csv.path == path {
		csv = *draft.csv
	}
	header, err := batchBrowserWork(ctx, "Reading CSV headings", show, func(workCtx context.Context) ([]string, error) {
		header, err := readBatchCSVHeader(workCtx, path)
		if notice = batchBrowserCSVNotice(err); notice != "" {
			return nil, nil
		}
		return header, err
	})
	if err != nil || notice != "" {
		return false, notice, err
	}
	csv.header = header
	if !slices.Contains(header, csv.column) {
		csv.column = ""
	}
	form := newBatchBrowserCSVForm(&csv)
	if err := show(ctx, form); err != nil {
		return false, "", err
	}
	if form.chosen != "review" {
		return false, "", nil
	}
	// Reuse an already uploaded file only if its request settings still match.
	if reimport || draft.csv == nil || draft.csv.path != path || draft.csv.column != csv.column ||
		draft.csv.model != csv.model || draft.csv.instructions != csv.instructions || !slices.Equal(draft.csv.header, header) {
		draft.path, draft.inputID, draft.inputName = path, "", ""
	}
	draft.csv, draft.folder, draft.endpoint = &csv, batchBrowserFileFolder(path), string(openai.BatchNewParamsEndpointV1Responses)
	draft.endpointSet = false
	return true, "", nil
}

func batchBrowserCanExportCSV(batch gjson.Result) bool {
	return batch.Get("endpoint").Str == "/v1/responses" && batch.Get("input_file_id").Str != "" &&
		batchCSVTerminalStatus(batch.Get("status").Str)
}
