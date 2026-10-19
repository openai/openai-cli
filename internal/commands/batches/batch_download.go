package batches

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func (w workflow) newBatchDownloadCommand() *cli.Command {
	return &cli.Command{
		Name: "download", Usage: "Download a batch output, error, or input file",
		Description:     "Downloads exact file bytes without overwriting existing files. Use --output - for stdout. Output formats affect receipts and errors, never file bytes.",
		HideHelpCommand: true, Suggest: true,
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: "batch-id", PathParam: "batch_id", Required: true},
			&cli.StringFlag{Name: "file", Value: "output", Usage: "Select output, error, or input"},
			&cli.StringFlag{Name: "output", Required: true, TakesFile: true, Usage: "Save to a new file, or use - for exact bytes on stdout"},
		},
		Action: w.handleBatchDownload,
	}
}

func (w workflow) handleBatchDownload(ctx context.Context, command *cli.Command) (err error) {
	selection := command.String("file")
	if selection != "output" && selection != "error" && selection != "input" {
		return batchError("Choose --file output, error, or input.")
	}
	path := command.String("output")
	if path == "" {
		return batchError("Add --output with a new file path, or - for stdout.")
	}
	if os.IsPathSeparator(path[len(path)-1]) {
		return batchError("Choose an output file path without a trailing path separator.")
	}
	ctx, stopSignals := batchSignalContext(ctx)
	stopReset := context.AfterFunc(ctx, stopSignals)
	defer func() {
		contextErr := errors.Join(ctx.Err(), context.Cause(ctx))
		stopReset()
		stopSignals()
		if errors.Is(contextErr, context.Canceled) {
			err = batchContextError(errors.Join(err, contextErr))
		}
	}()
	options, err := w.batchRequestOptions(ctx, command)
	if err != nil {
		return err
	}
	client := openai.NewClient(w.options.DefaultRequestOptions(command)...)
	batch, err := client.Batches.Get(ctx, command.String("batch-id"), options...)
	if err != nil {
		return err
	}
	if batch == nil {
		return batchError("The API returned no batch object. Retrieve the batch again to check its file IDs.")
	}
	fileID := batch.OutputFileID
	if selection == "error" {
		fileID = batch.ErrorFileID
	} else if selection == "input" {
		fileID = batch.InputFileID
	}
	if fileID == "" {
		return batchError("The batch has no selected file available. Retrieve the batch to check its status and file IDs.")
	}
	response, err := client.Files.Content(ctx, fileID, options...)
	if err != nil {
		return err
	}
	if path == "-" {
		copyErr := writeBatchStdout(ctx, command.Root().Writer, func(out io.Writer) error {
			_, copyErr := io.Copy(clioutput.Writer{Context: ctx, Sink: out}, response.Body)
			return copyErr
		})
		return errors.Join(copyErr, response.Body.Close(), ctx.Err())
	}
	written, err := writeBatchDownload(ctx, path, response.Body)
	if err != nil {
		return err
	}
	receipt, err := json.Marshal(struct {
		BatchID string `json:"batch_id"`
		File    string `json:"file"`
		FileID  string `json:"file_id"`
		Output  string `json:"output"`
		Bytes   int64  `json:"bytes"`
	}{batch.ID, selection, fileID, path, written})
	if err != nil {
		return err
	}
	return w.batchShowJSON(ctx, command, gjson.ParseBytes(receipt), "download")
}

// The directory handle anchors every staging and publication operation. A hard
// link publishes complete bytes without replacing an existing destination.
func writeBatchDownload(ctx context.Context, path string, body io.ReadCloser) (written int64, err error) {
	directory, name := filepath.Split(path)
	if directory == "" {
		directory = "."
	}
	return writeBatchDownloadInDirectory(ctx, directory, name, body, nil)
}

func writeBatchDownloadInDirectory(ctx context.Context, directory, name string, body io.ReadCloser, expected os.FileInfo) (written int64, err error) {
	bodyOpen := true
	defer func() {
		if bodyOpen {
			err = errors.Join(err, body.Close())
		}
	}()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return 0, batchDownloadFailure("Could not open the output directory. Check --output and directory permissions.", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	parent, err := root.Stat(".")
	if err != nil {
		return 0, batchDownloadFailure("Could not inspect the output directory.", err)
	}
	if expected != nil && !os.SameFile(expected, parent) {
		return 0, batchError("The output directory changed. No output file was published.")
	}
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return 0, batchDownloadFailure("The output path already exists or cannot be checked. Choose a new --output path.", err)
	}
	temporary := ".openai-batch-" + rand.Text()
	file, err := clioutput.CreatePrivateDownloadFile(root, temporary)
	if err != nil {
		return 0, batchDownloadFailure("Could not prepare the output file. Check directory permissions and available space.", err)
	}
	owned, statErr := file.Stat()
	if statErr != nil {
		return 0, batchDownloadFailure("Could not inspect the temporary download file.", errors.Join(statErr, file.Close()))
	}
	fileOpen := true
	defer func() {
		if fileOpen {
			err = errors.Join(err, file.Close())
		}
		if cleanupErr := removeBatchDownloadTemporary(root, temporary, owned); cleanupErr != nil {
			err = errors.Join(err, batchDownloadFailure("Could not remove the temporary download file. Check the output directory.", cleanupErr))
		}
	}()
	// Keep downloaded results readable and writable by their owner even when
	// the process umask removes those permissions during private creation.
	if err := file.Chmod(0o600); err != nil {
		return 0, batchDownloadFailure("Could not prepare the output file permissions. No output file was published.", err)
	}
	written, copyErr := io.Copy(clioutput.Writer{Context: ctx, Sink: file}, body)
	bodyOpen = false
	bodyCloseErr := body.Close()
	fileOpen = false
	fileCloseErr := file.Close()
	if err := errors.Join(copyErr, bodyCloseErr, fileCloseErr, ctx.Err()); err != nil {
		return written, batchDownloadFailure("The download did not finish. No output file was published.", err)
	}
	if err := checkBatchDownloadIdentity(root, temporary, owned); err != nil {
		return written, batchDownloadFailure("The temporary download file changed. No output file was published.", err)
	}
	currentParent, err := os.Stat(directory)
	if err != nil || !os.SameFile(parent, currentParent) {
		return written, batchDownloadFailure("The output directory changed. No output file was published.", errors.Join(err, os.ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return written, err
	}
	if err := root.Link(temporary, name); err != nil {
		return written, batchDownloadFailure("Could not publish the output file without overwriting. Choose a new --output path.", err)
	}
	if err := checkBatchDownloadIdentity(root, name, owned); err != nil {
		return written, batchDownloadFailure("The output file changed after publication. Check the output directory before retrying.", err)
	}
	currentParent, err = os.Stat(directory)
	if err != nil || !os.SameFile(parent, currentParent) {
		return written, batchDownloadFailure("The output directory changed after publication. Check the saved file before retrying.", errors.Join(err, os.ErrInvalid))
	}
	return written, nil
}

func checkBatchDownloadIdentity(root *os.Root, name string, owned os.FileInfo) error {
	current, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(owned, current) {
		return os.ErrInvalid
	}
	return nil
}

func removeBatchDownloadTemporary(root *os.Root, name string, owned os.FileInfo) error {
	if err := checkBatchDownloadIdentity(root, name, owned); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return root.Remove(name)
}

func batchDownloadFailure(message string, cause error) error {
	return &WorkflowError{message: message, code: 1, cause: cause}
}

type batchOutputDirectory struct {
	path string
	info os.FileInfo
}

func prepareBatchOutputDirectory(ctx context.Context, path string) (*batchOutputDirectory, error) {
	if path == "" {
		return nil, batchError("Choose a nonempty --output-dir path.")
	}
	observed, err := createBatchOutputDirectory(ctx, path)
	if err != nil {
		return nil, batchDownloadFailure("Could not create the output directory. Check its parent and permissions.", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, batchDownloadFailure("Could not open the output directory. Check --output-dir and directory permissions.", err)
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return nil, batchDownloadFailure("Could not inspect the output directory.", err)
	}
	if observed != nil && !os.SameFile(observed, info) {
		return nil, batchError("The output directory changed. No output file was published.")
	}
	for _, name := range []string{"output.jsonl", "error.jsonl"} {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				err = os.ErrExist
			}
			return nil, batchDownloadFailure("A result path already exists or cannot be checked. Choose a new --output-dir. To recover a missing file, use batches download with the same batch ID, --file output or --file error, and a new --output path.", err)
		}
	}
	return &batchOutputDirectory{path: path, info: info}, nil
}

func createBatchOutputDirectory(ctx context.Context, path string) (os.FileInfo, error) {
	// Preserve the caller's path traversal and existing-directory permissions.
	observed, err := createPrivateBatchDirectory(ctx, path)
	if errors.Is(err, os.ErrExist) {
		return nil, nil
	}
	// Failure may leave an empty directory. A later observation cannot prove
	// creation ownership, so directory rollback would risk removing a replacement.
	return observed, err
}

func (w workflow) collectBatchFiles(ctx context.Context, cmd *cli.Command, client openai.Client, options []option.RequestOption, batch gjson.Result, destination *batchOutputDirectory) error {
	switch batch.Get("status").String() {
	case "completed", "failed", "expired", "cancelled":
	default:
		return nil
	}
	var failures []error
	var problems []string
	finish := func() error {
		if len(failures) == 0 {
			return nil
		}
		message := strings.Join(problems, "\n")
		if message == "" {
			message = "Could not finish displaying the download receipts."
		}
		return batchDownloadFailure(message+"\nAny completed files remain saved. Reuse the same batch ID and connection options; do not create another batch.", errors.Join(failures...))
	}
	for _, selection := range []string{"output", "error"} {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, finish())
		}
		value := batch.Get(selection + "_file_id")
		if value.Type == gjson.Null || value.Type == gjson.String && value.String() == "" {
			if err := w.batchCollectionReceipt(ctx, cmd, "No "+selection+" file returned."); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		if value.Type != gjson.String {
			message := "The batch returned an invalid " + selection + " file ID. Inspect the batch response before downloading."
			problems = append(problems, message)
			failures = append(failures, batchError(message))
			continue
		}
		response, err := client.Files.Content(ctx, value.String(), options...)
		name := selection + ".jsonl"
		if err == nil {
			_, err = writeBatchDownloadInDirectory(ctx, destination.path, name, response.Body, destination.info)
		}
		if err != nil {
			message := fmt.Sprintf("Could not save %s.jsonl. Retry with batches download --file %s --output NEW_PATH.", selection, selection)
			var local *WorkflowError
			var apierr *openai.Error
			switch {
			case errors.As(err, &local):
				message += "\n" + local.message
			case errors.As(err, &apierr):
				message += "\n" + w.options.APIErrorMessage(cmd.Root(), err, apierr)
			}
			problems = append(problems, message)
			failures = append(failures, err)
			continue
		}
		// Preserve filesystem traversal in the receipt, including drive-relative paths.
		path := destination.path
		if !os.IsPathSeparator(path[len(path)-1]) && !(filepath.VolumeName(path) == path && strings.HasSuffix(path, ":")) {
			path += string(os.PathSeparator)
		}
		if err := w.batchCollectionReceipt(ctx, cmd, fmt.Sprintf("Saved %q", path+name)); err != nil {
			failures = append(failures, err)
		}
	}
	return finish()
}

func (w workflow) batchCollectionReceipt(ctx context.Context, cmd *cli.Command, message string) error {
	if !w.options.HumanTerminal(ctx, cmd.Root()) {
		return nil
	}
	return writeBatchStdout(ctx, os.Stderr, func(out io.Writer) error {
		return readable.WriteText(out, message)
	})
}
