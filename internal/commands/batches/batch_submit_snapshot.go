package batches

import (
	"context"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"syscall"

	requestconfig "github.com/openai/openai-cli/internal/request"
)

// A snapshot owns the private bytes inspected before submission. Upload never
// reopens the source path. FileInfo retains the original identity and timestamps.
type batchSubmissionSnapshot struct {
	os.FileInfo
	store               *batchRecordStore
	file                *os.File
	size                int64
	filename, mediaType string
}

func (s *batchSubmissionSnapshot) Size() int64  { return s.size }
func (s *batchSubmissionSnapshot) close() error { return s.store.close() }

type batchSnapshotWriter struct{ file *os.File }

func (w batchSnapshotWriter) Write(data []byte) (int, error) {
	n, err := w.file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, batchRecordStorageError(err)
}

func prepareBatchSubmissionSnapshot(ctx context.Context, path string) (snapshot *batchSubmissionSnapshot, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source := requestconfig.FileInputSource{Option: "--file"}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, source.Failure("open", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, source.Failure("close", closeErr))
		}
		if err != nil && snapshot != nil {
			err = errors.Join(err, snapshot.close())
			snapshot = nil
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, source.Failure("inspect", err)
	}
	if !info.Mode().IsRegular() {
		return nil, batchError("Submit requires a regular local JSONL file. Use files upload for other input sources.")
	}
	store, err := newBatchScratchStore(ctx)
	if err != nil {
		return nil, err
	}
	snapshot = &batchSubmissionSnapshot{FileInfo: info, store: store, filename: filepath.Base(path), mediaType: mime.TypeByExtension(filepath.Ext(path))}
	if snapshot.mediaType == "" {
		snapshot.mediaType = "application/octet-stream"
	}
	snapshot.file, _, err = store.temporaryFile()
	if err != nil {
		return snapshot, err
	}
	reader := &requestconfig.InputFileReader{Reader: batchRequestInput{ctx, file}, Source: source}
	var buffer [32 * 1024]byte
	snapshot.size, err = io.CopyBuffer(batchSnapshotWriter{snapshot.file}, reader, buffer[:])
	if err != nil {
		return snapshot, err
	}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	current, err := file.Stat()
	if err != nil {
		return snapshot, source.Failure("inspect", err)
	}
	if current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return snapshot, batchError("The input file changed while preparing its snapshot. Check the file, then retry.")
	}
	return snapshot, nil
}

func openBatchSubmissionFile(ctx context.Context, path string, snapshot *batchSubmissionSnapshot) (requestconfig.FileUpload, error) {
	if err := ctx.Err(); err != nil {
		return requestconfig.FileUpload{}, err
	}
	source := requestconfig.FileInputSource{Option: "--file"}
	info, err := os.Stat(path)
	if err != nil {
		return requestconfig.FileUpload{}, source.Failure("open", err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(snapshot.FileInfo, info) || snapshot.FileInfo.Size() != info.Size() || !snapshot.ModTime().Equal(info.ModTime()) {
		return requestconfig.FileUpload{}, batchError("The input file changed after review. Review the file again before creating the batch.")
	}
	upload, err := requestconfig.FileUploadFromFile(snapshot.file)
	if err != nil {
		return requestconfig.FileUpload{}, batchRecordStorageError(err)
	}
	upload.FileName, upload.MediaType = snapshot.filename, snapshot.mediaType
	// The snapshot owns its descriptor until submission finishes. This wrapper
	// checks cancellation and classifies private-storage failures without paths.
	upload.Reader = batchRecordContextReader{ctx, upload.Reader}
	return upload, nil
}
