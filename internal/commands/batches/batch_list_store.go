package batches

import (
	"context"
	"hash/maphash"
	"strings"

	"github.com/tidwall/gjson"
)

func (s *batchRecordStore) fetchList(
	ctx context.Context, after string, maximum int64, fetchPage func(string) ([]byte, error),
	appendItem func(gjson.Result) error, rememberCursor func(string) (bool, error),
) error {
	if _, err := rememberCursor(after); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := fetchPage(after)
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
			if ctx.Err() != nil || maximum >= 0 && s.recordCount >= maximum {
				return false
			}
			id := item.Get("id")
			if !item.IsObject() || id.Type != gjson.String || id.Str == "" {
				itemErr = batchError("The API returned a list item without a valid ID. Results may be incomplete; retry the list operation.")
				return false
			}
			last = id.Str
			itemErr = appendItem(item)
			return itemErr == nil
		})
		if err := ctx.Err(); err != nil {
			return err
		}
		if itemErr != nil {
			return itemErr
		}
		if maximum >= 0 && s.recordCount >= maximum || !more.Bool() {
			return nil
		}
		seen, err := rememberCursor(last)
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

func (s *batchRecordStore) appendListBytes(data []byte) error {
	if err := s.write(s.data, data, s.position); err != nil {
		return err
	}
	s.position += int64(len(data))
	return nil
}

// The page validator already checked this object. Watch snapshots compact JSON
// whitespace. Input records preserve raw bytes for explicit raw output and extraction.
func (s *batchRecordStore) appendListRecord(raw string, compact bool) error {
	start := s.position
	var chunk [32 * 1024]byte
	used := 0
	quoted, escaped := false, false
	for index := 0; index < len(raw); index++ {
		if index%4096 == 0 {
			if err := s.ctx.Err(); err != nil {
				return err
			}
		}
		value := raw[index]
		if compact && !quoted && (value == ' ' || value == '\t' || value == '\r' || value == '\n') {
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
			if err := s.appendListBytes(chunk[:]); err != nil {
				return err
			}
			used = 0
		}
	}
	if err := s.appendListBytes(chunk[:used]); err != nil {
		return err
	}
	id := s.recordCount + 1
	if err := s.saveRecord(id, batchStoredRecord{start: start, length: s.position - start}); err != nil {
		return err
	}
	s.recordCount = id
	return nil
}

func (s *batchRecordStore) rememberListCursor(cursor string) (seen bool, err error) {
	if cursor == "" {
		return false, nil
	}
	hash := maphash.String(s.seed, cursor)
	head, err := s.bucket(hash)
	if err != nil {
		return false, err
	}
	for id := head; id != 0; {
		group, err := s.group(id)
		if err != nil {
			return false, err
		}
		equal, err := s.equalKey(group, cursor)
		if err != nil || equal {
			return equal, err
		}
		id = group.next
	}
	start := s.position
	for offset := 0; offset < len(cursor); {
		end := min(offset+32*1024, len(cursor))
		if err := s.appendListBytes([]byte(cursor[offset:end])); err != nil {
			return false, err
		}
		offset = end
	}
	id := s.groupCount + 1
	if err := s.saveGroup(id, batchStoredGroup{next: head, keyStart: start, keyLength: int64(len(cursor))}); err != nil {
		return false, err
	}
	if err := s.saveBucket(hash, id); err != nil {
		return false, err
	}
	s.groupCount = id
	return false, s.grow()
}
