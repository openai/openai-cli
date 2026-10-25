package batches

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func (w workflow) newBatchInputsCommand() *cli.Command {
	command := &cli.Command{
		Name: "inputs", Usage: "Find uploaded Batch input files or select one",
		Description:     "Lists files with purpose=batch, newest first. Search matches literal filename or ID text. Selection returns File metadata without creating a batch. Use --format-error text with --select and machine output. Selection excludes --quiet, --debug, and --transform-error.",
		HideHelpCommand: true, Suggest: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "search", Usage: "Case-insensitive filename or file ID text"},
			&cli.Int64Flag{Name: "max-items", Value: 100, Usage: "Maximum matching files to return; -1 returns all matches"},
			&cli.BoolFlag{Name: "select", Usage: "Select one file interactively; requires terminal stdin and stderr"},
		},
		Action: w.listBatchInputs,
	}
	command.Metadata = map[string]any{"help-content": clihelp.Content{
		Description: command.Description,
		Examples:    []clihelp.Example{{Description: "Find uploaded input files:", Command: "batches inputs --search requests.jsonl"}},
	}}
	return command
}

func (w workflow) listBatchInputs(ctx context.Context, cmd *cli.Command) (err error) {
	if cmd.Args().Len() != 0 {
		return batchError("Unexpected extra arguments. Use --search to find Batch input files.")
	}
	maximum := cmd.Int64("max-items")
	if maximum < -1 {
		return batchError("--max-items must be -1, zero, or positive.")
	}
	if cmd.Bool("select") {
		root := cmd.Root()
		if !batchInputSelectionAllowed(root, term.IsTerminal(os.Stdin.Fd()), term.IsTerminal(os.Stderr.Fd())) {
			return batchError("File selection requires terminal stdin and stderr without --quiet, --debug, or --transform-error. Use --format-error text with machine output, or omit --select.")
		}
		if maximum == 0 {
			return batchError("Use a positive --max-items value or -1 with --select.")
		}
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
	search := strings.ToLower(cmd.String("search"))
	items, fetchErr := batchFetchPages(ctx, "", maximum, func(after string) ([]byte, error) {
		var raw []byte
		opts := append(options[:len(options):len(options)], option.WithQuery("purpose", "batch"), option.WithQuery("order", "desc"), option.WithQuery("limit", "100"), option.WithResponseBodyInto(&raw))
		if after != "" {
			opts = append(opts, option.WithQuery("after", after))
		} else {
			opts = append(opts, option.WithQueryDel("after"))
		}
		_, err := client.Files.List(ctx, openai.FileListParams{}, opts...)
		return raw, err
	}, func(file gjson.Result) bool {
		return file.Get("purpose").Str == "batch" && (search == "" || strings.Contains(strings.ToLower(file.Get("filename").Str), search) || strings.Contains(strings.ToLower(file.Get("id").Str), search))
	})
	if cmd.Bool("select") {
		if fetchErr != nil {
			return fetchErr
		}
		if len(items) == 0 {
			return batchError("No matching Batch input files were found. Change --search or upload a file with files upload --purpose batch.")
		}
		selected, err := runBatchInputPicker(ctx, items)
		if err != nil {
			return err
		}
		return w.batchShowJSON(ctx, cmd, selected, "inputs")
	}
	if fetchErr != nil && len(items) == 0 {
		return fetchErr
	}
	value, err := batchRawArray(ctx, items)
	if err != nil {
		return errors.Join(fetchErr, err)
	}
	if fetchErr != nil {
		message := "Input file listing stopped. Displayed files may be incomplete."
		var apierr *openai.Error
		var workflowErr *WorkflowError
		if errors.As(fetchErr, &apierr) && w.options.APIErrorMessage != nil {
			message += "\n" + w.options.APIErrorMessage(cmd.Root(), fetchErr, apierr)
		} else if errors.As(fetchErr, &workflowErr) {
			message += "\n" + workflowErr.Error()
		}
		message += "\nRetry batches inputs with the same search, connection, and project settings."
		fetchErr = &WorkflowError{message: message, cause: fetchErr}
	}
	return errors.Join(fetchErr, w.batchShowJSON(ctx, cmd, value, "inputs"))
}

func batchInputSelectionAllowed(root *cli.Command, inputTerminal, errorTerminal bool) bool {
	format := root.String("format")
	if root.IsSet("format-error") {
		format = root.String("format-error")
	}
	format = strings.ToLower(format)
	return inputTerminal && errorTerminal && root.String("transform-error") == "" && !root.Bool("quiet") && !root.Bool("debug") && (format == "" || format == "auto" || format == "text")
}

// batchFetchPages preserves complete records and returns partial results with failures.
// The caller decides whether partial records can be printed or selected.
func batchFetchPages(ctx context.Context, after string, maximum int64, fetch func(string) ([]byte, error), keep func(gjson.Result) bool) ([]gjson.Result, error) {
	var items []gjson.Result
	seen := map[string]bool{after: true}
	for {
		if err := ctx.Err(); err != nil {
			return items, err
		}
		raw, err := fetch(after)
		if err != nil {
			return items, err
		}
		if !gjson.ValidBytes(raw) {
			return items, batchError("The API returned invalid list JSON. Results may be incomplete; retry the list operation.")
		}
		page := gjson.ParseBytes(raw)
		data, more := page.Get("data"), page.Get("has_more")
		if !page.IsObject() || !data.IsArray() || more.Type != gjson.True && more.Type != gjson.False {
			return items, batchError("The API returned an unfamiliar list response. Results may be incomplete; retry the list operation.")
		}
		var last string
		var invalid bool
		data.ForEach(func(_, item gjson.Result) bool {
			if ctx.Err() != nil || maximum >= 0 && int64(len(items)) >= maximum {
				return false
			}
			id := item.Get("id")
			if !item.IsObject() || id.Type != gjson.String || id.Str == "" {
				invalid = true
				return false
			}
			last = id.Str
			if keep == nil || keep(item) {
				items = append(items, item)
			}
			return true
		})
		if err := ctx.Err(); err != nil {
			return items, err
		}
		if invalid {
			return items, batchError("The API returned a list item without a valid ID. Results may be incomplete; retry the list operation.")
		}
		if maximum >= 0 && int64(len(items)) >= maximum || !more.Bool() {
			return items, nil
		}
		if last == "" || seen[last] {
			return items, batchError("List pagination stopped because the API returned a missing or repeated cursor. Results may be incomplete.\nRetry from the first page with the same search, connection, and project settings.")
		}
		seen[last] = true
		after = last
	}
}

func batchRawArray(ctx context.Context, items []gjson.Result) (gjson.Result, error) {
	var raw strings.Builder
	raw.WriteByte('[')
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, err
		}
		if i != 0 {
			raw.WriteByte(',')
		}
		raw.WriteString(item.Raw)
	}
	raw.WriteByte(']')
	return gjson.Parse(raw.String()), ctx.Err()
}
