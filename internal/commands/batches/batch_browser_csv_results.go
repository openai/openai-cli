package batches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
)

func writeBatchBrowserCSVRow(out io.Writer, number int, header, row []string, answer, problem string) error {
	var text strings.Builder
	if number > 1 {
		text.WriteByte('\n')
	}
	fmt.Fprintf(&text, "Row %d\n", number)
	for i, column := range header {
		text.WriteString("  " + batchInputLabel(column) + ": " + batchInputLabel(row[i]) + "\n")
	}
	if answer != "" {
		text.WriteString("Answer: " + batchInputLabel(answer) + "\n")
	}
	if problem != "" {
		text.WriteString("Error: " + batchInputLabel(problem) + "\n")
	}
	_, err := io.WriteString(out, text.String())
	return err
}

// A wrong or moved spreadsheet is an editable file choice. Keep the user at
// the picker on a source mismatch; propagate actual API and storage failures.
func openBatchBrowserCSVRows(ctx context.Context, client openai.Client, options []option.RequestOption, batch gjson.Result, actions *batchBrowserScreen, show func(context.Context, *batchBrowserScreen) error) error {
	source := actions.actionsState.csvSource
	var folder, notice string
	for {
		if source == "" {
			var err error
			source, err = chooseBatchBrowserFile(ctx, folder, "CSV", func(ctx context.Context, picker *batchBrowserScreen) error {
				if notice != "" && picker.kind == "flow" && picker.flow.mode == "picker" {
					picker.notice, notice = notice, ""
				}
				return show(ctx, picker)
			})
			if err != nil || source == "" {
				return err
			}
		}
		err := browseBatchCSVResults(ctx, client, options, batch, source, actions, show)
		msg := batchBrowserCSVNotice(err)
		if msg == "" || ctx.Err() != nil {
			return err
		}
		actions.actionsState.csvSource = ""
		actions.setOverview(batch)
		folder, notice, source = batchBrowserFileFolder(source), msg, ""
	}
}

func browseBatchCSVResults(ctx context.Context, client openai.Client, options []option.RequestOption, batch gjson.Result, source string, actions *batchBrowserScreen, show func(context.Context, *batchBrowserScreen) error) (err error) {
	if err := validateBatchCSVResults(batch); err != nil {
		return err
	}
	document, err := newBatchBrowserDocument(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, document.close()) }()
	sheet, _, err := document.store.temporaryFile()
	if err != nil {
		return err
	}
	// Navigation and saving share one verified join. No user file is created until requested.
	if _, err := batchBrowserWork(ctx, "Reading CSV results", show, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, writeBatchCSVResults(ctx, client, options, batch, source, sheet, document)
	}); err != nil {
		return err
	}
	actions.actionsState.csvSource = source
	actions.setOverview(batch)
	screen := newBatchBrowserDocumentScreen(ctx, document, "Rows with answers", filepath.Base(source))
	screen.documentState.csvResults = true
	defer screen.documentState.stop()
	dest := strings.TrimSuffix(source, filepath.Ext(source)) + "-results.csv"
	if actions.actionsState.saveFolder != "" {
		dest, err = batchBrowserSaveDestinationIn(filepath.Base(dest), actions.actionsState.saveFolder)
		if err != nil {
			return err
		}
	}
	for {
		if err := show(ctx, screen); err != nil {
			return err
		}
		if screen.documentState.err != nil {
			return screen.documentState.err
		}
		if screen.chosen != "save" && screen.chosen != "save-as" {
			return nil
		}
		saved, err := saveBrowserCSVSheet(ctx, sheet, dest, actions, screen.chosen == "save-as", show)
		if err != nil {
			return err
		}
		if saved != "" {
			dest = saved
			screen.subtitle = "Saved " + filepath.Base(saved) + " in " + batchBrowserFileFolder(saved)
		}
	}
}

func saveBrowserCSVSheet(ctx context.Context, sheet *os.File, dest string, actions *batchBrowserScreen, choose bool, show func(context.Context, *batchBrowserScreen) error) (string, error) {
	path := dest
	notice := ""
	for {
		if choose {
			var err error
			path, err = chooseBatchBrowserSavePath(ctx, actions, "csv", path, func(ctx context.Context, m *batchBrowserScreen) error {
				if m.kind == "path" && notice != "" {
					m.notice, notice = notice, ""
				}
				return show(ctx, m)
			})
			if err != nil || path == "" {
				return "", err
			}
		}
		err := saveBatchCSVResults(ctx, sheet, path)
		if err == nil {
			return path, nil
		}
		if ctx.Err() != nil {
			return "", err
		}
		switch {
		case errors.Is(err, os.ErrExist):
			notice = "That file already exists. Choose a new name or folder."
		case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
			notice = "The destination folder is unavailable. Press Ctrl+L to choose an existing folder."
		case errors.Is(err, os.ErrPermission):
			// A cleanup error can occur after publication. Preserve that failure
			// if the destination exists or its status cannot be checked.
			if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
				return "", err
			}
			notice = "Cannot write in this folder. Press Ctrl+L to choose a writable folder."
		default:
			return "", err
		}
		choose = true
	}
}
