package batches

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/tidwall/gjson"
)

func batchCSVTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "expired":
		return true
	default:
		return false
	}
}

func validateBatchCSVResults(batch gjson.Result) error {
	if !batchCSVTerminalStatus(batch.Get("status").Str) {
		return batchError("This batch has not finished. Refresh its status before exporting results.")
	}
	if batch.Get("input_file_id").Str == "" {
		return batchError("The batch has no input file to match against the CSV. Open Batch details to check the file.")
	}
	return nil
}

func writeBatchCSVResults(ctx context.Context, client openai.Client, options []option.RequestOption, batch gjson.Result, sourcePath string, file *os.File, preview io.Writer) (err error) {
	source, err := prepareBatchCSVSnapshot(ctx, sourcePath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, source.close()) }()
	reader, header, err := newBatchCSVReader(batchRecordContextReader{ctx, io.NewSectionReader(source.file, 0, source.size)})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	store, err := newBatchRecordStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.close()) }()
	var inputGroups, beforeErrors int64
	for _, selection := range []string{"input", "output", "error"} {
		if selection == "error" {
			beforeErrors = store.recordCount
		}
		id := batch.Get(selection + "_file_id").Str
		if id != "" {
			if err := readBatchRecordsFile(ctx, client, options, id, selection, "", selection == "input", true, store); err != nil {
				return err
			}
		}
		if selection == "input" {
			inputGroups = store.groupCount
			if inputGroups != store.recordCount {
				return batchError("The batch input file repeats a request ID. Inspect the input file before exporting.")
			}
			if inputGroups == 0 {
				return batchError("The batch input file contains no requests. Inspect the input file before exporting.")
			}
		} else if store.groupCount != inputGroups {
			return batchError(fmt.Sprintf("The batch %s file contains a request ID missing from the batch input file. Inspect both files before exporting.", selection))
		}
	}

	writer := csv.NewWriter(batchSnapshotWriter{file})
	columns := slices.Clone(header)
	for _, label := range []string{"result", "error"} {
		name := label
		for n := 2; slices.ContainsFunc(columns, func(s string) bool { return strings.EqualFold(s, name) }); n++ {
			name = fmt.Sprintf("%s_%d", label, n)
		}
		columns = append(columns, name)
	}
	if err := writeBatchCSVExportRow(writer, columns); err != nil {
		return err
	}
	for number := 1; ; number++ {
		row, err := readBatchCSVRow(ctx, reader, number)
		if err == io.EOF {
			if number == 1 || int64(number-1) != store.groupCount {
				return batchCSVSourceMismatch()
			}
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if int64(number) > store.groupCount {
			return batchCSVSourceMismatch()
		}
		group, err := store.group(int64(number))
		if err != nil {
			return err
		}
		match, err := store.equalKey(group, batchCSVRowID(header, row, number))
		if err != nil {
			return err
		}
		if !match {
			return batchCSVSourceMismatch()
		}
		if group.resultCount > 1 {
			return batchError(fmt.Sprintf("CSV row %d has multiple result records. Inspect the raw batch files before exporting.", number))
		}
		answer, problem := "", "No result returned for this request."
		if group.resultCount == 1 {
			var record bytes.Buffer
			if err := store.writeRecord(&record, group.resultHead); err != nil {
				return err
			}
			answer, problem = batchCSVResult(gjson.ParseBytes(record.Bytes()), group.resultHead > beforeErrors)
		}
		if preview != nil {
			if err := writeBatchBrowserCSVRow(preview, number, header, row, answer, problem); err != nil {
				return err
			}
		}
		if err := writeBatchCSVExportRow(writer, append(row, answer, problem)); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func saveBatchCSVResults(ctx context.Context, file *os.File, destPath string) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return batchRecordStorageError(err)
	}
	_, err := writeBatchDownload(ctx, destPath, io.NopCloser(file))
	return err
}

func batchCSVSourceMismatch() error {
	return invalidBatchCSV("The CSV does not match this batch's input. Choose the original CSV with the same columns, values, and row order.", nil)
}

func batchCSVResult(record gjson.Result, fromErrorFile bool) (answer, problem string) {
	for _, key := range []string{"error", "response.body.error"} {
		if detail := record.Get(key); detail.Exists() && detail.Type != gjson.Null {
			for _, field := range []string{"message", "code", "type"} {
				if text := detail.Get(field).Str; text != "" {
					return "", text
				}
			}
			return "", "Request failed. See the batch error record for details."
		}
	}
	status, ok := batchCount(record.Get("response.status_code"))
	if !ok || status < 100 || status > 599 {
		return "", "Invalid response status. Inspect the raw batch files."
	}
	if status < 200 || status >= 300 {
		return "", fmt.Sprintf("Request failed with HTTP %d.", status)
	}
	if fromErrorFile {
		return "", "The API returned an error record. Inspect the batch error file."
	}
	var response responses.Response
	if err := json.Unmarshal([]byte(record.Get("response.body").Raw), &response); err != nil || response.Object != "response" {
		return "", "Unrecognized response. Inspect the raw batch output."
	}
	answer = response.OutputText()
	if response.Status != "" && response.Status != "completed" {
		return answer, "Response status: " + string(response.Status) + "."
	}
	for _, item := range response.Output {
		if item.Type != "message" {
			continue
		}
		if item.Status != "" && item.Status != "completed" {
			return answer, "Message status: " + item.Status + "."
		}
		for _, part := range item.Content {
			if part.Type == "refusal" {
				return answer, "Refused: " + part.Refusal
			}
		}
	}
	if answer == "" {
		return "", "No text answer returned. Inspect the raw batch output."
	}
	return answer, ""
}

// A spreadsheet can evaluate formula-like input cells, headers, and model output.
func writeBatchCSVExportRow(writer *csv.Writer, row []string) error {
	for index, cell := range row {
		first := strings.TrimLeftFunc(cell, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
		lead, _ := utf8.DecodeRuneInString(first)
		if strings.ContainsRune("=+-@＝＋－＠", lead) || strings.HasPrefix(cell, "\t") || strings.HasPrefix(cell, "\r") || strings.HasPrefix(cell, "\n") {
			row[index] = "'" + cell
		}
	}
	return writer.Write(row)
}
