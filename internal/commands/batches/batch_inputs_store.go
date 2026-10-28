package batches

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"strings"

	"github.com/tidwall/gjson"
)

// Input records, visited cursors, and picker matches use private disk storage.
// Fetching retains one API page. Selection reads only one complete record.
type batchInputCollection struct {
	store      *batchRecordStore
	matches    *os.File
	matchCount int64
}

func (s *batchInputCollection) fetch(ctx context.Context, maximum int64, fetch func(string) ([]byte, error), keep func(gjson.Result) bool) error {
	after := ""
	for {
		if err := ctx.Err(); err != nil {
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
			if ctx.Err() != nil || maximum >= 0 && s.store.recordCount >= maximum {
				return false
			}
			id := item.Get("id")
			if !item.IsObject() || id.Type != gjson.String || id.Str == "" {
				itemErr = batchError("The API returned a list item without a valid ID. Results may be incomplete; retry the list operation.")
				return false
			}
			last = id.Str
			if keep == nil || keep(item) {
				itemErr = s.store.appendListRecord(item.Raw, false)
			}
			return itemErr == nil
		})
		if err := ctx.Err(); err != nil {
			return err
		}
		if itemErr != nil {
			return itemErr
		}
		if maximum >= 0 && s.store.recordCount >= maximum || !more.Bool() {
			return nil
		}
		seen, err := s.store.rememberListCursor(last)
		if err != nil {
			return err
		}
		if last == "" || seen {
			return batchError("List pagination stopped because the API returned a missing or repeated cursor. Results may be incomplete.\nRetry from the first page with the same search, connection, and project settings.")
		}
		after = strings.Clone(last)
	}
}

func (s *batchInputCollection) writeJSON(out io.Writer) error {
	if err := s.store.ctx.Err(); err != nil {
		return err
	}
	if _, err := io.WriteString(out, "["); err != nil {
		return err
	}
	for id := int64(1); id <= s.store.recordCount; id++ {
		if err := s.store.ctx.Err(); err != nil {
			return err
		}
		if id != 1 {
			if _, err := io.WriteString(out, ","); err != nil {
				return err
			}
		}
		if err := s.store.writeRecord(out, id); err != nil {
			return err
		}
	}
	_, err := io.WriteString(out, "]")
	return err
}

func (s *batchInputCollection) item(ctx context.Context, id int64) (gjson.Result, error) {
	record, err := s.store.record(id)
	if err != nil {
		return gjson.Result{}, err
	}
	var raw strings.Builder
	var buffer [32 * 1024]byte
	reader := batchRecordContextReader{ctx, io.NewSectionReader(s.store.data, record.start, record.length)}
	written, err := io.CopyBuffer(&raw, reader, buffer[:])
	if err == nil && written != record.length {
		err = batchRecordIncompleteError()
	}
	if err != nil {
		return gjson.Result{}, err
	}
	return gjson.Parse(raw.String()), nil
}

func (s *batchInputCollection) filter(ctx context.Context, query string) (err error) {
	s.matchCount = 0
	defer func() {
		if err != nil {
			s.matchCount = 0
		}
	}()
	if s.matches == nil {
		s.matches, _, err = s.store.temporaryFile()
		if err != nil {
			return err
		}
	}
	if err := s.matches.Truncate(0); err != nil {
		return batchRecordStorageError(err)
	}
	query = strings.ToLower(query)
	for id := int64(1); id <= s.store.recordCount; id++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if query != "" {
			item, err := s.item(ctx, id)
			if err != nil {
				return err
			}
			if !strings.Contains(strings.ToLower(item.Get("filename").Str), query) && !strings.Contains(strings.ToLower(item.Get("id").Str), query) {
				continue
			}
		}
		var record [8]byte
		binary.LittleEndian.PutUint64(record[:], uint64(id))
		if err := s.store.write(s.matches, record[:], s.matchCount*8); err != nil {
			return err
		}
		s.matchCount++
	}
	return ctx.Err()
}

func (s *batchInputCollection) selected(ctx context.Context, index int64) (gjson.Result, error) {
	if index < 0 || index >= s.matchCount {
		return gjson.Result{}, batchRecordIncompleteError()
	}
	var data [8]byte
	if err := s.store.read(s.matches, data[:], index*8); err != nil {
		return gjson.Result{}, err
	}
	return s.item(ctx, int64(binary.LittleEndian.Uint64(data[:])))
}
