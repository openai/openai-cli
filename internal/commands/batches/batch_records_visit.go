package batches

import (
	"encoding/json"
	"io"
)

// visitRecords follows the same disk-backed order as writeJSON. A duplicate
// group exposes metadata followed by individual records, never a buffered group.
func (s *batchRecordStore) visitRecords(selection string, match bool, visit func(RecordItem) error) error {
	kind := "result"
	if selection == "input" {
		kind = "input"
	}
	if !match {
		for id := int64(1); id <= s.recordCount; id++ {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			if err := visit(RecordItem{Kind: kind, WriteJSON: func(out io.Writer) error { return s.writeRecord(out, id) }}); err != nil {
				return err
			}
		}
		return nil
	}
	for id := int64(1); id <= s.groupCount; id++ {
		group, err := s.group(id)
		if err != nil {
			return err
		}
		if err := visit(RecordItem{Kind: "group", WriteJSON: func(out io.Writer) error { return s.writeGroupMetadata(out, group) }}); err != nil {
			return err
		}
		for _, linked := range []struct {
			kind string
			head int64
		}{{"matched_result", group.resultHead}, {"matched_input", group.inputHead}} {
			for recordID := linked.head; recordID != 0; {
				currentID := recordID
				if err := visit(RecordItem{Kind: linked.kind, WriteJSON: func(out io.Writer) error { return s.writeRecord(out, currentID) }}); err != nil {
					return err
				}
				record, err := s.record(recordID)
				if err != nil {
					return err
				}
				recordID = record.next
			}
		}
	}
	return nil
}

func (s *batchRecordStore) writeGroupMetadata(out io.Writer, group batchStoredGroup) error {
	if _, err := io.WriteString(out, `{"custom_id":`); err != nil {
		return err
	}
	if err := s.writeKey(out, group); err != nil {
		return err
	}
	status, issues := describeBatchRecordCounts(group.keyLength != 0, group.inputCount, group.resultCount)
	metadata, err := json.Marshal(struct {
		Match   string   `json:"match"`
		Issues  []string `json:"issues,omitempty"`
		Inputs  int64    `json:"input_count"`
		Results int64    `json:"result_count"`
	}{status, issues, group.inputCount, group.resultCount})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(out, ","); err != nil {
		return err
	}
	_, err = out.Write(metadata[1:])
	return err
}
