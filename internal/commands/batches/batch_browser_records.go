package batches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Each action downloads once. Navigation and view changes reuse private records.
func (w workflow) browseBatchRecords(ctx context.Context, cmd *cli.Command, client openai.Client, options []option.RequestOption, batch gjson.Result, selection string, show func(context.Context, *batchBrowserScreen) error, beforeOutput func(context.Context) error) (err error) {
	store, err := newBatchRecordStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.close()) }()
	fileID, batchID := batch.Get(selection+"_file_id").Str, batch.Get("id").Str
	if err = readBatchRecordsFile(ctx, client, options, fileID, selection, "", false, false, store); err != nil {
		return err
	}
	catalog, err := newBatchBrowserCatalog(ctx, store, selection)
	if err != nil {
		return err
	}
	list := newBatchBrowserRequests(ctx, catalog, batchBrowserActionLabel(selection), batchID)
	defer func() {
		list.requestState.stop()
		catalog.wait()
	}()
	for {
		if err = show(ctx, list); err != nil {
			return err
		}
		if list.requestState.err != nil {
			return list.requestState.err
		}
		if list.chosen == "back" || list.chosen == "" {
			return nil
		}
		recordID := int64(0)
		if list.chosen != "all" {
			recordID, err = strconv.ParseInt(strings.TrimPrefix(list.chosen, "record:"), 10, 64)
			if err != nil || recordID < 1 || recordID > store.recordCount {
				return batchError("The selected request is unavailable. Return to the request list and select it again.")
			}
		}
		if err = w.showBatchBrowserRecord(ctx, cmd, store, batchID, selection, fileID, recordID, show, beforeOutput); err != nil {
			return err
		}
	}
}

func browserRecordOutput(store *batchRecordStore, batchID, selection, fileID string, recordID int64) RecordOutput {
	kind := "result"
	if selection == "input" {
		kind = "input_detail"
	}
	return RecordOutput{
		BatchID: batchID, File: selection, FileID: fileID, Detail: true,
		TemporaryFile: store.temporaryFile,
		WriteJSON: func(out io.Writer) error {
			if recordID != 0 {
				return store.writeRecord(out, recordID)
			}
			return store.writeJSON(out, batchID, selection, fileID, false)
		},
		Visit: func(visit func(RecordItem) error) error {
			first, last := recordID, recordID
			if recordID == 0 {
				first, last = 1, store.recordCount
			}
			for id := first; id <= last; id++ {
				if err := store.ctx.Err(); err != nil {
					return err
				}
				if err := visit(RecordItem{Kind: kind, WriteJSON: func(out io.Writer) error { return store.writeRecord(out, id) }}); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func (w workflow) showBatchBrowserRecord(ctx context.Context, cmd *cli.Command, store *batchRecordStore, batchID, selection, fileID string, recordID int64, show func(context.Context, *batchBrowserScreen) error, beforeOutput func(context.Context) error) (err error) {
	var views [2]*batchBrowserScreen
	defer func() {
		for _, screen := range views {
			if screen != nil {
				screen.documentState.stop()
				err = errors.Join(err, screen.document.close())
			}
		}
	}()
	records := browserRecordOutput(store, batchID, selection, fileID, recordID)
	title := map[string]string{"input": "Request details", "output": "Result details", "error": "Request error"}[selection]
	identity := fmt.Sprintf("%s / Record %d", batchID, recordID)
	if recordID == 0 {
		title, identity = batchBrowserActionLabel(selection)+" / All records", batchID
	}
	mode := 0
	for {
		if views[mode] == nil {
			document, createErr := newBatchBrowserDocument(ctx)
			if createErr != nil {
				return createErr
			}
			viewTitle := title
			if mode == 1 {
				viewTitle += " / Raw JSON"
			}
			screen := newBatchBrowserDocumentScreen(ctx, document, viewTitle, identity)
			screen.documentRawAvailable, screen.documentRaw = true, mode == 1
			views[mode] = screen
			if mode == 1 {
				err = writeBatchBrowserRawJSON(ctx, document, records.WriteJSON)
			} else {
				presentation := records
				if recordID > 0 {
					presentation.Decorate = document.decorate
				}
				err = w.options.ShowRecords(ctx, cmd, presentation, document, io.Discard)
			}
			if err != nil {
				return err
			}
		}
		screen := views[mode]
		if err = show(ctx, screen); err != nil {
			return err
		}
		if screen.documentState.err != nil {
			return screen.documentState.err
		}
		switch screen.chosen {
		case "toggle_raw":
			mode = 1 - mode
		case "print":
			if err = beforeOutput(ctx); err != nil {
				return &batchBrowserPrintError{err}
			}
			if err = writeBatchStdout(ctx, cmd.Root().Writer, func(out io.Writer) error { return screen.document.writeTo(ctx, out) }); err != nil {
				return &batchBrowserPrintError{err}
			}
			if err = show(ctx, newBatchBrowserReturn("Printed to terminal scrollback.")); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}
