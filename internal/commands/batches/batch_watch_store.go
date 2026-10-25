package batches

import (
	"context"
	"errors"
	"hash/maphash"
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
	if _, err := s.rememberCursor(after); err != nil {
		return err
	}
	for {
		if err := s.store.ctx.Err(); err != nil {
			return err
		}
		raw, err := fetch(after)
		if err != nil {
			return err
		}
		if !gjson.ValidBytes(raw) {
			return batchError("The API returned invalid list JSON. Results may be incomplete; retry the list operation.")
		}
		page := gjson.ParseBytes(raw)
		data, more := page.Get("data"), page.Get("has_more")
		if !page.IsObject() || !data.IsArray() || more.Type != gjson.True && more.Type != gjson.False {
			return batchError("The API returned an unfamiliar list response. Results may be incomplete; retry the list operation.")
		}
		var last string
		var itemErr error
		data.ForEach(func(_, item gjson.Result) bool {
			if s.store.ctx.Err() != nil || maximum >= 0 && s.store.recordCount >= maximum {
				return false
			}
			id := item.Get("id")
			if !item.IsObject() || id.Type != gjson.String || id.Str == "" {
				itemErr = batchError("The API returned a list item without a valid ID. Results may be incomplete; retry the list operation.")
				return false
			}
			last = id.Str
			itemErr = s.appendRecord(item.Raw)
			return itemErr == nil
		})
		if err := s.store.ctx.Err(); err != nil {
			return err
		}
		if itemErr != nil {
			return itemErr
		}
		if maximum >= 0 && s.store.recordCount >= maximum || !more.Bool() {
			return nil
		}
		seen, err := s.rememberCursor(last)
		if err != nil {
			return err
		}
		if last == "" || seen {
			return batchError("List pagination stopped because the API returned a missing or repeated cursor. Results may be incomplete.\nRetry from the first page with the same search, connection, and project settings.")
		}
		// gjson strings can reference the whole page. Retain only the next cursor.
		after = strings.Clone(last)
	}
}

func (s *batchWatchSnapshot) appendBytes(data []byte) error {
	if err := s.store.write(s.store.data, data, s.store.position); err != nil {
		return batchWatchStorageError(err)
	}
	s.store.position += int64(len(data))
	return nil
}

// The page validator already checked this object. Remove only JSON whitespace
// outside strings, preserving number lexemes, escapes, duplicate keys, and UTF-8.
func (s *batchWatchSnapshot) appendRecord(raw string) error {
	start := s.store.position
	var chunk [32 * 1024]byte
	used := 0
	quoted, escaped := false, false
	for index := 0; index < len(raw); index++ {
		if index%4096 == 0 {
			if err := s.store.ctx.Err(); err != nil {
				return err
			}
		}
		value := raw[index]
		if !quoted && (value == ' ' || value == '\t' || value == '\r' || value == '\n') {
			continue
		}
		chunk[used] = value
		used++
		if quoted {
			if escaped {
				escaped = false
			} else if value == '\\' {
				escaped = true
			} else if value == '"' {
				quoted = false
			}
		} else if value == '"' {
			quoted = true
		}
		if used == len(chunk) {
			if err := s.appendBytes(chunk[:]); err != nil {
				return err
			}
			used = 0
		}
	}
	if err := s.appendBytes(chunk[:used]); err != nil {
		return err
	}
	id := s.store.recordCount + 1
	if err := s.store.saveRecord(id, batchStoredRecord{start: start, length: s.store.position - start}); err != nil {
		return batchWatchStorageError(err)
	}
	s.store.recordCount = id
	return nil
}

func (s *batchWatchSnapshot) rememberCursor(cursor string) (seen bool, err error) {
	defer func() { err = batchWatchStorageError(err) }()
	if cursor == "" {
		return false, nil
	}
	hash := maphash.String(s.store.seed, cursor)
	head, err := s.store.bucket(hash)
	if err != nil {
		return false, err
	}
	for id := head; id != 0; {
		group, err := s.store.group(id)
		if err != nil {
			return false, err
		}
		equal, err := s.store.equalKey(group, cursor)
		if err != nil || equal {
			return equal, err
		}
		id = group.next
	}
	start := s.store.position
	for offset := 0; offset < len(cursor); {
		end := min(offset+32*1024, len(cursor))
		if err := s.appendBytes([]byte(cursor[offset:end])); err != nil {
			return false, err
		}
		offset = end
	}
	id := s.store.groupCount + 1
	if err := s.store.saveGroup(id, batchStoredGroup{next: head, keyStart: start, keyLength: int64(len(cursor))}); err != nil {
		return false, err
	}
	if err := s.store.saveBucket(hash, id); err != nil {
		return false, err
	}
	s.store.groupCount = id
	return false, s.store.grow()
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
