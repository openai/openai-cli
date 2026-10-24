package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// This control source also compiles against the unchanged baseline.
func xperfFramingControl(t *testing.T, source io.Reader) (*xperfCSVRejection, map[string]any, map[string]any, string) {
	t.Helper()
	reader := xperfReader{ctx: t.Context(), index: map[string]int{}}
	_, err := reader.scan(source, true)
	if err == nil || err.Error() != "csv_syntax" || reader.result.CSVRejection == nil {
		t.Fatal("strict CSV rejection was not preserved")
	}
	if reader.result.Accepted || reader.result.Complete {
		t.Fatal("rejected input acquired evidence acceptance")
	}
	data, err := json.Marshal(reader.result)
	if err != nil || len(data) > 16384 {
		t.Fatal("safe diagnostic encoding exceeded its control bound")
	}
	for _, private := range []string{"SECRET_FAMILY", "SECRET_CELL", "SECRET_PATH", "SECRET_READ_ERROR"} {
		if strings.Contains(string(data), private) {
			t.Fatal("private value escaped structural diagnostics")
		}
	}
	var record map[string]any
	if json.Unmarshal(data, &record) != nil {
		t.Fatal("diagnostic JSON is invalid")
	}
	rejection := record["csv_rejection"].(map[string]any)
	framing, ok := rejection["framing"].(map[string]any)
	if !ok {
		t.Fatal("CSV rejection omitted framing state")
	}
	shape, ok := framing["shape"].(map[string]any)
	if !ok {
		t.Fatal("CSV rejection omitted structural shape")
	}
	return reader.result.CSVRejection, framing, shape, string(data)
}

func xperfFramingNumber(t *testing.T, record map[string]any, key string, want int64) {
	t.Helper()
	if value, ok := record[key].(float64); !ok || value != float64(want) {
		t.Fatalf("wrong numeric framing field %s", key)
	}
}

func xperfFramingHeader() string {
	kinds := []string{"P-Start", "P-End", "P-DCStart", "P-DCEnd", "T-Start", "T-End", "T-DCStart", "T-DCEnd", "CSwitch", "ReadyThread", "SampledProfile", "Stack", "I-Start", "I-End", "I-DCStart", "I-DCEnd"}
	var output strings.Builder
	output.WriteString("BeginHeader\n")
	for _, kind := range kinds {
		output.WriteString(kind + ",TimeStamp,PID\n")
	}
	return output.String()
}

func TestOwnedWaitXperfCSVFramingControls(t *testing.T) {
	t.Run("unknown_kind_single_line_quote", func(t *testing.T) {
		header := xperfFramingHeader() + strings.Repeat("SECRET_FAMILY,ignored\n", 70) + "EndHeader\n"
		prefix := "SECRET_FAMILY,1,\""
		row := prefix + strings.Repeat("x", 53-len(prefix)-1) + "\"x\n"
		failure, framing, shape, _ := xperfFramingControl(t, strings.NewReader(header+row))
		if failure.Ordinal != 89 || failure.StartLine != 89 || failure.Line != 89 || failure.Column != 53 || failure.Fields != 2 || failure.Kind != "" || failure.Category != "quote" {
			t.Fatal("synthetic rejection did not match native coordinates")
		}
		if framing["header_state"] != "after" || framing["first_cell_category"] != "unknown" || shape["state"] != "observed" {
			t.Fatal("unknown record framing is incorrect")
		}
		xperfFramingNumber(t, framing, "begin_header_record", 1)
		xperfFramingNumber(t, framing, "end_header_record", 88)
		xperfFramingNumber(t, framing, "previous_record_offset", int64(len(header)))
		xperfFramingNumber(t, shape, "error_byte_offset", 52)
		classes := shape["before_at_after_coordinate_classes"].([]any)
		if len(classes) != 3 || classes[0] != "other" || classes[1] != "quote" || classes[2] != "other" {
			t.Fatal("quote-adjacent byte classes are incorrect")
		}
	})
	t.Run("all_schemas_before_header_end", func(t *testing.T) {
		_, framing, _, _ := xperfFramingControl(t, strings.NewReader(xperfFramingHeader()+"SECRET_FAMILY,1,SECRET_CELL\"x\n"))
		if framing["header_state"] != "inside" {
			t.Fatal("schema count incorrectly established EndHeader")
		}
		xperfFramingNumber(t, framing, "begin_header_record", 1)
		xperfFramingNumber(t, framing, "end_header_record", 0)
	})
	t.Run("before_header", func(t *testing.T) {
		_, framing, _, _ := xperfFramingControl(t, strings.NewReader("SECRET_FAMILY,1,SECRET_CELL\"x\n"))
		if framing["header_state"] != "before" {
			t.Fatal("unobserved header became observed")
		}
		xperfFramingNumber(t, framing, "begin_header_record", 0)
		xperfFramingNumber(t, framing, "end_header_record", 0)
	})
	for _, tc := range []struct{ name, cell, category string }{
		{"fixed_preamble", "OS Version: SECRET_CELL", "os_version_prefix"},
		{"preamble_near_match", "OS VersionX: SECRET_CELL", "unknown"},
		{"private_first_cell", "SECRET_PATH", "unknown"},
		{"known_event", "P-Start", "allowlisted_event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, framing, _, _ := xperfFramingControl(t, strings.NewReader("BeginHeader\nEndHeader\n"+tc.cell+",1,\"SECRET_CELL\"x\n"))
			if framing["first_cell_category"] != tc.category {
				t.Fatal("fixed first-cell classification is incorrect")
			}
		})
	}
	t.Run("clipped_slice", func(t *testing.T) {
		_, _, shape, _ := xperfFramingControl(t, strings.NewReader("SECRET_FAMILY,1,\""+strings.Repeat("x", 8192)+"\"x\n"))
		if shape["state"] != "clipped" || shape["error_position_state"] != "outside_slice" {
			t.Fatal("clipped slice falsely located the error")
		}
		xperfFramingNumber(t, shape, "slice_bytes", 4096)
		if _, exists := shape["error_byte_offset"]; exists {
			t.Fatal("clipped error has an invented offset")
		}
	})
	t.Run("multiline_with_blank_lines_and_crlf", func(t *testing.T) {
		header := "BeginHeader\r\nEndHeader\r\n"
		row := "\r\n\nSECRET_FAMILY,1,\"SECRET_CELL\r\nSECRET_PATH\"x\r\n"
		failure, _, shape, _ := xperfFramingControl(t, strings.NewReader(header+row))
		if failure.StartLine != 5 || failure.Line != 6 || shape["error_position_state"] != "mapped_candidate" {
			t.Fatal("multiline physical coordinates are incorrect")
		}
		xperfFramingNumber(t, shape, "error_byte_offset", int64(strings.Index(row, "\"x")))
		classes := shape["before_at_after_coordinate_classes"].([]any)
		if classes[1] != "quote" || classes[2] != "other" {
			t.Fatal("multiline error byte was mapped incorrectly")
		}
	})
	t.Run("missing_reader_at", func(t *testing.T) {
		source := struct{ io.Reader }{strings.NewReader("SECRET_FAMILY,1,SECRET_CELL\"x\n")}
		_, _, shape, _ := xperfFramingControl(t, source)
		if shape["state"] != "reader_at_unavailable" {
			t.Fatal("missing ReaderAt became observed shape")
		}
	})
	t.Run("same_handle_without_offset_change", func(t *testing.T) {
		header := "BeginHeader\nEndHeader\n"
		source := &xperfFramingReadAtControl{Reader: strings.NewReader(header + "SECRET_FAMILY,1,SECRET_CELL\"x\n")}
		_, _, shape, _ := xperfFramingControl(t, source)
		if source.calls != 1 || source.offset != int64(len(header)) || source.bytes > 4096 || source.moved || shape["state"] != "observed" {
			t.Fatal("snapshot did not preserve the original reader")
		}
	})
	t.Run("read_failure_redaction", func(t *testing.T) {
		source := &xperfFramingReadAtControl{Reader: strings.NewReader("SECRET_FAMILY,1,SECRET_CELL\"x\n"), fail: true}
		_, _, shape, _ := xperfFramingControl(t, source)
		if shape["state"] != "read_failed" {
			t.Fatal("failed snapshot became observed shape")
		}
	})
	t.Run("in_place_rewrite_is_not_parser_replay", func(t *testing.T) {
		input := "SECRET_FAMILY,1,SECRET_CELL\"x\n"
		file, err := os.CreateTemp(t.TempDir(), "snapshot")
		if err != nil {
			t.Fatal("owned snapshot creation failed")
		}
		t.Cleanup(func() { file.Close() })
		if _, err := file.WriteString(input); err != nil {
			t.Fatal("owned snapshot write failed")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			t.Fatal("owned snapshot seek failed")
		}
		source := &xperfFramingRewrittenFile{File: file, replacement: []byte(strings.Repeat("x", len(input)))}
		failure, _, shape, _ := xperfFramingControl(t, source)
		if failure.Category != "bare_quote" || shape["state"] != "observed" || shape["snapshot_basis"] != "same_handle_after_rejection" || shape["parser_bytes_verified"] != false {
			t.Fatal("post-rejection snapshot became verified parser input")
		}
		xperfFramingNumber(t, shape, "quote_count", 0)
		classes := shape["before_at_after_coordinate_classes"].([]any)
		if classes[1] != "other" || shape["error_position_state"] != "mapped_candidate" {
			t.Fatal("rewritten bytes lost their diagnostic-only classification")
		}
	})
	t.Run("position_array_bound", func(t *testing.T) {
		row := "SECRET_FAMILY,1,\"" + strings.Repeat("x,\"\"", 100) + "SECRET_CELL\"x\n"
		_, _, shape, _ := xperfFramingControl(t, strings.NewReader(row))
		if shape["positions_truncated"] != true || len(shape["quote_byte_offsets"].([]any)) != 64 || len(shape["comma_byte_offsets"].([]any)) != 64 {
			t.Fatal("position array did not preserve its bound")
		}
		if shape["quote_count"].(float64) <= 64 || shape["comma_count"].(float64) <= 64 {
			t.Fatal("position truncation hid aggregate counts")
		}
	})
	t.Run("valid_input_never_snapshots", func(t *testing.T) {
		source := &xperfFramingReadAtControl{Reader: strings.NewReader("BeginHeader\nEndHeader\nSECRET_FAMILY,1,\"x,y\"\n")}
		reader := xperfReader{ctx: t.Context(), index: map[string]int{}}
		if _, err := reader.scan(source, true); err != nil || source.calls != 0 || reader.result.CSVRejection != nil {
			t.Fatal("valid input entered rejection diagnostics")
		}
	})
	t.Run("fixed_byte_class_allowlist", func(t *testing.T) {
		_, _, shape, _ := xperfFramingControl(t, strings.NewReader("SECRET_FAMILY,1,\"SECRET_CELL\\\"x\n"))
		for _, value := range shape["before_at_after_coordinate_classes"].([]any) {
			if !slices.Contains([]string{"quote", "comma", "backslash", "space", "carriage_return", "line_feed", "other", "boundary"}, value.(string)) {
				t.Fatal("arbitrary byte value escaped structural diagnostics")
			}
		}
		xperfFramingNumber(t, shape, "backslash_quote_pairs", 1)
	})
}

type xperfFramingReadAtControl struct {
	*strings.Reader
	calls  int
	offset int64
	bytes  int
	moved  bool
	fail   bool
}

func (source *xperfFramingReadAtControl) ReadAt(data []byte, offset int64) (int, error) {
	source.calls++
	source.offset = offset
	source.bytes += len(data)
	before, _ := source.Seek(0, io.SeekCurrent)
	if source.fail {
		return 0, errors.New("SECRET_READ_ERROR")
	}
	n, err := source.Reader.ReadAt(data, offset)
	after, _ := source.Seek(0, io.SeekCurrent)
	source.moved = source.moved || before != after
	return n, err
}

// The same owned handle changes after strict parsing rejects the original bytes.
type xperfFramingRewrittenFile struct {
	*os.File
	replacement []byte
}

func (source *xperfFramingRewrittenFile) ReadAt(data []byte, offset int64) (int, error) {
	if _, err := source.File.WriteAt(source.replacement, 0); err != nil {
		return 0, errors.New("owned_snapshot_rewrite_failed")
	}
	return source.File.ReadAt(data, offset)
}
