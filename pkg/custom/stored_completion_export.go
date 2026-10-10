package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Register before subgroup cloning so both command paths have identical behavior.
func registerStoredCompletionExport(root *cli.Command) {
	resource := root.Command("chat:completions")
	if resource == nil || resource.Command("export") != nil {
		return
	}
	const description = "Export every matching stored completion as JSONL. Only requests created with store=true are available.\n" +
		"Records preserve the returned completion, including choices and metadata. Original input messages are not included.\n" +
		"Existing destinations are never overwritten. Use --output - for stdout; failures can leave partial stdout."
	resource.Commands = append(resource.Commands, &cli.Command{
		Name: "export", Usage: "Export all matching stored Chat Completions to JSONL.",
		Description: description, HideHelpCommand: true, Suggest: true,
		Metadata: map[string]any{"help-content": clihelp.Content{
			Description: description,
			Examples:    []clihelp.Example{{Description: "Export stored completions:", Command: "chat completions export --model model-demo --output completions.jsonl"}},
		}},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Required: true, Usage: "New JSONL file, or - for stdout. Existing paths are rejected."},
			&requestflag.Flag[string]{Name: "after", QueryPath: "after", Usage: "Start after this completion ID; export every following matching page."},
			&requestflag.Flag[int64]{Name: "limit", QueryPath: "limit", Default: 20, Usage: "Completions per API page. This does not limit the total export."},
			&requestflag.Flag[map[string]any]{Name: "metadata", QueryPath: "metadata", Usage: "Filter by metadata key-value pairs, as JSON or YAML."},
			&requestflag.Flag[string]{Name: "model", QueryPath: "model", Usage: "Filter by the model used to generate completions."},
			&requestflag.Flag[string]{Name: "order", QueryPath: "order", Default: "asc", Usage: "Timestamp order: asc or desc. Defaults to asc."},
		},
		Action: handleStoredCompletionExport,
	})
}

// Only static prose and a completed-record count enter readable diagnostics.
// Keep the cause available for API error formats, cancellation, and exit status.
type storedCompletionExportError struct {
	message string
	cause   error
}

func (e *storedCompletionExportError) Error() string {
	if errors.Is(e.cause, errStoredCompletionPage) {
		return e.message + "\nThe API returned incomplete or non-advancing pagination. Retry the export; report repeated failures."
	}
	return e.message
}
func (e *storedCompletionExportError) Unwrap() error { return e.cause }
func (e *storedCompletionExportError) ExitCode() int {
	var interrupted *downloadSignalError
	if errors.As(e.cause, &interrupted) {
		return interrupted.exitCode
	}
	if errors.Is(e.cause, context.Canceled) {
		return 130
	}
	return 1
}

var errStoredCompletionPage = errors.New("invalid completion pagination response")

func storedCompletionExportMessage(failure error, exportFailure *storedCompletionExportError) string {
	var timeout net.Error
	prefix := ""
	switch {
	case errors.Is(failure, context.Canceled):
		prefix = "Request canceled.\n"
	case errors.Is(failure, context.DeadlineExceeded), errors.As(failure, &timeout) && timeout.Timeout():
		prefix = "The request timed out.\n"
	}
	return prefix + exportFailure.Error()
}

func handleStoredCompletionExport(ctx context.Context, command *cli.Command) error {
	if command.Args().Present() {
		return &storedCompletionExportError{message: "Export takes no positional arguments. Use --output PATH or --output -."}
	}
	root := command.Root()
	if format := strings.ToLower(root.String("format")); format != "" && format != "auto" && format != "jsonl" ||
		root.String("transform") != "" || root.Bool("raw-output") {
		return &storedCompletionExportError{message: "Export writes complete JSONL records. Use chat completions list for other formats or extraction."}
	}
	path := command.String("output")
	if path == "" {
		return &storedCompletionExportError{message: "Export requires a destination. Use --output PATH or --output -."}
	}
	options, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, false)
	if err != nil {
		return err
	}
	client := openai.NewClient(GetDefaultRequestOptions(command)...)
	var count int64
	write := func(ctx context.Context, out io.Writer, interruptRequests bool) error {
		var err error
		count, err = writeStoredCompletionExport(ctx, &client, options, out, interruptRequests)
		return err
	}
	if path == "-" {
		// Protect real stdout from SIGPIPE without suppressing export failures.
		if root.Writer == os.Stdout {
			err = streamToStdout(func(out *os.File) error { return write(ctx, out, true) })
		} else {
			err = write(ctx, root.Writer, true)
		}
		if err != nil {
			return &storedCompletionExportError{
				fmt.Sprintf("Export incomplete after %d complete records. Stdout may contain a partial final record. Retry to a new destination.", count),
				errors.Join(err, downloadContextError(ctx)),
			}
		}
	} else {
		// File mode owns a staging file until cleanup finishes. Stdout and
		// receipts retain normal signal handling while a consumer blocks writes.
		err = func() error {
			saveCtx, stop := downloadSignalContext(ctx)
			defer stop()
			return saveStoredCompletionExport(saveCtx, path, func(out io.Writer) error { return write(saveCtx, out, false) })
		}()
		if err != nil {
			var failure *storedCompletionExportError
			if errors.As(err, &failure) {
				failure.message += fmt.Sprintf("\nComplete records processed: %d.", count)
			}
			return err
		}
	}
	message := fmt.Sprintf("Stored completions: %d\nAll pages fetched.\n", count)
	if path != "-" {
		displayPath := path
		if strings.ContainsAny(path, "\n\t") {
			displayPath = strconv.Quote(path)
		}
		message = "Saved " + readable.Text(displayPath) + "\n" + message
	}
	if err := writeOutputReceipt(ctx, message); err != nil {
		return &storedCompletionExportError{message: "Export completed, but its receipt could not be written. The exported data was retained.", cause: err}
	}
	return nil
}

func writeStoredCompletionExport(ctx context.Context, client *openai.Client, options []option.RequestOption, out io.Writer, interruptRequests bool) (count int64, err error) {
	// Brent's cursor-cycle check uses constant memory, even for unlimited exports.
	var checkpoint string
	var distance uint64
	power := uint64(1)
	after := ""
	for {
		if err = downloadContextError(ctx); err != nil {
			return count, err
		}
		pageOptions := options
		if after != "" {
			pageOptions = append(append([]option.RequestOption(nil), options...), option.WithQuery("after", after))
		}
		requestCtx, stop := ctx, func() {}
		if interruptRequests {
			requestCtx, stop = downloadSignalContext(ctx)
		}
		page, requestErr := client.Chat.Completions.List(requestCtx, openai.ChatCompletionListParams{}, pageOptions...)
		requestErr = errors.Join(requestErr, downloadContextError(requestCtx))
		stop()
		// stop cancels its child even on success. Only a signal cause remains
		// meaningful after teardown; retain a signal received at that boundary.
		var interrupted *downloadSignalError
		if errors.As(context.Cause(requestCtx), &interrupted) {
			requestErr = errors.Join(requestErr, interrupted)
		}
		if requestErr != nil {
			return count, requestErr
		}
		if page == nil {
			return count, errStoredCompletionPage
		}
		data, more := gjson.Get(page.RawJSON(), "data"), gjson.Get(page.RawJSON(), "has_more")
		if !data.IsArray() || (more.Type != gjson.True && more.Type != gjson.False) || len(page.Data) == 0 && page.HasMore {
			return count, errStoredCompletionPage
		}
		if len(page.Data) > 0 {
			cursor := page.Data[len(page.Data)-1].ID
			if cursor == "" || page.HasMore && cursor == checkpoint {
				return count, errStoredCompletionPage
			}
			if checkpoint == "" || distance == power {
				checkpoint, distance = cursor, 0
				if power <= ^uint64(0)/2 {
					power *= 2
				}
			}
			distance++
		}
		for _, completion := range page.Data {
			if completion.ID == "" || !gjson.Parse(completion.RawJSON()).IsObject() {
				return count, errStoredCompletionPage
			}
			complete, writeErr := writeStoredCompletionRecord(ctx, out, completion.RawJSON())
			if complete {
				count++
			}
			if writeErr != nil {
				return count, writeErr
			}
		}
		if !page.HasMore {
			return count, downloadContextError(ctx)
		}
		after = page.Data[len(page.Data)-1].ID
	}
}

// The SDK validates the JSON. Remove only whitespace outside strings, preserving
// unknown fields, duplicate keys, number spelling, escapes, and field order.
// A fixed buffer keeps writes and cancellation checks independent of record size.
func writeStoredCompletionRecord(ctx context.Context, out io.Writer, raw string) (bool, error) {
	buffer := make([]byte, 0, 32*1024)
	quoted, escaped := false, false
	flush := func() (bool, error) {
		if err := downloadContextError(ctx); err != nil {
			return false, err
		}
		n, err := out.Write(buffer)
		complete := n == len(buffer)
		if n != len(buffer) && err == nil {
			err = io.ErrShortWrite
		}
		buffer = buffer[:0]
		return complete, err
	}
	for i := 0; i < len(raw); i++ {
		if i%(32*1024) == 0 {
			if err := downloadContextError(ctx); err != nil {
				return false, err
			}
		}
		b := raw[i]
		if !quoted && (b == ' ' || b == '\t' || b == '\r' || b == '\n') {
			continue
		}
		buffer = append(buffer, b)
		if escaped {
			escaped = false
		} else if quoted && b == '\\' {
			escaped = true
		} else if b == '"' {
			quoted = !quoted
		}
		if len(buffer) == cap(buffer) {
			if _, err := flush(); err != nil {
				return false, err
			}
		}
	}
	buffer = append(buffer, '\n')
	return flush()
}

// Reuse the download stage's private permissions, directory pinning, exclusive
// publication, fallback copy, and owned-file cleanup. Existing files are refused.
func saveStoredCompletionExport(ctx context.Context, path string, write func(io.Writer) error) (err error) {
	outcome := "Export could not start. No destination file was created. Check the destination directory and permissions."
	defer func() {
		if err != nil {
			err = &storedCompletionExportError{outcome, errors.Join(err, downloadContextError(ctx))}
		}
	}()
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			outcome = "Export did not start. The destination already exists. Choose a new --output path."
			err = os.ErrExist
		}
		return err
	}
	directory, name := filepath.Split(path)
	if directory == "" {
		directory = "."
	}
	stage, err := newDownloadStage(directory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.cleanup()) }()
	outcome = "Export incomplete. No destination file was created. Check the error and retry to a new destination."
	if err = write(stage.file); err != nil {
		return err
	}
	if err = errors.Join(stage.file.Sync(), stage.closeWriter(), downloadContextError(ctx)); err != nil {
		return err
	}
	parent, parentErr := os.Stat(directory)
	pinned, pinnedErr := stage.root.Stat(".")
	if parentErr != nil || pinnedErr != nil || !os.SameFile(parent, pinned) {
		return errors.Join(errDownloadDestinationChanged, parentErr, pinnedErr)
	}
	if err = stage.verify(); err != nil {
		return err
	}
	saved, incomplete, err := publishNewDownload(ctx, stage, name, stage.root.Link)
	if saved != nil {
		outcome = "Export finished, but final verification or cleanup failed. The saved file may remain. Check the destination."
	} else if incomplete {
		outcome = "Export incomplete. The destination may contain partial output. Check it before retrying to a new destination."
	}
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(current, saved) {
		return errors.Join(errDownloadDestinationChanged, err)
	}
	return nil
}
