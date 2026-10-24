package main

// Temporary xperf evidence reader. It does not accept causal attribution.
import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	Invalid        int      `json:"invalid_rows"`
	Supported      bool     `json:"observation_schema_supported"`
	Failure        string   `json:"failure,omitempty"`
	fields         []string
}

type xperfImage struct {
	PID    uint32 `json:"candidate_pid"`
	Module string `json:"module"`
	Size   uint64 `json:"size"`
	Start  string `json:"start_decimal"`
	End    string `json:"end_decimal,omitempty"`
	base   uint64
}

type xperfEvidence struct {
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
	Tail             [][]any             `json:"tail_rows"`
	Matched          int                 `json:"matched_rows"`
	Omitted          int                 `json:"omitted_rows"`
	UnknownKinds     int                 `json:"unknown_kind_rows"`
	UnknownHeaders   int                 `json:"unknown_header_rows"`
	Truncated        bool                `json:"truncated"`
	Limits           []string            `json:"limits"`
}

type xperfReader struct {
	taintedTIDs map[uint32]bool
	ctx         context.Context
	result      xperfEvidence
	index       map[string]int
	pids        map[uint32]bool
	tids        map[uint32]map[uint32]bool
	tokens      map[string]int
	rowBytes    int
	rowCount    int
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
	r.result = xperfEvidence{State: "partial_observations", Clock: "unverified_exact_decimal_only", Lifetime: "unverified", Limits: []string{
		"Rows match manifest identifiers; matching identifiers do not establish owned lifetimes.",
		"Timestamp decimals have no accepted unit or UTC mapping.",
		"Pointer tokens use candidate PID scope and lifetime_unverified.",
		"ReadyThread numeric columns retain their declared labels; actor and target roles remain unverified.",
		"No stack association, wait duration, or historical cause is accepted.",
	}}
	reject := func(code string) xperfEvidence { r.result.Failure = code; return r.result }
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
	r.result.Omitted = max(0, r.result.Matched-len(r.result.Rows)-len(r.result.Tail))
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
	return slices.Contains([]string{"P-Start", "P-End", "P-DCStart", "P-DCEnd", "T-Start", "T-End", "T-DCStart", "T-DCEnd", "CSwitch", "ReadyThread", "SampledProfile", "Stack", "ImageLoad", "ImageUnload", "ImageDCStart", "ImageDCEnd"}, kind)
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
	case "Address", "PrgrmCtr", "InstructionPointer", "ImageBase", "BaseAddr":
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

func (r *xperfReader) scan(file io.Reader, discovery bool) (string, error) {
	hash := sha256.New()
	limited := io.LimitReader(file, xperfCSVLimit+1)
	budget := &waitBudgetReader{source: io.TeeReader(limited, hash), ctx: r.ctx, remaining: 128 << 10}
	parser := csv.NewReader(budget)
	parser.FieldsPerRecord = -1
	parser.TrimLeadingSpace = true
	row := 0
	inHeader := false
	headerEnded := false
	for {
		if err := r.ctx.Err(); err != nil {
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
			continue
		}
		if kind == "EndHeader" && len(cells) == 1 {
			if !inHeader {
				return "", errors.New("header_section_order")
			}
			inHeader = false
			headerEnded = true
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
			if s.Supported && len(cells) == len(s.fields)+1 && strings.HasPrefix(kind, "Image") {
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
		encoded, _ := json.Marshal(record)
		if r.rowBytes+len(encoded)+1 <= xperfRowBudget && len(r.result.Rows) < 100000 {
			r.result.Rows = append(r.result.Rows, record)
			r.rowBytes += len(encoded) + 1
		} else {
			r.result.Truncated = true
			r.result.Omitted++
			if len(r.result.Tail) >= 64 {
				r.result.Tail = r.result.Tail[1:]
			}
			r.result.Tail = append(r.result.Tail, record)
		}
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
	pidSeen, pidValid := false, true
	var base, size uint64
	stamp, module := "", ""
	baseOK, sizeOK := false, false
	for i, name := range s.fields {
		switch name {
		case "Process Name (PID)", "Process Name ( PID)", "PID", "ProcessID", "ProcessId":
			value, valid := xperfID(cells[i], xperfColumn(name))
			if pidSeen && pid != value {
				pidValid = false
			}
			pidValid = pidValid && valid
			pidSeen = true
			pid = value
		case "ImageBase", "BaseAddr":
			base, baseOK = xperfPointer(cells[i])
		case "ImageSize", "Size":
			size, sizeOK = waitNumber(cells[i])
		case "FileName", "File Name", "Image", "ImageName":
			module = xperfModule(cells[i])
		case "TimeStamp":
			stamp = cells[i]
		}
	}
	if !pidSeen || !pidValid || !baseOK || !sizeOK || size == 0 || base > math.MaxUint64-size || module == "" || module == "unresolved" || !xperfDecimal.MatchString(stamp) {
		return
	}
	if !r.pids[pid] && !(pid == 0 && slices.Contains([]string{"ntoskrnl.exe", "ntkrnlmp.exe", "hal.dll"}, module)) {
		return
	}
	if s.Kind == "ImageUnload" {
		for i := range r.result.CandidateImages {
			im := &r.result.CandidateImages[i]
			if im.PID == pid && im.base == base && im.Module == module && im.End == "" {
				im.End = stamp
			}
		}
		return
	}
	if s.Kind == "ImageDCEnd" {
		return
	}
	if len(r.result.CandidateImages) >= 2048 {
		r.result.ImageTruncated = true
		return
	}
	r.result.CandidateImages = append(r.result.CandidateImages, xperfImage{PID: pid, Module: module, Size: size, Start: stamp, base: base})
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
		if address < im.base || address-im.base >= im.Size {
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
		"ImageLoad":      {{"TimeStamp"}, {"PID", "ProcessID", "ProcessId", "Process Name (PID)", "Process Name ( PID)"}, {"ImageBase", "BaseAddr"}, {"ImageSize", "Size"}, {"FileName", "File Name", "Image", "ImageName"}},
		"Stack":          {{"TimeStamp"}, {"ThreadID", "ThreadId", "TThreadId", "TID"}, {"No."}, {"Address"}},
		"SampledProfile": {{"TimeStamp"}, {"ThreadID", "ThreadId", "TThreadId", "TID"}, {"Process Name (PID)", "Process Name ( PID)", "PID", "ProcessID", "ProcessId"}, {"PrgrmCtr", "InstructionPointer"}},
	}
	for group, kinds := range map[string][]string{"scheduling": {"CSwitch", "ReadyThread"}, "lifetimes": {"P-Start", "P-End", "P-DCStart", "T-Start", "T-End", "T-DCStart"}, "images": {"ImageLoad", "ImageDCStart"}, "stacks": {"Stack", "SampledProfile"}} {
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

// Preserve independent decoded process anchors without weakening the XML reducer.
func (r *xperfReader) xmlAnchors(directory string) {
	r.result.AnchorState = "unavailable"
	decoder, budget, closeFile, err := waitXMLDecoder(r.ctx, filepath.Join(directory, "private-wait.xml"))
	if err != nil {
		return
	}
	defer closeFile()
	count := 0
	for {
		budget.remaining = 128 << 10
		token, err := decoder.Token()
		if err == io.EOF {
			r.result.AnchorState = "observed_unverified"
			return
		}
		if err != nil {
			r.result.AnchorState = "partial_xml_schema"
			return
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Event" {
			continue
		}
		count++
		if count > 2000000 {
			r.result.AnchorState = "event_bound"
			return
		}
		var event waitXMLEvent
		budget.remaining = 128 << 10
		if decoder.DecodeElement(&event, &start) != nil {
			r.result.AnchorState = "partial_xml_schema"
			return
		}
		guid := event.Extended.GUID
		if guid == "" {
			guid = event.System.Provider.GUID
		}
		if strings.ToLower(strings.Trim(guid, "{} \r\n\t")) != waitProcessGUID || !slices.Contains([]uint32{1, 2, 3, 4}, event.System.Opcode) {
			continue
		}
		var pid uint64
		seen := false
		valid := true
		for _, field := range event.Data {
			if strings.EqualFold(field.Name, "ProcessId") {
				if seen {
					valid = false
				}
				pid, ok = waitNumber(field.Value)
				valid = valid && ok
				seen = true
			}
		}
		raw, ok := waitNumber(event.System.Clock.Raw)
		if !valid || !seen || !ok || pid > math.MaxUint32 || !r.pids[uint32(pid)] {
			continue
		}
		if len(r.result.Anchors) >= 64 {
			r.result.AnchorState = "anchor_bound"
			return
		}
		r.result.Anchors = append(r.result.Anchors, map[string]uint64{"pid": pid, "opcode": uint64(event.System.Opcode), "raw_qpc": raw})
	}
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
