package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

// This bounds the optional projection's memory, not accepted payload size.
// Larger records retain complete, cancellation-aware streaming detail output.
const batchRecordProjectionBuffer = 256 * 1024

func showReadableBatchRecords(ctx context.Context, records batchcommands.RecordOutput, out, diagnostics io.Writer) error {
	out = clioutput.Writer{Context: ctx, Sink: out}
	metadata, _ := json.Marshal(struct {
		Batch  string `json:"batch"`
		Source string `json:"source"`
		FileID string `json:"file_id"`
	}{records.BatchID, records.File + " file", records.FileID})
	if err := readable.Write(out, gjson.ParseBytes(metadata)); err != nil {
		return err
	}
	visited, projected := false, false
	inputIndex, resultIndex := 0, 0
	err := records.Visit(func(item batchcommands.RecordItem) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		{
			if _, err := io.WriteString(out, "\n"); err != nil {
				return err
			}
		}
		visited = true
		switch item.Kind {
		case "group":
			inputIndex, resultIndex = 0, 0
		case "matched_input":
			inputIndex++
			if _, err := fmt.Fprintf(out, "Input record %d:\n", inputIndex); err != nil {
				return err
			}
		case "matched_result":
			resultIndex++
			if _, err := fmt.Fprintf(out, "Result %d:\n", resultIndex); err != nil {
				return err
			}
		}
		summarized, err := showReadableBatchRecord(ctx, out, item, records)
		projected = projected || summarized && item.Kind != "group"
		return err
	})
	if err != nil {
		return err
	}
	if !visited {
		_, err := io.WriteString(out, "\nNo records match this selection in the selected file.\n")
		return err
	}
	if projected && outputDiagnosticsAllowed(ctx) {
		if err := readable.WriteText(clioutput.Writer{Context: ctx, Sink: diagnostics}, clioutput.ResourceSummaryHint); err != nil {
			return &clioutput.DiagnosticError{Err: err}
		}
	}
	return ctx.Err()
}

func showReadableBatchRecord(ctx context.Context, out io.Writer, item batchcommands.RecordItem, records batchcommands.RecordOutput) (_ bool, err error) {
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { err := item.WriteJSON(writer); _ = writer.CloseWithError(err); done <- err }()
	defer func() { _ = reader.CloseWithError(err); err = errors.Join(err, <-done) }()
	// A single record may be much larger than the projection buffer. Preserve
	// the buffered prefix and stream its remainder through the existing renderer.
	prefix, readErr := io.ReadAll(io.LimitReader(reader, batchRecordProjectionBuffer+1))
	if readErr != nil {
		if _, err := io.WriteString(out, "Details (partial record):\n"); err != nil {
			return false, errors.Join(err, readErr)
		}
	} else if len(prefix) <= batchRecordProjectionBuffer {
		value, projectionErr := transform.ProjectBatchRecord(ctx, gjson.ParseBytes(prefix), item.Kind)
		if projectionErr != nil {
			return false, projectionErr
		}
		if value.Exists() {
			return true, readable.Write(out, value)
		}
		if _, err := io.WriteString(out, "Details (unrecognized record format):\n"); err != nil {
			return false, err
		}
	} else {
		if _, err := io.WriteString(out, "Details (large record; complete content):\n"); err != nil {
			return false, err
		}
	}
	return false, readable.WriteJSONStream(ctx, out, io.MultiReader(bytes.NewReader(prefix), reader), records.TemporaryFile)
}
