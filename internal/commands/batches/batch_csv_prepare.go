package batches

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	requestconfig "github.com/openai/openai-cli/internal/request"
)

type batchCSVPreview struct {
	Count      int
	FirstInput string
	Header     []string
}

// Only a problem with the selected spreadsheet can be corrected in the picker.
type batchCSVSourceError struct{ error }

func (e *batchCSVSourceError) Unwrap() error { return e.error }

func invalidBatchCSV(message string, cause error) error {
	return &batchCSVSourceError{&WorkflowError{message: message, cause: cause}}
}

func batchCSVParseFailure(message string, err error) error {
	var parse *csv.ParseError
	if errors.As(err, &parse) {
		return invalidBatchCSV(message, err)
	}
	return &WorkflowError{message: message, cause: err}
}

func soleBatchCSVFailure(err error) error {
	for {
		join, ok := err.(interface{ Unwrap() []error })
		if !ok || len(join.Unwrap()) != 1 {
			return err
		}
		err = join.Unwrap()[0]
	}
}

func prepareBatchCSVSnapshot(ctx context.Context, path string) (*batchSubmissionSnapshot, error) {
	file, err := prepareBatchSubmissionSnapshot(ctx, path)
	if err == nil || ctx.Err() != nil {
		return file, err
	}
	// A snapshot can also fail in scratch storage or while closing files.
	// Preserve those failures, including errors joined with a source error.
	switch failure := soleBatchCSVFailure(err).(type) {
	case *requestconfig.InputFileError:
		if failure.Operation != "close" {
			return nil, invalidBatchCSV("Could not read the original CSV. Check the file and its permissions.", err)
		}
	case *WorkflowError:
		if failure.cause == nil {
			return nil, invalidBatchCSV("Choose a regular CSV file that did not change while it was being read.", err)
		}
	}
	return nil, err
}

func readBatchCSVHeader(ctx context.Context, path string) (header []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, invalidBatchCSV("Could not open the CSV file. Check its location and read permissions.", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, &WorkflowError{message: "Could not close the CSV file.", cause: closeErr})
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, invalidBatchCSV("Choose a regular CSV file on this computer.", err)
	}
	_, header, err = newBatchCSVReader(batchRequestInput{ctx: ctx, file: file})
	return header, err
}

func newBatchCSVReader(input io.Reader) (*csv.Reader, []string, error) {
	in := bufio.NewReader(input)
	prefix, err := in.Peek(3)
	if err != nil && err != io.EOF {
		return nil, nil, &WorkflowError{message: "Could not read the CSV header. Check the file and try again.", cause: err}
	}
	if string(prefix) == "\uFEFF" {
		_, _ = in.Discard(3)
	}
	reader := csv.NewReader(in)
	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil, invalidBatchCSV("The CSV is empty. Add column headings and at least one data row.", nil)
	}
	if err != nil {
		return nil, nil, batchCSVParseFailure("Could not read the CSV header. Check the file encoding and quoted column names.", err)
	}
	seen := make(map[string]bool, len(header))
	for i, field := range header {
		name := strings.TrimSpace(field)
		if name == "" || !utf8.ValidString(field) || seen[name] {
			return nil, nil, invalidBatchCSV(fmt.Sprintf("CSV column %d needs a unique, nonempty UTF-8 heading. Rename the column and try again.", i+1), nil)
		}
		seen[name] = true
	}
	return reader, header, nil
}

// Row numbers count data records, so a quoted newline never changes a record's ID.
func readBatchCSVRow(ctx context.Context, reader *csv.Reader, rowNumber int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row, err := reader.Read()
	if err == io.EOF {
		return nil, io.EOF
	}
	if err != nil {
		message := fmt.Sprintf("Could not read CSV data row %d. Check its quotes and number of columns.", rowNumber)
		return nil, batchCSVParseFailure(message, err)
	}
	for _, field := range row {
		if !utf8.ValidString(field) {
			return nil, invalidBatchCSV(fmt.Sprintf("CSV data row %d contains invalid UTF-8. Save the CSV as UTF-8 and try again.", rowNumber), nil)
		}
	}
	return row, nil
}

// Length framing distinguishes field boundaries. Including every column and the
// data row number detects edited spreadsheets and distinguishes identical rows.
func batchCSVRowID(header, row []string, rowNumber int) string {
	hash := sha256.New()
	var size [8]byte
	writeNumber := func(n int) {
		binary.BigEndian.PutUint64(size[:], uint64(n))
		hash.Write(size[:])
	}
	writeNumber(rowNumber)
	for _, fields := range [][]string{header, row} {
		writeNumber(len(fields))
		for _, value := range fields {
			writeNumber(len(value))
			io.WriteString(hash, value)
		}
	}
	return "csv-" + base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}

// A matching identifier can offer a local CSV. The subsequent join verifies every original row.
func isBatchCSVRowID(id string) bool {
	encoded, ok := strings.CutPrefix(id, "csv-")
	if !ok || len(encoded) != base64.RawURLEncoding.EncodedLen(sha256.Size) {
		return false
	}
	_, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	return err == nil
}

func prepareBatchCSV(ctx context.Context, path, column, model, instructions string) (*batchSubmissionSnapshot, batchCSVPreview, error) {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(instructions) == "" {
		return nil, batchCSVPreview{}, invalidBatchCSV("Choose a model and enter instructions before reviewing the batch.", nil)
	}
	snapshot, err := prepareBatchCSVSnapshot(ctx, path)
	if err != nil {
		return nil, batchCSVPreview{}, err
	}
	preview, err := convertBatchCSV(ctx, snapshot, column, model, instructions)
	if err != nil {
		return nil, batchCSVPreview{}, errors.Join(err, snapshot.close())
	}
	return snapshot, preview, nil
}

func convertBatchCSV(ctx context.Context, snapshot *batchSubmissionSnapshot, column, model, instructions string) (preview batchCSVPreview, err error) {
	reader, header, err := newBatchCSVReader(batchRecordContextReader{ctx, io.NewSectionReader(snapshot.file, 0, snapshot.Size())})
	if err != nil {
		return preview, err
	}
	selected := slices.Index(header, column)
	if selected < 0 {
		return preview, invalidBatchCSV("The selected column no longer exists in this CSV. Choose the input column again.", nil)
	}
	preview.Header = header
	jsonl, _, err := snapshot.store.temporaryFile()
	if err != nil {
		return preview, err
	}
	out := bufio.NewWriter(batchSnapshotWriter{jsonl})
	encoder := json.NewEncoder(out)
	body := map[string]string{"model": model, "instructions": instructions}
	record := map[string]any{"method": "POST", "url": "/v1/responses", "body": body}
	for rowNumber := 1; ; rowNumber++ {
		row, err := readBatchCSVRow(ctx, reader, rowNumber)
		if err == io.EOF {
			break
		}
		if err != nil {
			return preview, err
		}
		input := row[selected]
		if strings.TrimSpace(input) == "" {
			return preview, invalidBatchCSV(fmt.Sprintf("The input column is empty in CSV data row %d. Add text or remove that row.", rowNumber), nil)
		}
		if preview.Count == 0 {
			// Retain only a terminal-sized excerpt, even when a cell spans many megabytes.
			var capture batchBrowserCatalogCapture
			capture.data = []byte(input[:min(len(input), batchBrowserCatalogHint)])
			capture.truncated = len(input) > len(capture.data)
			preview.FirstInput = capture.hint()
		}
		record["custom_id"] = batchCSVRowID(header, row, rowNumber)
		body["input"] = input
		if err := encoder.Encode(record); err != nil {
			return preview, err
		}
		preview.Count++
	}
	if preview.Count == 0 {
		return preview, invalidBatchCSV("The CSV has column headings but no data. Add at least one data row and try again.", nil)
	}
	if err := out.Flush(); err != nil {
		return preview, err
	}
	info, err := jsonl.Stat()
	if err != nil {
		return preview, batchRecordStorageError(err)
	}
	if err := snapshot.store.removeFile(snapshot.file); err != nil {
		return preview, err
	}
	snapshot.file, snapshot.size = jsonl, info.Size()
	snapshot.filename = strings.TrimSuffix(snapshot.filename, filepath.Ext(snapshot.filename)) + ".jsonl"
	snapshot.mediaType = "application/jsonl"
	return preview, ctx.Err()
}
