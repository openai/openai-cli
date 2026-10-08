package custom

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func newBatchDownloadCommand() *cli.Command {
	return &cli.Command{
		Name: "download", Usage: "Download a batch output, error, or input file",
		Description:     "Downloads exact file bytes without overwriting existing files. Use --output - for stdout. Output formats affect receipts and errors, never file bytes.",
		HideHelpCommand: true, Suggest: true,
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: "batch-id", PathParam: "batch_id", Required: true},
			&cli.StringFlag{Name: "file", Value: "output", Usage: "Select output, error, or input"},
			&cli.StringFlag{Name: "output", Required: true, TakesFile: true, Usage: "Save to a new file, or use - for exact bytes on stdout"},
		},
		Action: handleBatchDownload,
	}
}

func handleBatchDownload(ctx context.Context, command *cli.Command) (err error) {
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
	options, err := batchRequestOptions(command)
	if err != nil {
		return err
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
	client := openai.NewClient(GetDefaultRequestOptions(command)...)
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
			_, copyErr := io.Copy(outputWriter{ctx: ctx, out: out}, response.Body)
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
	return batchShowJSON(ctx, command, gjson.ParseBytes(receipt), "download")
}

// The directory handle anchors every staging and publication operation. A hard
// link publishes complete bytes without replacing an existing destination.
func writeBatchDownload(ctx context.Context, path string, body io.ReadCloser) (written int64, err error) {
	bodyOpen := true
	defer func() {
		if bodyOpen {
			err = errors.Join(err, body.Close())
		}
	}()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	directory, name := filepath.Dir(path), filepath.Base(path)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return 0, batchDownloadFailure("Could not open the output directory. Check --output and directory permissions.", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	parent, err := root.Stat(".")
	if err != nil {
		return 0, batchDownloadFailure("Could not inspect the output directory.", err)
	}
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return 0, batchDownloadFailure("The output path already exists or cannot be checked. Choose a new --output path.", err)
	}
	temporary := ".openai-batch-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
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
	written, copyErr := io.Copy(outputWriter{ctx: ctx, out: file}, body)
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
	return &batchWorkflowError{message: message, code: 1, cause: cause}
}
