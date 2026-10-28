package batches

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/tidwall/gjson"
)

// A snapshot stores records and visited cursors on disk. Only the current API
// page and one readable record need memory, regardless of the number of pages.
type batchWatchSnapshot struct{ store *batchRecordStore }

func batchWatchStorageError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &WorkflowError{message: "Batch list watching stopped. Retry batches list --watch with the same connection and project settings.", cause: err}
}

func (s *batchWatchSnapshot) fetch(after string, maximum int64, fetch func(string) ([]byte, error)) error {
	return s.store.fetchList(s.store.ctx, after, maximum, fetch, func(item gjson.Result) error {
		return s.appendRecord(item.Raw)
	}, s.rememberCursor)
}

func (s *batchWatchSnapshot) appendBytes(data []byte) error {
	return batchWatchStorageError(s.store.appendListBytes(data))
}

func (s *batchWatchSnapshot) appendRecord(raw string) error {
	return batchWatchStorageError(s.store.appendListRecord(raw, true))
}

func (s *batchWatchSnapshot) rememberCursor(cursor string) (bool, error) {
	seen, err := s.store.rememberListCursor(cursor)
	return seen, batchWatchStorageError(err)
}

func (s *batchWatchSnapshot) writeRecord(out io.Writer, id int64) error {
	record, err := s.store.record(id)
	if err != nil {
		return batchWatchStorageError(err)
	}
	var buffer [32 * 1024]byte
	source := batchRecordContextReader{s.store.ctx, io.NewSectionReader(s.store.data, record.start, record.length)}
	written, err := io.CopyBuffer(out, batchWatchRecordReader{source}, buffer[:])
	if err == nil && written != record.length {
		return batchWatchStorageError(batchRecordIncompleteError())
	}
	return err
}

// Classify source failures without changing errors from the output writer.
type batchWatchRecordReader struct{ io.Reader }

func (r batchWatchRecordReader) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		err = batchWatchStorageError(err)
	}
	return n, err
}

func (s *batchWatchSnapshot) item(id int64) (gjson.Result, error) {
	var raw strings.Builder
	if err := s.writeRecord(&raw, id); err != nil {
		return gjson.Result{}, batchWatchStorageError(err)
	}
	return gjson.Parse(raw.String()), nil
}
