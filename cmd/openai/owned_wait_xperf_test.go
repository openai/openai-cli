package main

// Temporary xperf evidence reader. It does not accept causal attribution.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const xperfCSVLimit = 256 << 20
const xperfEvidenceLimit = 4 << 20
const xperfRowBudget = 2 << 20

var xperfHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var xperfDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})(\.[0-9]{1,12})?$`)
var xperfHeaderLabel = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9 _().!+-]{0,95}$`)
var xperfPIDCell = regexp.MustCompile(`\([ \t]{0,8}([0-9]{1,10})[ \t]{0,8}\)[ \t]{0,8}$`)

type xperfSchema struct {
	Declared       []string `json:"declared_columns"`
	Kind           string   `json:"kind"`
	Hash           string   `json:"header_sha256"`
	Columns        []string `json:"columns"`
	UnknownColumns int      `json:"unknown_columns"`
	Rows           int      `json:"rows"`
	Matches        int      `json:"candidate_matches"`
	Retained       int      `json:"retained_unique_rows"`
	Omitted        int      `json:"omitted_unique_rows"`
	Invalid        int      `json:"invalid_rows"`
	Supported      bool     `json:"observation_schema_supported"`
	Failure        string   `json:"failure,omitempty"`
	fields         []string
}

type xperfImage struct {
	Kind       string `json:"event_kind"`
	RangeBasis string `json:"range_basis"`
	Endpoint   string `json:"endpoint_semantics"`
	Span       uint64 `json:"end_minus_base_span,omitempty"`
	PID        uint32 `json:"candidate_pid"`
	Module     string `json:"module"`
	Size       uint64 `json:"size,omitempty"`
	Start      string `json:"start_decimal,omitempty"`
	End        string `json:"end_decimal,omitempty"`
	Observed   string `json:"observed_decimal"`
	base       uint64
	endAddress uint64
	endToken   string
}

type xperfCSVRejection struct {
	Framing    xperfCSVFraming             `json:"framing"`
	Input      xperfCSVInputClassification `json:"parser_input"`
	Ordinal    int                         `json:"record_ordinal"`
	Offset     int64                       `json:"input_offset"`
	ColumnName string                      `json:"declared_column,omitempty"`
	Category   string                      `json:"category"`
	Pass       string                      `json:"pass"`
	StartLine  int                         `json:"start_line"`
	Line       int                         `json:"line"`
	Column     int                         `json:"column"`
	Fields     int                         `json:"partial_field_count"`
	Kind       string                      `json:"allowlisted_event_kind,omitempty"`
}

type xperfCSVFraming struct {
	HeaderState    string        `json:"header_state"`
	BeginRecord    int           `json:"begin_header_record"`
	EndRecord      int           `json:"end_header_record"`
	PreviousOffset int64         `json:"previous_record_offset"`
	FirstCell      string        `json:"first_cell_category"`
	Shape          xperfCSVShape `json:"shape"`
}

// The snapshot can differ from parsed bytes after an in-place rewrite.
// These fields describe diagnostic byte structure, not verified parser input.
type xperfCSVShape struct {
	State                string   `json:"state"`
	SnapshotBasis        string   `json:"snapshot_basis"`
	ParserBytesVerified  bool     `json:"parser_bytes_verified"`
	RecordBytes          int64    `json:"record_bytes"`
	SliceBytes           int      `json:"slice_bytes"`
	QuoteOffsets         []int    `json:"quote_byte_offsets"`
	CommaOffsets         []int    `json:"comma_byte_offsets"`
	Quotes               int      `json:"quote_count"`
	Commas               int      `json:"comma_count"`
	AdjacentQuotes       int      `json:"adjacent_quote_pairs"`
	BackslashQuotes      int      `json:"backslash_quote_pairs"`
	LineFeeds            int      `json:"line_feed_count"`
	CarriageReturns      int      `json:"carriage_return_count"`
	PositionsTruncated   bool     `json:"positions_truncated"`
	ErrorPosition        string   `json:"error_position_state"`
	ErrorOffset          *int     `json:"error_byte_offset,omitempty"`
	ErrorAdjacentClasses []string `json:"before_at_after_coordinate_classes,omitempty"`
}

type xperfEvidence struct {
	CSVRejection *xperfCSVRejection `json:"csv_rejection,omitempty"`

	Version          int                 `json:"format_version"`
	Reservations     []xperfReservation  `json:"reservations"`
	TailSources      []int               `json:"tail_source_rows"`
	ImageTruncated   bool                `json:"image_mapping_truncated"`
	PointerTruncated bool                `json:"pointer_tokens_truncated"`
	ExportState      string              `json:"export_state"`
	HelpState        string              `json:"help_state"`
	ExportHash       string              `json:"expected_csv_sha256"`
	CandidateImages  []xperfImage        `json:"candidate_images"`
	Capabilities     map[string]any      `json:"capabilities"`
	Anchors          []map[string]uint64 `json:"xml_process_anchors"`
	AnchorState      string              `json:"xml_anchor_state"`
	RedactedCells    int                 `json:"redacted_cells"`
	MappedFrames     int                 `json:"candidate_mapped_frames"`
	AmbiguousFrames  int                 `json:"ambiguous_frames"`
	State            string              `json:"state"`
	Failure          string              `json:"failure,omitempty"`
	Accepted         bool                `json:"attribution_accepted"`
	Clock            string              `json:"clock_state"`
	Lifetime         string              `json:"lifetime_state"`
	CSVHash          string              `json:"csv_sha256"`
	Bytes            int64               `json:"csv_bytes"`
	Complete         bool                `json:"input_complete"`
	Schemas          []xperfSchema       `json:"schemas"`
	Rows             [][]any             `json:"rows"`
	Tail             [][]any             `json:"-"`
	Matched          int                 `json:"matched_rows"`
	Omitted          int                 `json:"omitted_rows"`
	UnknownKinds     int                 `json:"unknown_kind_rows"`
	UnknownHeaders   int                 `json:"unknown_header_rows"`
	Truncated        bool                `json:"truncated"`
	Limits           []string            `json:"limits"`
}

type xperfReader struct {
	taintedTIDs      map[uint32]bool
	ctx              context.Context
	result           xperfEvidence
	index            map[string]int
	pids             map[uint32]bool
	tids             map[uint32]map[uint32]bool
	tokens           map[string]int
	reservations     []*xperfReservation
	reservationIndex map[xperfReservationKey]*xperfReservation
}

func TestOwnedWaitXperfEvidenceProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) != 1 {
		t.Fatal("owned-wait invalid_xperf_arguments")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	result := readXperfEvidence(ctx, args[0])
	data, err := json.Marshal(result)
	if err != nil || len(data) > xperfEvidenceLimit {
		t.Fatal("owned-wait xperf_safe_encoding_failed")
	}
	path := filepath.Join(args[0], "wait-xperf-evidence.json")
	if os.WriteFile(path+".tmp", data, 0600) != nil {
		t.Fatal("owned-wait xperf_safe_write_failed")
	}
	if os.Rename(path+".tmp", path) != nil {
		t.Fatal("owned-wait xperf_safe_publish_failed")
	}
	if !result.Complete {
		t.Fatalf("owned-wait xperf_evidence_incomplete code=%s", result.Failure)
	}
}

func readXperfEvidence(ctx context.Context, directory string) xperfEvidence {
	r := xperfReader{ctx: ctx, index: map[string]int{}, pids: map[uint32]bool{}, tids: map[uint32]map[uint32]bool{}, tokens: map[string]int{}, taintedTIDs: map[uint32]bool{}}
	r.result = xperfEvidence{Version: 3, State: "partial_observations", Clock: "unverified_exact_decimal_only", Lifetime: "unverified", Limits: []string{
		"Rows match manifest identifiers; matching identifiers do not establish owned lifetimes.",
		"Timestamp decimals have no accepted unit or UTC mapping.",
		"Pointer tokens use candidate PID scope and lifetime_unverified.",
		"ReadyThread numeric columns retain their declared labels; actor and target roles remain unverified.",
		"No stack association, wait duration, or historical cause is accepted.",
		"Scope-zero reservations preserve provisional matches without claiming process or thread ownership.",
		"Reservation omissions count memberships; global omissions count unique source rows.",
		"CPU and sleep windows are not separated before clock validation.",
	}}
	reject := func(code string) xperfEvidence { r.finishRetention(); r.result.Failure = code; return r.result }
	var exported struct {
		State      string `json:"state"`
		Completion string `json:"completion"`
		Cleanup    string `json:"cleanup"`
		Hash       string `json:"csv_sha256"`
		Bytes      int64  `json:"csv_bytes"`
		Help       string `json:"help_validation"`
	}
	if waitReadJSON(directory, "wait-xperf-export-status.json", &exported) != nil {
		return reject("export_status_schema")
	}
	r.result.ExportState = waitFixedState(exported.State)
	r.result.HelpState = "unrecognized"
	if slices.Contains([]string{"recognized_command_shape", "unrecognized", "unavailable"}, exported.Help) {
		r.result.HelpState = exported.Help
	}
	if exported.State != "exported" || exported.Completion != "complete" || exported.Cleanup != "confirmed" || !xperfHash.MatchString(exported.Hash) {
		return reject("export_incomplete")
	}
	r.result.ExportHash = exported.Hash
	var ready waitReady
	var input waitInput
	if waitReadJSON(directory, "wait-trace-ready.json", &ready) != nil || waitReadJSON(directory, "wait-trace-input.json", &input) != nil {
		return reject("input_schema")
	}
	if ready.PID == 0 || len(ready.Controls) != 2 || len(input.Processes) > 2 {
		return reject("input_identity")
	}
	r.pids[ready.PID] = true
	r.addReservations(ready.PID, "recorder", false)
	for _, c := range ready.Controls {
		if c.PID != ready.PID || c.Thread == 0 {
			return reject("control_identity")
		}
		r.addThread(c.Thread, c.PID)
	}
	for _, p := range input.Processes {
		if p.PID == 0 || r.pids[p.PID] {
			return reject("target_identity")
		}
		r.pids[p.PID] = true
		role := "target"
		if p.Role == "powershell_direct" || p.Role == "powershell_pipeline" {
			role = p.Role
		}
		r.addReservations(p.PID, role, true)
	}
	file, err := os.Open(filepath.Join(directory, "private-events.csv"))
	if err != nil {
		return reject("csv_open")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > xperfCSVLimit {
		return reject("csv_bound")
	}
	r.result.Bytes = info.Size()
	if info.Size() != exported.Bytes {
		return reject("export_size_mismatch")
	}
	first, err := r.scan(file, true)
	if err != nil {
		return reject(err.Error())
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return reject("csv_seek")
	}
	second, err := r.scan(file, false)
	if err != nil {
		return reject(err.Error())
	}
	if first != second || second != exported.Hash {
		return reject("csv_changed")
	}
	r.finishRetention()
	r.result.CSVHash = second
	r.result.Complete = true
	r.result.Capabilities = r.capabilities()
	r.xmlAnchors(directory)
	if len(r.result.Schemas) == 0 {
		return reject("no_known_headers")
	}
	return r.result
}

func (r *xperfReader) addThread(tid, pid uint32) {
	if r.tids[tid] == nil {
		r.tids[tid] = map[uint32]bool{}
	}
	r.tids[tid][pid] = true
}

func xperfKind(kind string) bool {
	return slices.Contains([]string{"P-Start", "P-End", "P-DCStart", "P-DCEnd", "T-Start", "T-End", "T-DCStart", "T-DCEnd", "CSwitch", "ReadyThread", "SampledProfile", "Stack", "ImageLoad", "ImageUnload", "ImageDCStart", "ImageDCEnd", "I-Start", "I-End", "I-DCStart", "I-DCEnd"}, kind)
}

// These types permit safe observations, not an unverified semantic conversion.
func xperfColumn(name string) string {
	switch name {
	case "TimeStamp":
		return "time"
	case "Process Name (PID)", "Process Name ( PID)", "New Process Name (PID)", "New Process Name ( PID)", "Old Process Name (PID)", "Old Process Name ( PID)":
		return "pidcell"
	case "PID", "ProcessID", "ProcessId", "ParentID", "ParentId", "New ProcessId", "Old ProcessId":
		return "pid"
	case "ThreadID", "ThreadId", "TThreadId", "TID", "New TID", "Old TID", "Ready TID", "Rdy TID", "Target TID", "ReadiedThreadId", "ReadyingThreadId":
		return "tid"
	case "CPU", "Count", "No.", "AdjustReason", "AdjustIncrement", "Flag", "NPri", "OPri", "NQnt", "OQnt", "IdealProc", "OldRemQnt", "NewPriDecr", "PrevCState", "OldThrdBamQosLevel", "NewThrdBamQosLevel":
		return "number"
	case "Image!Function", "ThreadStartImage!Function", "FileName", "File Name", "Image", "ImageName":
		return "module"
	case "ImageSize", "Size":
		return "number"
	case "TmSinceLast", "WaitTime", "InSwitchTime":
		return "decimal"
	case "OldState", "Old Thread State", "Wait Reason", "OldThreadWaitReason", "Swapable":
		return "enum"
	case "Address", "PrgrmCtr", "InstructionPointer", "ImageBase", "BaseAddr", "EndAddr":
		return "pointer"
	default:
		return ""
	}
}

func (r *xperfReader) header(kind string, fields []string) error {
	if len(fields) > 64 || len(r.result.Schemas) >= 32 {
		return errors.New("header_bound")
	}
	data, _ := json.Marshal(fields)
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	if old, ok := r.index[kind]; ok {
		if r.result.Schemas[old].Hash != hash {
			return errors.New("conflicting_header")
		}
		return nil
	}
	s := xperfSchema{Kind: kind, Hash: hash, fields: fields, Columns: []string{"schema_id", "source_row", "candidate_pid_scope", "match_kind"}}
	seen := map[string]bool{}
	timeColumn, identity := false, false
	for i, name := range fields {
		if xperfHeaderLabel.MatchString(name) {
			s.Declared = append(s.Declared, name)
		} else {
			s.Declared = append(s.Declared, fmt.Sprintf("rejected_sha256_%x", sha256.Sum256([]byte(name))))
		}
		typeName := xperfColumn(name)
		if typeName == "" {
			s.UnknownColumns++
			s.Columns = append(s.Columns, fmt.Sprintf("ignored_column_%d", i))
			continue
		}
		if seen[name] {
			s.Failure = "duplicate_known_column"
		}
		seen[name] = true
		if typeName == "time" {
			timeColumn = true
		}
		if typeName == "tid" || typeName == "pid" || typeName == "pidcell" {
			identity = true
		}
		s.Columns = append(s.Columns, name)
	}
	s.Supported = timeColumn && identity && s.Failure == ""
	if !s.Supported && s.Failure == "" {
		s.Failure = "missing_observation_identity_or_time"
	}
	r.index[kind] = len(r.result.Schemas)
	r.result.Schemas = append(r.result.Schemas, s)
	return nil
}

func (r *xperfReader) csvRejection(err error, cells []string, discovery bool, ordinal int, offset int64, framing xperfCSVFraming, file io.Reader, captured *xperfCSVInputWindow) {
	rejection := &xperfCSVRejection{Category: "other_reader_error", Pass: "observation", Fields: len(cells), Ordinal: ordinal, Offset: offset}
	framing.FirstCell = "unavailable"
	framing.Shape.State = "deadline"
	rejection.Input.State = "deadline"
	if r.ctx.Err() == nil {
		framing.Shape = xperfCSVShapeAt(file, framing.PreviousOffset, offset, err)
	}
	if r.ctx.Err() == nil {
		rejection.Input = xperfClassifyCSVInput(captured, framing.PreviousOffset, offset, err, cells)
	}
	if discovery {
		rejection.Pass = "discovery"
	}
	switch {
	case errors.Is(err, csv.ErrBareQuote):
		rejection.Category = "bare_quote"
	case errors.Is(err, csv.ErrQuote):
		rejection.Category = "quote"
	case errors.Is(err, csv.ErrFieldCount):
		rejection.Category = "field_count"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || err.Error() == "reduction_deadline":
		rejection.Category = "reader_deadline"
	case err.Error() == "xml_event_bound":
		rejection.Category = "reader_budget"
	}
	var parse *csv.ParseError
	if errors.As(err, &parse) {
		rejection.StartLine = max(0, parse.StartLine)
		rejection.Line = max(0, parse.Line)
		rejection.Column = max(0, parse.Column)
	}
	if len(cells) > 0 {
		kind := strings.TrimSpace(strings.TrimPrefix(cells[0], "\ufeff"))
		framing.FirstCell = "unknown"
		switch {
		case kind == "BeginHeader":
			framing.FirstCell = "begin_header"
		case kind == "EndHeader":
			framing.FirstCell = "end_header"
		case strings.HasPrefix(kind, "OS Version:"):
			framing.FirstCell = "os_version_prefix"
		}
		if xperfKind(kind) {
			framing.FirstCell = "allowlisted_event"
			rejection.Kind = kind
			if index, exists := r.index[kind]; exists {
				schema := r.result.Schemas[index]
				column := len(cells) - 1
				if column >= 0 && column < len(schema.Declared) {
					rejection.ColumnName = schema.Declared[column]
				}
			}
		}
	}
	rejection.Framing = framing
	r.result.CSVRejection = rejection
}

func xperfCSVShapeAt(file io.Reader, previous, current int64, failure error) xperfCSVShape {
	shape := xperfCSVShape{State: "not_parse_error", SnapshotBasis: "none", ErrorPosition: "unavailable"}
	var parse *csv.ParseError
	if !errors.As(failure, &parse) {
		return shape
	}
	if previous < 0 || current <= previous || current > xperfCSVLimit+1 {
		shape.State = "invalid_range"
		return shape
	}
	shape.RecordBytes = current - previous
	source, ok := file.(io.ReaderAt)
	if !ok {
		shape.State = "reader_at_unavailable"
		return shape
	}
	shape.SnapshotBasis = "same_handle_after_rejection"
	const sliceLimit = 4096
	data := make([]byte, min(shape.RecordBytes, sliceLimit))
	n, err := source.ReadAt(data, previous)
	if n != len(data) || err != nil {
		shape.State = "read_failed"
		return shape
	}
	shape.State = "observed"
	shape.SliceBytes = len(data)
	if shape.RecordBytes > sliceLimit {
		shape.State = "clipped"
	}
	for offset, value := range data {
		switch value {
		case '"':
			shape.Quotes++
			if len(shape.QuoteOffsets) < 64 {
				shape.QuoteOffsets = append(shape.QuoteOffsets, offset)
			} else {
				shape.PositionsTruncated = true
			}
			if offset > 0 && data[offset-1] == '"' {
				shape.AdjacentQuotes++
			}
			if offset > 0 && data[offset-1] == '\\' {
				shape.BackslashQuotes++
			}
		case ',':
			shape.Commas++
			if len(shape.CommaOffsets) < 64 {
				shape.CommaOffsets = append(shape.CommaOffsets, offset)
			} else {
				shape.PositionsTruncated = true
			}
		case '\n':
			shape.LineFeeds++
		case '\r':
			shape.CarriageReturns++
		}
	}
	if parse.StartLine <= 0 || parse.Line < parse.StartLine || parse.Column <= 0 {
		return shape
	}
	// InputOffset precedes empty lines that encoding/csv skips before a record.
	position := 0
	for position < len(data) {
		if data[position] == '\n' {
			position++
		} else if position+1 < len(data) && data[position] == '\r' && data[position+1] == '\n' {
			position += 2
		} else {
			break
		}
	}
	for line := parse.StartLine; line < parse.Line; line++ {
		next := bytes.IndexByte(data[position:], '\n')
		if next < 0 {
			shape.ErrorPosition = "outside_slice"
			return shape
		}
		position += next + 1
	}
	position += parse.Column - 1
	if position > len(data) || (position == len(data) && shape.State == "clipped") {
		shape.ErrorPosition = "outside_slice"
		return shape
	}
	shape.ErrorPosition = "mapped_candidate"
	shape.ErrorOffset = &position
	for _, offset := range []int{position - 1, position, position + 1} {
		shape.ErrorAdjacentClasses = append(shape.ErrorAdjacentClasses, xperfCSVByteClass(data, offset))
	}
	return shape
}

// This window copies only bytes returned to encoding/csv. ReadAt is not a source.
// Read-ahead remains in the window until later reads evict it.
type xperfCSVInputWindow struct {
	source    io.Reader
	data      [16 << 10]byte
	delivered int64
	retained  int
}

func (w *xperfCSVInputWindow) Read(p []byte) (int, error) {
	n, err := w.source.Read(p)
	if n > 0 {
		end := w.delivered + int64(n)
		keep := min(n, len(w.data))
		start := int((end - int64(keep)) % int64(len(w.data)))
		first := min(keep, len(w.data)-start)
		copy(w.data[start:], p[n-keep:n-keep+first])
		copy(w.data[:], p[n-keep+first:n])
		w.delivered = end
		w.retained = min(len(w.data), w.retained+n)
	}
	return n, err
}

type xperfCSVInputClassification struct {
	State               string `json:"state"`
	ParserBytesVerified bool   `json:"parser_bytes_verified"`
	RecordBytes         int64  `json:"record_bytes"`
	Boundary            string `json:"boundary"`
	QuotePrefix         string `json:"quote_prefix"`
	QuotedPrefix        string `json:"quoted_prefix"`
	Suffix              string `json:"suffix"`
	SuffixBytes         int    `json:"suffix_bytes"`
	Delimiter           string `json:"delimiter"`
}

func xperfClassifyCSVInput(w *xperfCSVInputWindow, previous, current int64, failure error, cells []string) xperfCSVInputClassification {
	result := xperfCSVInputClassification{State: "not_quote_error", Boundary: "unavailable", QuotePrefix: "ambiguous", QuotedPrefix: "ambiguous", Suffix: "ambiguous", Delimiter: "unavailable"}
	var parse *csv.ParseError
	if !errors.As(failure, &parse) || !errors.Is(failure, csv.ErrQuote) {
		return result
	}
	if previous < 0 || current <= previous || current > xperfCSVLimit+1 {
		result.State = "invalid_range"
		return result
	}
	result.RecordBytes = current - previous
	if result.RecordBytes > 4096 {
		result.State = "record_bound"
		return result
	}
	if w == nil || previous < w.delivered-int64(w.retained) || current > w.delivered {
		result.State = "window_unavailable"
		return result
	}
	data := make([]byte, int(result.RecordBytes))
	start := int(previous % int64(len(w.data)))
	first := min(len(data), len(w.data)-start)
	copy(data, w.data[start:start+first])
	copy(data[first:], w.data[:len(data)-first])
	result.ParserBytesVerified = true
	// InputOffset includes empty physical lines skipped before this record.
	for len(data) > 0 {
		if data[0] == '\n' {
			data = data[1:]
		} else if bytes.HasPrefix(data, []byte("\r\n")) {
			data = data[2:]
		} else {
			break
		}
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	if parse.StartLine <= 0 || parse.Line != parse.StartLine || bytes.ContainsAny(data, "\r\n") {
		result.State = "multiline_ambiguous"
		return result
	}
	result.Boundary = "single_physical_line"
	quote := parse.Column - 1
	if quote < 0 || quote >= len(data) || data[quote] != '"' {
		result.State = "quote_coordinate_unavailable"
		return result
	}
	// The artificial EOF validates this quoted prefix, not the rejected full row.
	prefix := csv.NewReader(bytes.NewReader(data[:quote+1]))
	prefix.FieldsPerRecord = -1
	prefix.TrimLeadingSpace = true
	values, err := prefix.Read()
	if err != nil || len(values) != len(cells)+1 || !slices.Equal(values[:len(cells)], cells) {
		result.State = "quoted_prefix_ambiguous"
		return result
	}
	line, column := prefix.FieldPos(len(cells))
	opening := column - 1
	if line != 1 || opening < 0 || opening >= quote || data[opening] != '"' {
		result.State = "quoted_prefix_ambiguous"
		return result
	}
	result.State = "classified"
	paddingStart := bytes.LastIndexByte(data[:opening], ',') + 1
	result.QuotePrefix = "other"
	if len(bytes.Trim(data[paddingStart:opening], " \t")) == 0 {
		result.QuotePrefix = "padding"
	}
	result.QuotedPrefix = "other"
	switch values[len(cells)] {
	case "Unknown":
		result.QuotedPrefix = "fixed_Unknown"
	case "unknown":
		result.QuotedPrefix = "fixed_unknown"
	}
	suffix := data[quote+1:]
	result.Delimiter = "range_end"
	if comma := bytes.IndexByte(suffix, ','); comma >= 0 {
		suffix = suffix[:comma]
		result.Delimiter = "comma"
	}
	result.SuffixBytes = len(suffix)
	if bytes.IndexByte(suffix, '"') >= 0 {
		return result
	}
	trimmed := bytes.Trim(suffix, " \t")
	result.Suffix = "other"
	if len(trimmed) == 0 {
		result.Suffix = "padding"
	} else if len(trimmed) >= 3 && trimmed[0] == '(' && trimmed[len(trimmed)-1] == ')' {
		digits := bytes.Trim(trimmed[1:len(trimmed)-1], " \t")
		category := "parenthesized_unsigned_decimal"
		if len(digits) > 0 && (digits[0] == '+' || digits[0] == '-') {
			category = "parenthesized_signed_decimal"
			digits = digits[1:]
		}
		if len(digits) > 0 && bytes.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			result.Suffix = category
		}
	}
	return result
}

func xperfCSVByteClass(data []byte, offset int) string {
	if offset < 0 || offset >= len(data) {
		return "boundary"
	}
	switch data[offset] {
	case '"':
		return "quote"
	case ',':
		return "comma"
	case '\\':
		return "backslash"
	case ' ', '\t':
		return "space"
	case '\r':
		return "carriage_return"
	case '\n':
		return "line_feed"
	default:
		return "other"
	}
}

func (r *xperfReader) scan(file io.Reader, discovery bool) (string, error) {
	hash := sha256.New()
	limited := io.LimitReader(file, xperfCSVLimit+1)
	budget := &waitBudgetReader{source: io.TeeReader(limited, hash), ctx: r.ctx, remaining: 128 << 10}
	captured := &xperfCSVInputWindow{source: budget}
	parser := csv.NewReader(captured)
	parser.FieldsPerRecord = -1
	parser.TrimLeadingSpace = true
	row := 0
	inHeader := false
	headerEnded := false
	framing := xperfCSVFraming{HeaderState: "before"}
	for {
		framing.PreviousOffset = parser.InputOffset()
		if err := r.ctx.Err(); err != nil {
			r.csvRejection(err, nil, discovery, row+1, parser.InputOffset(), framing, file, captured)
			return "", errors.New("csv_deadline")
		}
		budget.remaining = 128 << 10
		cells, err := parser.Read()
		if err == io.EOF {
			if inHeader {
				return "", errors.New("unterminated_header")
			}
			break
		}
		if err != nil {
			r.csvRejection(err, cells, discovery, row+1, parser.InputOffset(), framing, file, captured)
			return "", errors.New("csv_syntax")
		}
		row++
		if row > 2000000 || len(cells) > 129 {
			return "", errors.New("csv_record_bound")
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
			if len(cells[i]) > 32768 {
				return "", errors.New("csv_cell_bound")
			}
		}
		if len(cells) == 0 {
			continue
		}
		kind := strings.TrimPrefix(cells[0], "\ufeff")
		if kind == "BeginHeader" && len(cells) == 1 {
			if inHeader || headerEnded {
				return "", errors.New("header_section_order")
			}
			inHeader = true
			framing.HeaderState = "inside"
			framing.BeginRecord = row
			continue
		}
		if kind == "EndHeader" && len(cells) == 1 {
			if !inHeader {
				return "", errors.New("header_section_order")
			}
			inHeader = false
			headerEnded = true
			framing.HeaderState = "after"
			framing.EndRecord = row
			continue
		}
		if kind == "" {
			continue
		}
		if !xperfKind(kind) {
			if !discovery {
				r.result.UnknownKinds++
			}
			continue
		}
		if inHeader {
			if discovery {
				if err := r.header(kind, cells[1:]); err != nil {
					return "", err
				}
			}
			continue
		}
		index, known := r.index[kind]
		if !known {
			if !discovery {
				r.result.UnknownHeaders++
			}
			continue
		}
		s := &r.result.Schemas[index]
		if discovery {
			if s.Supported && len(cells) == len(s.fields)+1 && (strings.HasPrefix(kind, "Image") || strings.HasPrefix(kind, "I-")) {
				r.discoverImage(s, cells[1:])
			}
			if s.Supported && len(cells) == len(s.fields)+1 && strings.HasPrefix(kind, "T-") {
				if len(r.tids) >= 100000 || len(r.taintedTIDs) >= 100000 {
					return "", errors.New("thread_identity_bound")
				}
				r.discoverThread(s, cells[1:])
			}
			continue
		}
		s.Rows++
		if !s.Supported {
			continue
		}
		if len(cells) != len(s.fields)+1 {
			s.Invalid++
			continue
		}
		values, scope, match, valid := r.observe(s, cells[1:])
		if !valid {
			s.Invalid++
			continue
		}
		if match == "" {
			continue
		}
		s.Matches++
		r.result.Matched++
		record := []any{index, row, scope, match}
		record = append(record, values...)
		r.retain(s, cells[1:], record, scope == 0)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func xperfID(value, typeName string) (uint32, bool) {
	if typeName == "pidcell" {
		match := xperfPIDCell.FindStringSubmatch(value)
		if len(match) != 2 {
			return 0, false
		}
		value = match[1]
	}
	v, err := strconv.ParseUint(value, 10, 32)
	return uint32(v), err == nil
}

func (r *xperfReader) observe(s *xperfSchema, cells []string) ([]any, uint32, string, bool) {
	values := make([]any, len(cells))
	scopes := map[uint32]bool{}
	explicitPIDs := map[uint32]bool{}
	match := ""
	for i, name := range s.fields {
		typeName := xperfColumn(name)
		if typeName != "pid" && typeName != "pidcell" && typeName != "tid" {
			continue
		}
		id, ok := xperfID(cells[i], typeName)
		if !ok {
			return nil, 0, "", false
		}
		values[i] = id
		if typeName != "tid" && !strings.HasPrefix(name, "Parent") {
			explicitPIDs[id] = true
		}
		if typeName == "tid" {
			for pid := range r.tids[id] {
				if r.pids[pid] {
					scopes[pid] = true
					match = "manifest_tid_match"
				}
			}
		} else if r.pids[id] && !strings.HasPrefix(name, "Parent") {
			scopes[id] = true
			match = "manifest_pid_match"
		}
	}
	if match == "" {
		return nil, 0, "", true
	}
	scope := uint32(0)
	ambiguousThread := false
	for i, name := range s.fields {
		if xperfColumn(name) == "tid" {
			id, _ := xperfID(cells[i], "tid")
			if len(r.tids[id]) > 1 || r.taintedTIDs[id] {
				ambiguousThread = true
			}
		}
	}
	if len(scopes) == 1 && !ambiguousThread && len(explicitPIDs) <= 1 {
		for pid := range scopes {
			if len(explicitPIDs) == 0 || explicitPIDs[pid] {
				scope = pid
			}
		}
	}
	for i, name := range s.fields {
		if xperfColumn(name) == "tid" {
			id, _ := xperfID(cells[i], "tid")
			if len(r.tids[id]) > 0 && !r.tids[id][scope] {
				scope = 0
			}
		}
	}
	for i, name := range s.fields {
		typeName := xperfColumn(name)
		switch typeName {
		case "time", "decimal":
			if !xperfDecimal.MatchString(cells[i]) {
				return nil, 0, "", false
			}
			values[i] = cells[i]
		case "number":
			v, err := strconv.ParseUint(cells[i], 10, 64)
			if err != nil {
				r.result.RedactedCells++
				continue
			}
			values[i] = v
		case "enum":
			if v, err := strconv.ParseUint(cells[i], 10, 32); err == nil {
				values[i] = v
			} else if slices.Contains([]string{"Wait", "Init", "Run", "Term", "DelayExec", "Initialized", "Ready", "Running", "Standby", "Terminated", "Waiting", "Transition", "DeferredReady", "Executive", "FreePage", "PageIn", "PoolAllocation", "DelayExecution", "Suspended", "UserRequest", "WrExecutive", "WrFreePage", "WrPageIn", "WrPoolAllocation", "WrDelayExecution", "WrSuspended", "WrUserRequest", "WrEventPair", "WrQueue", "WrLpcReceive", "WrLpcReply", "WrVirtualMemory", "WrPageOut", "WrRendezvous", "WrKeyedEvent", "WrTerminated", "WrProcessInSwap", "WrCpuRateControl", "WrCalloutStack", "WrKernel", "WrResource", "WrPushLock", "WrMutex", "WrQuantumEnd", "WrDispatchInt", "WrPreempted", "WrYieldExecution", "WrFastMutex", "WrGuardedMutex", "WrRundown", "WrAlertByThreadId", "WrDeferredPreempt", "WrPhysicalFault", "WrIoRing", "WrMdlCache", "Yes", "No"}, cells[i]) {
				values[i] = cells[i]
			} else {
				r.result.RedactedCells++
			}
		case "module":
			values[i] = xperfModule(cells[i])
		case "pointer":
			if scope == 0 {
				continue
			}
			v, ok := xperfPointer(cells[i])
			if !ok {
				return nil, 0, "", false
			}
			key := fmt.Sprintf("%d/%x", scope, v)
			if _, ok := r.tokens[key]; !ok {
				if len(r.tokens) >= 100000 {
					r.result.PointerTruncated = true
					continue
				}
				r.tokens[key] = len(r.tokens) + 1
			}
			frame := map[string]any{"token": r.tokens[key]}
			stamp := ""
			for j, label := range s.fields {
				if label == "TimeStamp" {
					stamp = cells[j]
				}
			}
			imageIndex, ambiguous := -1, false
			if s.Kind == "Stack" || s.Kind == "SampledProfile" {
				imageIndex, ambiguous = r.imageAt(scope, v, stamp)
			}
			if ambiguous {
				r.result.AmbiguousFrames++
			}
			if imageIndex >= 0 {
				frame["image"] = imageIndex
				frame["offset"] = fmt.Sprintf("0x%x", v-r.result.CandidateImages[imageIndex].base)
				r.result.MappedFrames++
			}
			values[i] = frame
		}
	}
	return values, scope, match, true
}

func xperfPointer(value string) (uint64, bool) {
	value = strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if len(value) == 0 || len(value) > 16 {
		return 0, false
	}
	n, err := strconv.ParseUint(value, 16, 64)
	return n, err == nil
}
func xperfModule(value string) string {
	name, _, _ := strings.Cut(value, "!")
	name = strings.ToLower(strings.TrimSpace(name))
	name = name[strings.LastIndexAny(name, `\/`)+1:]
	if waitModule(name) {
		return name
	}
	return "unresolved"
}
func xperfCompare(a, b string) (int, bool) {
	if !xperfDecimal.MatchString(a) || !xperfDecimal.MatchString(b) {
		return 0, false
	}
	x, _ := new(big.Rat).SetString(a)
	y, _ := new(big.Rat).SetString(b)
	return x.Cmp(y), true
}
func (r *xperfReader) discoverImage(s *xperfSchema, cells []string) {
	var pid uint32
	pidSeen, valid := false, true
	var base, endAddress, size uint64
	stamp, module, endToken := "", "", ""
	baseSeen, endSeen, sizeSeen, moduleSeen := false, false, false, false
	for i, name := range s.fields {
		switch name {
		case "Process Name (PID)", "Process Name ( PID)", "PID", "ProcessID", "ProcessId":
			value, ok := xperfID(cells[i], xperfColumn(name))
			valid = valid && ok && (!pidSeen || pid == value)
			pidSeen, pid = true, value
		case "ImageBase", "BaseAddr":
			value, ok := xperfPointer(cells[i])
			valid = valid && ok && (!baseSeen || base == value)
			baseSeen, base = true, value
		case "EndAddr":
			endToken = cells[i]
			value, ok := xperfPointer(cells[i])
			valid = valid && ok && (!endSeen || endAddress == value)
			endSeen, endAddress = true, value
		case "ImageSize", "Size":
			value, ok := waitNumber(cells[i])
			valid = valid && ok && (!sizeSeen || size == value)
			sizeSeen, size = true, value
		case "FileName", "File Name", "Image", "ImageName":
			value := xperfModule(cells[i])
			valid = valid && (!moduleSeen || module == value)
			moduleSeen, module = true, value
		case "TimeStamp":
			stamp = cells[i]
		}
	}
	if !valid || !pidSeen || !baseSeen || !moduleSeen || module == "unresolved" || !xperfDecimal.MatchString(stamp) {
		return
	}
	legacyRange := !strings.HasPrefix(s.Kind, "I-")
	if legacyRange {
		if !sizeSeen || size == 0 || base > math.MaxUint64-size {
			return
		}
	} else if !endSeen || endAddress <= base || sizeSeen {
		return
	}
	if !r.pids[pid] && !(pid == 0 && slices.Contains([]string{"ntoskrnl.exe", "ntkrnlmp.exe", "hal.dll"}, module)) {
		return
	}
	if s.Kind == "ImageUnload" || s.Kind == "I-End" {
		for i := range r.result.CandidateImages {
			im := &r.result.CandidateImages[i]
			if im.PID == pid && im.base == base && im.Module == module && im.Start != "" && im.End == "" {
				im.End = stamp
			}
		}
	}
	if len(r.result.CandidateImages) >= 2048 {
		r.result.ImageTruncated = true
		return
	}
	im := xperfImage{Kind: s.Kind, PID: pid, Module: module, Observed: stamp, base: base, endAddress: endAddress, endToken: endToken}
	if legacyRange {
		im.Size = size
		im.RangeBasis = "base_size"
		im.Endpoint = "size_bound"
	} else {
		im.Span = endAddress - base
		im.RangeBasis = "base_endaddr"
		im.Endpoint = "unknown_strict_interior_only"
	}
	switch s.Kind {
	case "ImageUnload", "I-End":
		im.End = stamp
	case "ImageDCEnd", "I-DCEnd":
	default:
		im.Start = stamp
	}
	r.result.CandidateImages = append(r.result.CandidateImages, im)
}
func (r *xperfReader) imageAt(pid uint32, address uint64, stamp string) (int, bool) {
	found := -1
	if r.result.ImageTruncated {
		return -1, false
	}
	for i, im := range r.result.CandidateImages {
		if im.PID != pid && im.PID != 0 {
			continue
		}
		if im.Start == "" {
			continue
		}
		if im.RangeBasis == "base_endaddr" {
			if address <= im.base || address >= im.endAddress {
				continue
			}
		} else if address < im.base || address-im.base >= im.Size {
			continue
		}
		if cmp, ok := xperfCompare(stamp, im.Start); !ok || cmp < 0 {
			continue
		}
		if im.End != "" {
			if cmp, ok := xperfCompare(stamp, im.End); !ok || cmp >= 0 {
				continue
			}
		}
		if found >= 0 {
			return -1, true
		}
		found = i
	}
	return found, false
}
func (r *xperfReader) capabilities() map[string]any {
	result := map[string]any{}
	required := map[string][][]string{
		"CSwitch":        {{"TimeStamp"}, {"New TID"}, {"Old TID"}, {"OldState", "Old Thread State"}, {"Wait Reason", "OldThreadWaitReason"}},
		"ReadyThread":    {{"TimeStamp"}, {"TThreadId", "Ready TID", "Rdy TID", "Target TID", "ReadiedThreadId"}, {"ReadyingThreadId"}},
		"P-Start":        {{"TimeStamp"}, {"PID", "ProcessID", "ProcessId", "Process Name (PID)", "Process Name ( PID)"}},
		"T-Start":        {{"TimeStamp"}, {"PID", "ProcessID", "ProcessId", "Process Name (PID)", "Process Name ( PID)"}, {"ThreadID", "ThreadId", "TThreadId", "TID"}},
		"I-Start":        {{"TimeStamp"}, {"PID", "ProcessID", "ProcessId", "Process Name (PID)", "Process Name ( PID)"}, {"BaseAddr"}, {"EndAddr"}, {"FileName"}},
		"I-DCStart":      {{"TimeStamp"}, {"PID", "ProcessID", "ProcessId", "Process Name (PID)", "Process Name ( PID)"}, {"BaseAddr"}, {"EndAddr"}, {"FileName"}},
		"ImageLoad":      {{"TimeStamp"}, {"PID", "ProcessID", "ProcessId", "Process Name (PID)", "Process Name ( PID)"}, {"ImageBase", "BaseAddr"}, {"ImageSize", "Size"}, {"FileName", "File Name", "Image", "ImageName"}},
		"Stack":          {{"TimeStamp"}, {"ThreadID", "ThreadId", "TThreadId", "TID"}, {"No."}, {"Address"}},
		"SampledProfile": {{"TimeStamp"}, {"ThreadID", "ThreadId", "TThreadId", "TID"}, {"Process Name (PID)", "Process Name ( PID)", "PID", "ProcessID", "ProcessId"}, {"PrgrmCtr", "InstructionPointer"}},
	}
	for group, kinds := range map[string][]string{"scheduling": {"CSwitch", "ReadyThread"}, "lifetimes": {"P-Start", "P-End", "P-DCStart", "T-Start", "T-End", "T-DCStart"}, "images": {"ImageLoad", "ImageDCStart", "I-Start", "I-End", "I-DCStart", "I-DCEnd"}, "stacks": {"Stack", "SampledProfile"}} {
		observed, missing, fields := []string{}, []string{}, []string{}
		for _, kind := range kinds {
			i, ok := r.index[kind]
			if !ok || !r.result.Schemas[i].Supported {
				missing = append(missing, kind)
				continue
			}
			observed = append(observed, kind)
			for _, aliases := range required[kind] {
				present := false
				for _, name := range aliases {
					present = present || slices.Contains(r.result.Schemas[i].fields, name)
				}
				if !present {
					fields = append(fields, kind+"."+aliases[0])
				}
			}
		}
		result[group] = map[string]any{"observed_headers": observed, "missing_headers": missing, "missing_named_fields": fields, "semantic_acceptance": false}
	}
	result["images"].(map[string]any)["candidate_ranges"] = len(r.result.CandidateImages)
	result["stacks"].(map[string]any)["accepted_joins"] = 0
	return result
}

func (r *xperfReader) discoverThread(s *xperfSchema, cells []string) {
	pids, tids := map[uint32]bool{}, map[uint32]bool{}
	valid := true
	for i, name := range s.fields {
		switch name {
		case "Process Name (PID)", "Process Name ( PID)", "PID", "ProcessID", "ProcessId":
			id, ok := xperfID(cells[i], xperfColumn(name))
			valid = valid && ok && id != 0
			if ok {
				pids[id] = true
			}
		case "ThreadID", "ThreadId", "TThreadId", "TID":
			id, ok := xperfID(cells[i], "tid")
			valid = valid && ok && id != 0
			if ok {
				tids[id] = true
			}
		}
	}
	if !valid || len(pids) != 1 || len(tids) != 1 {
		for tid := range tids {
			r.taintedTIDs[tid] = true
		}
		return
	}
	for tid := range tids {
		for pid := range pids {
			r.addThread(tid, pid)
		}
	}
}

type xperfReservationKey struct {
	pid       uint32
	class     string
	ambiguous bool
}
type xperfReservationCount struct {
	Matched  int `json:"matched_memberships"`
	Retained int `json:"retained_memberships"`
	Omitted  int `json:"omitted_memberships"`
}
type xperfSavedRow struct {
	value []any
	bytes int
}
type xperfReservation struct {
	PID          uint32                            `json:"candidate_pid"`
	Role         string                            `json:"role"`
	Class        string                            `json:"class"`
	ScopeZero    bool                              `json:"scope_zero"`
	Limit        int                               `json:"primary_limit_bytes"`
	TailLimit    int                               `json:"tail_limit_bytes"`
	Matched      int                               `json:"matched_memberships"`
	Retained     int                               `json:"retained_memberships"`
	Omitted      int                               `json:"omitted_memberships"`
	Truncated    bool                              `json:"truncated"`
	Kinds        map[string]*xperfReservationCount `json:"kind_counts"`
	primary      []xperfSavedRow
	tail         []xperfSavedRow
	primaryBytes int
	tailBytes    int
}

func (r *xperfReader) addReservations(pid uint32, role string, target bool) {
	if r.reservationIndex == nil {
		r.reservationIndex = map[xperfReservationKey]*xperfReservation{}
	}
	for _, class := range []string{"interval", "sample"} {
		for _, ambiguous := range []bool{false, true} {
			limit := 128 << 10
			if target && class == "interval" {
				limit = 256 << 10
			}
			reservation := &xperfReservation{PID: pid, Role: role, Class: class, ScopeZero: ambiguous, Limit: limit, TailLimit: 16 << 10, Kinds: map[string]*xperfReservationCount{}}
			r.reservations = append(r.reservations, reservation)
			r.reservationIndex[xperfReservationKey{pid, class, ambiguous}] = reservation
		}
	}
}
func (r *xperfReader) retain(s *xperfSchema, cells []string, row []any, ambiguous bool) {
	pids := map[uint32]bool{}
	for i, name := range s.fields {
		typeName := xperfColumn(name)
		if typeName != "tid" && typeName != "pid" && typeName != "pidcell" {
			continue
		}
		id, ok := xperfID(cells[i], typeName)
		if !ok {
			continue
		}
		if typeName == "tid" {
			for pid := range r.tids[id] {
				if r.pids[pid] {
					pids[pid] = true
				}
			}
		} else if !strings.HasPrefix(name, "Parent") && r.pids[id] {
			pids[id] = true
		}
	}
	class := "interval"
	if s.Kind == "Stack" || s.Kind == "SampledProfile" {
		class = "sample"
	}
	encoded, _ := json.Marshal(row)
	saved := xperfSavedRow{value: row, bytes: len(encoded) + 1}
	for pid := range pids {
		reservation := r.reservationIndex[xperfReservationKey{pid, class, ambiguous}]
		if reservation == nil {
			continue
		}
		reservation.Matched++
		if reservation.Kinds[s.Kind] == nil {
			reservation.Kinds[s.Kind] = &xperfReservationCount{}
		}
		reservation.Kinds[s.Kind].Matched++
		if reservation.primaryBytes+saved.bytes <= reservation.Limit && len(reservation.primary) < 100000*reservation.Limit/xperfRowBudget {
			reservation.primary = append(reservation.primary, saved)
			reservation.primaryBytes += saved.bytes
			continue
		}
		if saved.bytes > reservation.TailLimit {
			continue
		}
		for len(reservation.tail) > 0 && (len(reservation.tail) >= 64 || reservation.tailBytes+saved.bytes > reservation.TailLimit) {
			reservation.tailBytes -= reservation.tail[0].bytes
			reservation.tail = reservation.tail[1:]
		}
		reservation.tail = append(reservation.tail, saved)
		reservation.tailBytes += saved.bytes
	}
}
func (r *xperfReader) finishRetention() {
	r.result.Rows, r.result.Tail, r.result.TailSources, r.result.Reservations = nil, nil, nil, nil
	unique := map[int][]any{}
	primary := map[int]bool{}
	for _, reservation := range r.reservations {
		reservation.Retained = len(reservation.primary) + len(reservation.tail)
		reservation.Omitted = max(0, reservation.Matched-reservation.Retained)
		reservation.Truncated = reservation.Omitted > 0
		for _, count := range reservation.Kinds {
			count.Retained = 0
			count.Omitted = 0
		}
		for _, saved := range reservation.primary {
			ordinal := saved.value[1].(int)
			unique[ordinal] = saved.value
			primary[ordinal] = true
			reservation.Kinds[r.result.Schemas[saved.value[0].(int)].Kind].Retained++
		}
		for _, saved := range reservation.tail {
			ordinal := saved.value[1].(int)
			unique[ordinal] = saved.value
			reservation.Kinds[r.result.Schemas[saved.value[0].(int)].Kind].Retained++
		}
		for _, count := range reservation.Kinds {
			count.Omitted = max(0, count.Matched-count.Retained)
		}
		r.result.Reservations = append(r.result.Reservations, *reservation)
	}
	ordinals := make([]int, 0, len(unique))
	for ordinal := range unique {
		ordinals = append(ordinals, ordinal)
	}
	sort.Ints(ordinals)
	for i := range r.result.Schemas {
		r.result.Schemas[i].Retained = 0
	}
	for _, ordinal := range ordinals {
		row := unique[ordinal]
		r.result.Rows = append(r.result.Rows, row)
		r.result.Schemas[row[0].(int)].Retained++
		if !primary[ordinal] {
			r.result.Tail = append(r.result.Tail, row)
			r.result.TailSources = append(r.result.TailSources, ordinal)
		}
	}
	for i := range r.result.Schemas {
		r.result.Schemas[i].Omitted = max(0, r.result.Schemas[i].Matches-r.result.Schemas[i].Retained)
	}
	r.result.Omitted = max(0, r.result.Matched-len(r.result.Rows))
	r.result.Truncated = r.result.Omitted > 0
}
