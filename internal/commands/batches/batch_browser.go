package batches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func (w workflow) newBatchBrowseCommand() *cli.Command {
	command := &cli.Command{
		Name: "browse", Usage: "Choose a batch, inspect its results, or download a file",
		Description:     "Opens a read-only Batch browser. Search filters the loaded page only; use Next page for more batches. Actions preserve this session's connection and project. Downloads require a new file path. Requires terminal stdin, stdout, and stderr with readable output. No batch is submitted or canceled. Action failures remain failures when the browser exits.",
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
			err = batchContextError(errors.Join(ctx.Err(), context.Cause(ctx), err))
		}
	}()
	options, err := w.options.RequestOptions(cmd, batchRequestInput{ctx: ctx, file: os.Stdin})
	if err != nil {
		return err
	}
	client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
	return w.runBatchBrowser(ctx, cmd, client, options, runBatchBrowserScreen)
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
func (w workflow) runBatchBrowser(ctx context.Context, cmd *cli.Command, client openai.Client, options []option.RequestOption, show func(context.Context, *batchBrowserScreen) error) error {
	var failure error
	report := func(err error) error {
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		if failure == nil {
			failure = err
		}
		message := "The browser action failed. Retry the action with the same batch after checking access and connection settings."
		var local *WorkflowError
		var apierr *openai.Error
		if errors.As(err, &apierr) && w.options.APIErrorMessage != nil {
			message = w.options.APIErrorMessage(cmd.Root(), err, apierr)
		} else if errors.As(err, &local) {
			message = local.Error()
		}
		if writeErr := writeBatchStdout(ctx, os.Stderr, func(out io.Writer) error { return readable.WriteText(out, message) }); writeErr != nil {
			return errors.Join(err, writeErr)
		}
		return show(ctx, newBatchBrowserReturn("Action failed. Review the message above."))
	}
	page, err := fetchBatchBrowsePage(ctx, client, options, "", cmd.Int64("page-size"))
	if err != nil {
		return err
	}
	list := newBatchBrowserList(page, false)
	cursors, position := []string{""}, 0
	for {
		if err := show(ctx, list); err != nil {
			return errors.Join(failure, err)
		}
		switch list.chosen {
		case "quit", "back":
			return failure
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
				if err := report(err); err != nil {
					return errors.Join(failure, err)
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
			actions := newBatchBrowserActions(id, cmd)
			drafts := make(map[string]string)
			for {
				if err := show(ctx, actions); err != nil {
					return errors.Join(failure, err)
				}
				if actions.chosen == "back" {
					break
				}
				if err := w.runBatchBrowserAction(ctx, cmd, client, options, id, actions.chosen, drafts, show); err != nil {
					if err := report(err); err != nil {
						return errors.Join(failure, err)
					}
				}
			}
		}
	}
}

func (w workflow) runBatchBrowserAction(ctx context.Context, cmd *cli.Command, client openai.Client, options []option.RequestOption, id, action string, drafts map[string]string, show func(context.Context, *batchBrowserScreen) error) error {
	var raw []byte
	opts := append(options[:len(options):len(options)], option.WithResponseBodyInto(&raw))
	if _, err := client.Batches.Get(ctx, id, opts...); err != nil {
		return err
	}
	if !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() || gjson.ParseBytes(raw).Get("id").Str != id {
		return batchError("The API returned invalid Batch details. Return to the action menu and retry Status.")
	}
	batch := gjson.ParseBytes(raw)
	if action == "download" {
		choice := newBatchBrowserDownload(id)
		if err := show(ctx, choice); err != nil {
			return err
		}
		if choice.chosen == "back" {
			return nil
		}
		if batch.Get(choice.chosen+"_file_id").Str == "" {
			if err := writeBatchBrowserMessage(ctx, cmd, batchBrowserMissingFile(batch, choice.chosen)); err != nil {
				return err
			}
			return show(ctx, newBatchBrowserReturn("File availability shown above."))
		}
		path := newBatchBrowserPath(id, choice.chosen)
		path.input.SetValue(drafts[choice.chosen])
		if err := show(ctx, path); err != nil {
			return err
		}
		drafts[choice.chosen] = path.input.Value()
		if path.chosen == "back" {
			return nil
		}
		if err := w.downloadBatchFile(ctx, cmd, client, options, id, choice.chosen, path.input.Value()); err != nil {
			return err
		}
		return show(ctx, newBatchBrowserReturn("Download receipt shown above."))
	}
	if action == "status" {
		if err := w.batchShowJSON(ctx, cmd, batch, "retrieve"); err != nil {
			return err
		}
	} else {
		selection := action
		if batch.Get(selection+"_file_id").Str == "" {
			message := batchBrowserMissingFile(batch, selection)
			if err := writeBatchBrowserMessage(ctx, cmd, message); err != nil {
				return err
			}
		} else if err := w.showBatchRecords(ctx, cmd, client, options, id, selection, "", false); err != nil {
			return err
		}
	}
	return show(ctx, newBatchBrowserReturn("Output shown above. Scroll your terminal to read it."))
}

func batchBrowserMissingFile(batch gjson.Result, selection string) string {
	status := batchInputLabel(batch.Get("status").Str)
	failed, valid := batchCount(batch.Get("request_counts.failed"))
	if selection == "error" && batch.Get("status").Str == "completed" && valid && failed == 0 {
		return "No error file was returned. This completed batch reports zero failed requests. Choose Results to read its answers."
	}
	return fmt.Sprintf("No %s file is available. Batch status: %s. Choose Status to inspect progress and validation errors, then retry later if processing continues.", selection, status)
}

func writeBatchBrowserMessage(ctx context.Context, cmd *cli.Command, message string) error {
	return writeBatchStdout(ctx, cmd.Root().Writer, func(out io.Writer) error { return readable.WriteText(out, message) })
}
