package batches

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func (w workflow) newBatchBrowseCommand() *cli.Command {
	command := &cli.Command{
		Name: "browse", Usage: "Create a batch, follow progress, inspect results, or save a file",
		Description:     "Opens the Batch browser. Ctrl+N creates a batch from JSONL, an uploaded file, or a CSV text column. Press Enter or Ctrl+S on the review screen to submit one batch. For a CSV batch, choose View rows with answers to read results beside the original data. Press s there to save a spreadsheet. Processing batches update every 10 seconds while their action screen is open. Leaving the browser does not cancel a remote batch. Search filters the loaded page only; use Next page for more batches. Actions preserve this session's connection and project. Downloads show their folder and never overwrite files. Requires terminal stdin, stdout, and stderr with readable output. Action failures remain failures when the browser exits.",
		HideHelpCommand: true, Suggest: true,
		Flags: []cli.Flag{
			&cli.Int64Flag{Name: "page-size", Value: 20, Usage: "Batches to request per page, from 1 to 100"},
		},
		Action: w.browseBatches,
	}
	command.Metadata = map[string]any{"help-content": clihelp.Content{
		Description: command.Description,
		Examples:    []clihelp.Example{{Description: "Choose a batch and an action:", Command: "batches browse"}},
	}}
	return command
}

func batchBrowseAllowed(root *cli.Command, inputTerminal, outputTerminal, errorTerminal bool) bool {
	format := strings.ToLower(root.String("format"))
	return inputTerminal && outputTerminal && errorTerminal && batchInputSelectionAllowed(root, true, true) &&
		(format == "" || format == "auto" || format == "text") && !root.Bool("raw-output") && root.String("transform") == ""
}

func (w workflow) browseBatches(ctx context.Context, cmd *cli.Command) (err error) {
	if cmd.Args().Len() != 0 {
		return batchError("Unexpected extra arguments. Use batches browse to choose a batch, or batches retrieve with an ID.")
	}
	if !batchBrowseAllowed(cmd.Root(), term.IsTerminal(os.Stdin.Fd()), isTerminal(cmd.Root().Writer), term.IsTerminal(os.Stderr.Fd())) {
		return batchError("Batch browsing requires terminal stdin, stdout, and stderr. Use --format text and --format-error text, without --quiet, --debug, --raw-output, or transforms. Use batches list for scripts or other formats.")
	}
	if cmd.Int64("page-size") < 1 || cmd.Int64("page-size") > 100 {
		return batchError("Choose --page-size from 1 to 100.")
	}
	ctx, stop := batchSignalContext(ctx)
	defer stop()
	defer func() {
		if ctx.Err() != nil {
			joined := errors.Join(ctx.Err(), context.Cause(ctx), err)
			contextual := batchContextError(joined)
			var recovery, interrupted *WorkflowError
			if errors.As(err, &recovery) && recovery.submission != nil && errors.As(contextual, &interrupted) {
				copy := *recovery
				copy.code, copy.cause = interrupted.ExitCode(), joined
				err = &copy
			} else {
				err = contextual
			}
		}
	}()
	options, err := w.options.RequestOptions(cmd, batchRequestInput{ctx: ctx, file: os.Stdin})
	if err != nil {
		return err
	}
	client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
	browserContext, cancelBrowser := context.WithCancelCause(ctx)
	defer cancelBrowser(nil)
	session := newBatchBrowserSession(browserContext, cancelBrowser)
	defer func() { err = errors.Join(session.close(), err) }()
	return w.runBatchBrowser(browserContext, cmd, client, options, session.show, session.beforeOutput)
}

type batchBrowsePage struct {
	items []gjson.Result
	next  string
}

func fetchBatchBrowsePage(ctx context.Context, client openai.Client, options []option.RequestOption, after string, size int64) (batchBrowsePage, error) {
	var raw []byte
	opts := append(options[:len(options):len(options)], option.WithQuery("limit", strconv.FormatInt(size, 10)), option.WithQueryDel("after"), option.WithResponseBodyInto(&raw))
	if after != "" {
		opts = append(opts, option.WithQuery("after", after))
	}
	if _, err := client.Batches.List(ctx, openai.BatchListParams{}, opts...); err != nil {
		return batchBrowsePage{}, err
	}
	if !gjson.ValidBytes(raw) {
		return batchBrowsePage{}, batchError("The API returned invalid Batch list JSON. Refresh the browser to retry.")
	}
	value := gjson.ParseBytes(raw)
	data, more := value.Get("data"), value.Get("has_more")
	if !value.IsObject() || !data.IsArray() || more.Type != gjson.True && more.Type != gjson.False {
		return batchBrowsePage{}, batchError("The API returned an unfamiliar Batch list. Refresh the browser to retry.")
	}
	page := batchBrowsePage{}
	var invalid bool
	data.ForEach(func(_, item gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		id := item.Get("id")
		if !item.IsObject() || id.Type != gjson.String || id.Str == "" {
			invalid = true
			return false
		}
		page.items = append(page.items, item)
		return true
	})
	if err := ctx.Err(); err != nil {
		return batchBrowsePage{}, err
	}
	if invalid {
		return batchBrowsePage{}, batchError("The API returned a Batch without an ID. Refresh the browser to retry.")
	}
	if more.Bool() {
		if len(page.items) == 0 || page.items[len(page.items)-1].Get("id").Str == after {
			return batchBrowsePage{}, batchError("The API returned a missing or repeated page cursor. Refresh from the first page.")
		}
		page.next = page.items[len(page.items)-1].Get("id").Str
	}
	return page, nil
}

// The controller receives UI decisions without mutating command flags or rerunning the command tree.
func (w workflow) runBatchBrowser(ctx context.Context, cmd *cli.Command, client openai.Client, options []option.RequestOption, show func(context.Context, *batchBrowserScreen) error, beforeOutput func(context.Context) error) error {
	var failure error
	terminalFailed := false
	showScreen := show
	show = func(ctx context.Context, screen *batchBrowserScreen) error {
		err := showScreen(ctx, screen)
		if err != nil {
			terminalFailed = true
		}
		return err
	}
	prepareOutput := beforeOutput
	beforeOutput = func(ctx context.Context) error {
		err := prepareOutput(ctx)
		if err != nil {
			terminalFailed = true
		}
		return err
	}
	report := func(err error, action, id string) error {
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		// Screen failures include local Ctrl+C and terminal EOF. The parent
		// context can remain active, but another screen cannot recover them.
		var printFailure *batchBrowserPrintError
		if terminalFailed || errors.As(err, &printFailure) {
			return err
		}
		if failure == nil {
			failure = err
		}
		var shown *batchBrowserPresentedError
		if errors.As(err, &shown) {
			return nil
		}
		return w.showBatchBrowserFailure(ctx, cmd, err, action, id, show, beforeOutput)
	}
	page, err := fetchBatchBrowsePage(ctx, client, options, "", cmd.Int64("page-size"))
	if err != nil {
		return err
	}
	list := newBatchBrowserList(page, false)
	creation := newBatchBrowserDraft()
	csvSources := make(map[string]string)
	var lastSaveFolder string
	saveDrafts := make(map[string]map[string]batchBrowserSaveDraft)
	openBatch := func(batch gjson.Result) error {
		id := batch.Get("id").Str
		actions := newBatchBrowserActions(id)
		actions.actionsState.saveFolder = lastSaveFolder
		actions.actionsState.saveDrafts = saveDrafts[id]
		actions.actionsState.csvSource = csvSources[id]
		defer func() {
			lastSaveFolder = actions.actionsState.saveFolder
			if len(actions.actionsState.saveDrafts) != 0 {
				saveDrafts[id] = actions.actionsState.saveDrafts
			}
			if actions.actionsState.csvSource != "" {
				csvSources[id] = actions.actionsState.csvSource
			} else {
				delete(csvSources, id)
			}
		}()
		actions.setOverview(batch)
		actions.progress = &batchBrowserProgress{ctx: ctx, fetch: func(ctx context.Context) (gjson.Result, error) {
			return fetchBatchBrowserDetails(ctx, client, options, id)
		}}
		defer actions.stopProgress()
		for {
			if err := show(ctx, actions); err != nil {
				return err
			}
			actions.stopProgress()
			if actions.chosen == "back" {
				return nil
			}
			if actions.chosen == "poll-error" {
				if err := report(actions.progress.err, "refresh", id); err != nil {
					return err
				}
				continue
			}
			if actions.chosen == "refresh" {
				actions.progress.paused = false
			}
			if err := w.runBatchBrowserAction(ctx, cmd, client, options, id, actions, show, beforeOutput); err != nil {
				if actions.chosen == "refresh" {
					actions.progress.paused = true
				}
				if err := report(err, actions.chosen, id); err != nil {
					return err
				}
			}
		}
	}
	cursors, position := []string{""}, 0
	for {
		if err := show(ctx, list); err != nil {
			return errors.Join(err, failure)
		}
		switch list.chosen {
		case "back":
			return failure
		case "create":
			batch, err := w.createBatchInBrowser(ctx, cmd, client, options, creation, show)
			if id := batch.Get("id").Str; id != "" {
				if creation.csv != nil {
					csvSources[id] = creation.csv.path
				}
				creation = newBatchBrowserDraft()
			}
			if err != nil {
				if err := report(err, "create", batch.Get("id").Str); err != nil {
					return errors.Join(err, failure)
				}
				continue
			}
			if batch.Get("id").Str == "" {
				continue
			}
			if err := openBatch(batch); err != nil {
				return errors.Join(err, failure)
			}
			updated, err := fetchBatchBrowsePage(ctx, client, options, "", cmd.Int64("page-size"))
			if err != nil {
				if err := report(err, "list", ""); err != nil {
					return errors.Join(err, failure)
				}
				continue
			}
			page, cursors, position = updated, []string{""}, 0
			list.input.SetValue("")
			list.setPage(page, false)
		case "next", "previous", "refresh":
			next := position
			after := cursors[position]
			switch list.chosen {
			case "next":
				after = page.next
				for _, previous := range cursors[:position+1] {
					if after == previous {
						return errors.Join(failure, batchError("The API returned a repeated page cursor. Restart batches browse to refresh the list."))
					}
				}
				next++
			case "previous":
				next--
				after = cursors[next]
			case "refresh":
				next, after = 0, ""
			}
			updated, err := fetchBatchBrowsePage(ctx, client, options, after, cmd.Int64("page-size"))
			if err != nil {
				if err := report(err, "list", ""); err != nil {
					return errors.Join(err, failure)
				}
				continue
			}
			if list.chosen == "refresh" {
				cursors = []string{""}
			}
			if list.chosen == "next" {
				// Page contents can change after going back. Retain the cursor actually loaded.
				cursors = append(cursors[:next], after)
			}
			position, page = next, updated
			list.setPage(page, position > 0)
		default:
			id := strings.TrimPrefix(list.chosen, "batch:")
			for _, batch := range page.items {
				if batch.Get("id").Str == id {
					if err := openBatch(batch); err != nil {
						return errors.Join(err, failure)
					}
					break
				}
			}
		}
	}
}

func (w workflow) renderBatchBrowserAction(ctx context.Context, cmd *cli.Command, client openai.Client, options []option.RequestOption, id string, actions *batchBrowserScreen, show func(context.Context, *batchBrowserScreen) error, beforeOutput func(context.Context) error, body io.Writer) error {
	var raw []byte
	opts := append(options[:len(options):len(options)], option.WithResponseBodyInto(&raw))
	if _, err := client.Batches.Get(ctx, id, opts...); err != nil {
		return err
	}
	if !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() || gjson.ParseBytes(raw).Get("id").Str != id {
		return batchError("The API returned invalid Batch details. Return to the action menu and retry Batch details.")
	}
	batch := gjson.ParseBytes(raw)
	actions.setOverview(batch)
	action := actions.chosen
	if action == "refresh" {
		return nil
	}
	if action == "output" && actions.actionsState.csvSource != "" && batchBrowserCanExportCSV(batch) {
		return openBatchBrowserCSVRows(ctx, client, options, batch, actions, show)
	}
	if action == "raw-output" {
		action = "output"
	}
	if selection, save := strings.CutPrefix(action, "save:"); save {
		if batch.Get(selection+"_file_id").Str == "" {
			title, message := batchBrowserMissingFile(batch, selection)
			if err := writeBatchBrowserSection(ctx, body, title, message); err != nil {
				return err
			}
			return nil
		}
		destination, err := chooseBatchBrowserSavePath(ctx, actions, selection, batchBrowserDefaultSaveName(id, selection), show)
		if err != nil || destination == "" {
			return err
		}
		if err := w.downloadBatchFile(ctx, cmd, client, options, id, selection, destination); err != nil {
			return err
		}
		return nil
	}
	if action == "status" {
		if w.options.ShowBrowserDetails != nil {
			decorate := func(RecordTextStyle) {}
			if document, ok := body.(*batchBrowserDocument); ok {
				decorate = document.decorate
			}
			return w.options.ShowBrowserDetails(ctx, batch, body, decorate)
		}
		if err := w.batchShowJSON(ctx, cmd, batch, "retrieve"); err != nil {
			return err
		}
	} else {
		selection := action
		var diagnostics []gjson.Result
		if selection == "error" {
			batch.ForEach(func(key, value gjson.Result) bool {
				if key.Str == "errors" && !batchBrowserNoValidationErrors(value) {
					diagnostics = append(diagnostics, value)
				}
				return true
			})
		}
		showDiagnostics := len(diagnostics) > 0
		if showDiagnostics {
			if err := writeBatchBrowserSection(ctx, body, "Batch validation diagnostics", batchBrowserContext(batch)+"\n"); err != nil {
				return err
			}
			for _, diagnostic := range diagnostics {
				if err := w.batchShowJSON(ctx, cmd, diagnostic, "validation"); err != nil {
					return err
				}
			}
		}
		if batch.Get(selection+"_file_id").Str == "" && !showDiagnostics {
			title, message := batchBrowserMissingFile(batch, selection)
			if err := writeBatchBrowserSection(ctx, body, title, message); err != nil {
				return err
			}
		} else if batch.Get(selection+"_file_id").Str != "" {
			if err := w.browseBatchRecords(ctx, cmd, client, options, batch, selection, actions, show, beforeOutput); err != nil {
				return err
			}
		}
		if showDiagnostics {
			next := "Choose Batch details to inspect file IDs and status."
			if file := batch.Get("input_file_id"); file.Type == gjson.String && file.Str != "" {
				next = "Choose View input to inspect the referenced requests."
			}
			if err := writeBatchBrowserSection(ctx, body, "", "Next: "+next); err != nil {
				return err
			}
		}
	}
	return nil
}
