package main

// Temporary native trace reducer. Raw XML and addresses never leave the owned directory.
import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const waitProcessGUID = "3d6fa8d0-fe05-11d0-9dda-00c04fd7ba7c"
const waitThreadGUID = "3d6fa8d1-fe05-11d0-9dda-00c04fd7ba7c"
const waitImageGUID = "2cb15d1d-5fc1-11d2-abe1-00a0c911f518"
const waitPerfGUID = "ce1dbfb4-137e-4da6-87b0-3f59aa102cbc"
const waitStackGUID = "def2fe46-7bd6-4b80-bd94-f57fe20d0ce3"
const waitHeaderGUID = "68fdd900-4a3e-11d1-84f4-0000f80464e3"
const waitXMLLimit = 512 << 20
const waitRecordLimit = 100000

var waitIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,95}$`)
var waitGUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type waitControl struct {
	Kind      string    `json:"kind"`
	PID       uint32    `json:"pid"`
	Thread    uint32    `json:"thread_id"`
	StartUTC  time.Time `json:"start_utc"`
	EndUTC    time.Time `json:"end_utc"`
	Start     uint64    `json:"start_qpc"`
	End       uint64    `json:"end_qpc"`
	Frequency uint64    `json:"qpc_frequency"`
}
type waitReady struct {
	Ready    bool          `json:"ready"`
	Controls []waitControl `json:"controls"`
	PID      uint32        `json:"recorder_pid"`
	Created  time.Time     `json:"recorder_created_utc"`
}
type waitProcessInput struct {
	Role       string    `json:"role"`
	PID        uint32    `json:"pid"`
	Before     time.Time `json:"before_start_utc"`
	After      time.Time `json:"after_start_utc"`
	Script     time.Time `json:"-"`
	ScriptText string    `json:"script_entered_utc"`
	Exit       time.Time `json:"exit_utc"`
}
type waitInput struct {
	Processes []waitProcessInput `json:"processes"`
}
type waitStatus struct {
	EventsLost *uint64   `json:"events_lost"`
	State      string    `json:"state"`
	Cleanup    string    `json:"cleanup"`
	Start      time.Time `json:"recording_start_utc"`
	Stop       time.Time `json:"recording_stop_utc"`
}
type waitXMLValue struct {
	Name  string `xml:"Name,attr"`
	Type  string `xml:"Type,attr"`
	Value string `xml:",chardata"`
}
type waitXMLEvent struct {
	System struct {
		Provider struct {
			GUID string `xml:"Guid,attr"`
		} `xml:"Provider"`
		Opcode  uint32 `xml:"Opcode"`
		Version uint32 `xml:"Version"`
		Clock   struct {
			Raw string `xml:"RawTime,attr"`
			UTC string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
		Execution struct {
			PID uint32 `xml:"ProcessID,attr"`
			TID uint32 `xml:"ThreadID,attr"`
			CPU uint32 `xml:"ProcessorID,attr"`
		} `xml:"Execution"`
	} `xml:"System"`
	Data     []waitXMLValue `xml:"EventData>Data"`
	Extended struct {
		GUID string `xml:"EventGuid"`
	} `xml:"ExtendedTracingInfo"`
}
type waitEvent struct {
	GUID                           string
	Opcode, Version, PID, TID, CPU uint32
	Raw                            uint64
	Fields                         map[string]string
}
type waitSchema struct {
	Count    int      `json:"count"`
	Fields   []string `json:"fields"`
	RawClock bool     `json:"raw_clock"`
}
type waitLifetime struct {
	PID, TID   uint32
	Start, End uint64
	Rundown    bool
}
type waitImage struct {
	PID                    uint32
	Base, Size, Start, End uint64
	Name                   string
}
type waitKey struct {
	Raw uint64
	TID uint32
}
type waitStack struct {
	PID       uint32
	Addresses []uint64
}
type waitTransition struct {
	Lifetime                       uint64
	Kind                           string
	Raw                            uint64
	PID, TID, State, Reason, Waker uint32
}
type waitInterval struct {
	Kind             string
	Start, End, In   uint64
	PID, TID, Reason uint32
	Stack, Readying  waitKey
}
type waitFrame struct {
	Module string `json:"module"`
	Offset string `json:"offset,omitempty"`
}
type waitReduction struct {
	SchemaAccepted     bool                   `json:"native_schema_accepted"`
	ComparisonComplete bool                   `json:"comparison_complete"`
	State              string                 `json:"state"`
	Failure            string                 `json:"failure,omitempty"`
	Accepted           bool                   `json:"accepted"`
	Schemas            map[string]*waitSchema `json:"schemas"`
	Metadata           map[string]any         `json:"metadata"`
	Controls           []map[string]any       `json:"controls"`
	Processes          []map[string]any       `json:"processes"`
	Limits             []string               `json:"limits"`
}
type waitReducer struct {
	ThreadIndex map[uint32][]waitLifetime
	Ready       waitReady
	Input       waitInput
	Status      waitStatus
	Result      waitReduction
	Frequency   uint64
	Anchor      waitControl
	HeaderSeen  bool
	First, Last uint64
	Processes   []waitLifetime
	Threads     []waitLifetime
	Images      []waitImage
	Owned       map[uint32]waitLifetime
	Transitions []waitTransition
	Samples     []waitKey
	Wanted      map[waitKey]uint32
	Stacks      map[waitKey]waitStack
	Intervals   []waitInterval
	Censored    map[uint32]int
}

func TestOwnedWaitReductionProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) != 1 {
		t.Fatal("owned-wait invalid_reducer_arguments")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	result := waitReduce(ctx, args[0])
	data, err := json.Marshal(result)
	if err != nil || len(data) > 1<<20 {
		t.Fatal("owned-wait safe_summary_encoding_failed")
	}
	path := filepath.Join(args[0], "wait-reduction.json")
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		t.Fatal("owned-wait safe_summary_write_failed")
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal("owned-wait safe_summary_publish_failed")
	}
	if !result.Accepted {
		t.Fatalf("owned-wait reduction_rejected code=%s", result.Failure)
	}
}

func waitReduce(ctx context.Context, directory string) waitReduction {
	r := waitReducer{Owned: map[uint32]waitLifetime{}, Wanted: map[waitKey]uint32{}, Stacks: map[waitKey]waitStack{}, Censored: map[uint32]int{}}
	r.Result = waitReduction{State: "rejected", Schemas: map[string]*waitSchema{}, Metadata: map[string]any{}, Limits: []string{
		"Native XML schema acceptance requires both recorder controls.",
		"Missing intervals remain censored; missing time is never replaced with zero.",
		"Phase totals sum thread time and can exceed the phase wall duration.",
		"Module-relative offsets do not identify functions.",
		"Readying stacks describe the actor, never the target wait stack.",
		"Retained observed coverage cannot prove absence of unrecorded events.",
		"This trace cannot establish the cause of historical failures.",
	}}
	reject := func(code string) waitReduction { r.Result.Failure = code; return r.Result }
	if waitReadJSON(directory, "wait-trace-ready.json", &r.Ready) != nil || waitReadJSON(directory, "wait-trace-input.json", &r.Input) != nil || waitReadJSON(directory, "wait-trace-status.json", &r.Status) != nil {
		return reject("input_schema")
	}
	var exported struct {
		State string `json:"state"`
	}
	if waitReadJSON(directory, "wait-export-status.json", &exported) != nil {
		return reject("export_status_schema")
	}
	r.Result.Metadata["export_state"] = waitFixedState(exported.State)
	if r.Status.State != "recorded" || exported.State != "exported" {
		return reject("recording_or_export_failed")
	}
	if r.Status.EventsLost != nil {
		r.Result.Metadata["wpr_events_lost"] = *r.Status.EventsLost
	} else {
		r.Result.Metadata["wpr_events_lost"] = "unavailable"
	}
	if r.Status.Cleanup != "confirmed" {
		return reject("recorder_cleanup_unconfirmed")
	}
	path := filepath.Join(directory, "private-wait.xml")
	inventory, inventoryErr := waitInventory(ctx, path)
	r.Result.Metadata["xml_inventory"] = inventory
	if inventoryErr != nil {
		return reject(waitErrorCode(inventoryErr))
	}
	if err := r.prepare(); err != nil {
		return reject(err.Error())
	}
	if err := waitStream(ctx, path, func(e waitEvent) error { return r.metadata(e) }); err != nil {
		return reject(waitErrorCode(err))
	}
	if err := r.bind(); err != nil {
		return reject(err.Error())
	}
	if err := waitStream(ctx, path, func(e waitEvent) error { return r.scheduling(e) }); err != nil {
		return reject(waitErrorCode(err))
	}
	if err := waitStream(ctx, path, func(e waitEvent) error { return r.stack(e) }); err != nil {
		r.calculate()
		r.summarize()
		return reject(waitErrorCode(err))
	}
	r.calculate()
	r.summarize()
	if err := r.validate(); err != nil {
		return reject(err.Error())
	}
	r.Result.Accepted = true
	r.Result.State = "accepted"
	return r.Result
}
func waitFixedState(value string) string {
	if slices.Contains([]string{"recorded", "exported", "failed", "unavailable", "complete", "completed"}, value) {
		return value
	}
	return "unrecognized"
}
func waitReadJSON(directory, name string, destination any) error {
	file, err := os.Open(filepath.Join(directory, name))
	if err != nil {
		return errors.New("input_read")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("input_bound")
	}
	decoder := json.NewDecoder(io.LimitReader(file, (1<<20)+1))
	if decoder.Decode(destination) != nil {
		return errors.New("input_schema")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return errors.New("input_trailing")
	}
	return nil
}
func (r *waitReducer) prepare() error {
	if !r.Ready.Ready || len(r.Ready.Controls) != 2 || r.Ready.PID == 0 || r.Ready.Created.IsZero() {
		return errors.New("control_identity")
	}
	for index, c := range r.Ready.Controls {
		if c.Kind != []string{"cpu", "sleep"}[index] || c.PID != r.Ready.PID || c.Thread == 0 || c.Frequency == 0 || c.End <= c.Start || c.StartUTC.IsZero() || !c.EndUTC.After(c.StartUTC) {
			return errors.New("control_schema")
		}
		elapsed, ok := waitNS(c.End-c.Start, c.Frequency)
		if !ok || elapsed <= 0 || elapsed > int64(time.Minute) {
			return errors.New("control_clock_bound")
		}
		if math.Abs(float64(c.EndUTC.Sub(c.StartUTC)-time.Duration(elapsed))) > float64(5*time.Millisecond) {
			return errors.New("control_clock_ambiguity")
		}
		if index == 0 {
			r.Anchor = c
			r.Frequency = c.Frequency
		}
		if c.Frequency != r.Frequency || math.Abs(float64(r.utc(c.Start).Sub(c.StartUTC))) > float64(5*time.Millisecond) {
			return errors.New("control_clock_ambiguity")
		}
	}
	if len(r.Input.Processes) < 1 || len(r.Input.Processes) > 2 {
		return errors.New("target_count")
	}
	if r.Ready.Created.After(r.Ready.Controls[0].StartUTC) || r.Status.Start.IsZero() || r.Status.Stop.IsZero() || r.Status.Start.After(r.Ready.Controls[0].StartUTC) || r.Status.Stop.Before(r.Ready.Controls[1].EndUTC) {
		return errors.New("recording_window")
	}
	seen := map[uint32]bool{r.Ready.PID: true}
	for index, p := range r.Input.Processes {
		if p.ScriptText != "" {
			var err error
			p.Script, err = time.Parse(time.RFC3339Nano, p.ScriptText)
			if err != nil {
				return errors.New("target_script_timestamp")
			}
			r.Input.Processes[index].Script = p.Script
		}
		if p.Role != []string{"powershell_direct", "powershell_pipeline"}[index] || p.PID == 0 || seen[p.PID] || p.Before.IsZero() || p.After.Before(p.Before) || p.Exit.Before(p.After) {
			return errors.New("target_identity")
		}
		if !p.Script.IsZero() && (p.Script.Before(p.After) || p.Exit.Before(p.Script)) {
			return errors.New("target_phase_order")
		}
		if p.Before.Before(r.Ready.Controls[1].EndUTC) || p.Exit.After(r.Status.Stop) {
			return errors.New("target_recording_window")
		}
		seen[p.PID] = true
	}
	r.Result.Metadata["clock_mapping"] = "recorder_qpc_utc_anchors_with_trace_frequency_validation"
	r.Result.Metadata["qpc_frequency"] = r.Frequency
	r.Result.Metadata["target_controls_observed"] = len(r.Input.Processes)
	return nil
}
func waitNS(delta, frequency uint64) (int64, bool) {
	if frequency == 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(delta, 1000000000)
	if hi >= frequency {
		return 0, false
	}
	q, _ := bits.Div64(hi, lo, frequency)
	if q > math.MaxInt64 {
		return 0, false
	}
	return int64(q), true
}
func (r *waitReducer) utc(raw uint64) time.Time {
	var delta uint64
	negative := raw < r.Anchor.Start
	if negative {
		delta = r.Anchor.Start - raw
	} else {
		delta = raw - r.Anchor.Start
	}
	ns, ok := waitNS(delta, r.Frequency)
	if !ok || ns > int64(24*time.Hour) {
		return time.Time{}
	}
	if negative {
		ns = -ns
	}
	return r.Anchor.StartUTC.Add(time.Duration(ns)).UTC()
}
func waitNumber(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	base := 10
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		base = 16
		value = value[2:]
	}
	v, err := strconv.ParseUint(value, base, 64)
	return v, err == nil
}
func waitField(e waitEvent, names ...string) (uint64, error) {
	var result uint64
	found := false
	for _, name := range names {
		if value, ok := e.Fields[strings.ToLower(name)]; ok {
			n, valid := waitNumber(value)
			if !valid || (found && n != result) {
				return 0, errors.New("numeric_schema")
			}
			result = n
			found = true
		}
	}
	if !found {
		return 0, errors.New("missing_numeric_field")
	}
	return result, nil
}
func waitID(e waitEvent, names ...string) (uint32, error) {
	v, err := waitField(e, names...)
	if err != nil || v > math.MaxUint32 {
		return 0, errors.New("identity_schema")
	}
	return uint32(v), nil
}
func (r *waitReducer) target(pid uint32) bool {
	if pid == r.Ready.PID {
		return true
	}
	return slices.ContainsFunc(r.Input.Processes, func(p waitProcessInput) bool { return p.PID == pid })
}
func (r *waitReducer) metadata(e waitEvent) error {
	key := fmt.Sprintf("%s/%d/%d", e.GUID, e.Opcode, e.Version)
	schema := r.Result.Schemas[key]
	if schema == nil {
		if len(r.Result.Schemas) >= 128 {
			return errors.New("schema_count_bound")
		}
		schema = &waitSchema{RawClock: e.Raw != 0}
		r.Result.Schemas[key] = schema
		for field := range e.Fields {
			if waitIdentifier.MatchString(field) {
				schema.Fields = append(schema.Fields, field)
			}
		}
		sort.Strings(schema.Fields)
	}
	schema.Count++
	if e.Raw != 0 {
		if r.First == 0 || e.Raw < r.First {
			r.First = e.Raw
		}
		if e.Raw > r.Last {
			r.Last = e.Raw
		}
	}
	if e.GUID == waitHeaderGUID && e.Opcode == 0 {
		frequency, err := waitField(e, "PerfFreq")
		if err != nil || frequency != r.Frequency {
			return errors.New("trace_frequency_mismatch")
		}
		if r.HeaderSeen {
			return errors.New("multiple_trace_headers")
		}
		r.HeaderSeen = true
		for _, field := range []string{"EventsLost", "BuffersLost", "BufferSize", "NumberOfProcessors", "PointerSize"} {
			n, err := waitField(e, field)
			if err != nil {
				return err
			}
			r.Result.Metadata[strings.ToLower(field)] = n
		}
		return nil
	}
	if e.GUID == waitProcessGUID && slices.Contains([]uint32{1, 2, 3, 4}, e.Opcode) {
		pid, err := waitID(e, "ProcessId")
		if err != nil {
			return err
		}
		if !r.target(pid) {
			return nil
		}
		if e.Raw == 0 {
			return errors.New("missing_raw_timestamp")
		}
		if e.Opcode == 1 || e.Opcode == 3 {
			r.Processes = append(r.Processes, waitLifetime{PID: pid, Start: e.Raw, Rundown: e.Opcode == 3})
		} else if e.Opcode == 2 {
			for i := len(r.Processes) - 1; i >= 0; i-- {
				if r.Processes[i].PID == pid && r.Processes[i].End == 0 {
					r.Processes[i].End = e.Raw
					break
				}
			}
		}
	}
	if e.GUID == waitThreadGUID && slices.Contains([]uint32{1, 2, 3, 4}, e.Opcode) {
		pid, err := waitID(e, "ProcessId")
		if err != nil {
			return err
		}
		tid, err := waitID(e, "TThreadId", "ThreadId")
		if err != nil {
			return err
		}
		if e.Raw == 0 {
			return errors.New("missing_raw_timestamp")
		}
		if e.Opcode == 1 || e.Opcode == 3 {
			r.Threads = append(r.Threads, waitLifetime{PID: pid, TID: tid, Start: e.Raw, Rundown: e.Opcode == 3})
		} else if e.Opcode == 2 {
			for i := len(r.Threads) - 1; i >= 0; i-- {
				if r.Threads[i].PID == pid && r.Threads[i].TID == tid && r.Threads[i].End == 0 {
					r.Threads[i].End = e.Raw
					break
				}
			}
		}
	}
	if e.GUID == waitImageGUID && slices.Contains([]uint32{10, 2, 3}, e.Opcode) {
		pid, err := waitID(e, "ProcessId")
		if err != nil {
			return err
		}
		if !r.target(pid) && pid != 0 && pid != 4 {
			return nil
		}
		base, err := waitField(e, "ImageBase")
		if err != nil {
			return err
		}
		if e.Opcode == 2 {
			for i := len(r.Images) - 1; i >= 0; i-- {
				if r.Images[i].PID == pid && r.Images[i].Base == base && r.Images[i].End == 0 {
					r.Images[i].End = e.Raw
					break
				}
			}
			return nil
		}
		size, err := waitField(e, "ImageSize")
		if err != nil || size == 0 || base > math.MaxUint64-size {
			return errors.New("image_range")
		}
		name := e.Fields["filename"]
		name = strings.ToLower(name[strings.LastIndexAny(name, "/\\")+1:])
		if !waitModule(name) {
			return nil
		}
		r.Images = append(r.Images, waitImage{PID: pid, Base: base, Size: size, Start: e.Raw, Name: name})
	}
	if len(r.Processes) > 128 || len(r.Threads) > 65536 || len(r.Images) > 8192 {
		return errors.New("metadata_count_bound")
	}
	return nil
}
func waitModule(name string) bool {
	return slices.Contains([]string{"ntoskrnl.exe", "ntkrnlmp.exe", "hal.dll", "ntdll.dll", "kernelbase.dll", "kernel32.dll", "clr.dll", "coreclr.dll", "system.private.corelib.dll", "mscoree.dll", "mscoreei.dll", "wow64.dll", "wow64cpu.dll", "wow64win.dll", "wowarmhw.dll", "xtajit64.dll", "clrjit.dll", "mscorlib.dll", "mscorlib.ni.dll", "mscoreei.dll", "mscorwks.dll", "system.management.automation.dll", "system.management.automation.ni.dll", "powershell.exe", "pwsh.exe", "rpcrt4.dll", "advapi32.dll", "sechost.dll", "combase.dll", "ole32.dll", "bcrypt.dll", "bcryptprimitives.dll", "crypt32.dll", "wintrust.dll", "win32u.dll", "user32.dll", "gdi32full.dll", "ws2_32.dll", "winhttp.dll", "wininet.dll", "profapi.dll", "amsi.dll"}, name)
}
func (r *waitReducer) bind() error {
	r.ThreadIndex = map[uint32][]waitLifetime{}
	for _, life := range r.Threads {
		r.ThreadIndex[life.TID] = append(r.ThreadIndex[life.TID], life)
	}
	if !r.HeaderSeen {
		return errors.New("trace_header_unrecognized")
	}
	r.Result.Metadata["first_event_utc"] = r.utc(r.First)
	r.Result.Metadata["last_event_utc"] = r.utc(r.Last)
	if r.First > r.Ready.Controls[0].Start || r.Last < r.Ready.Controls[1].End {
		return errors.New("control_retention_gap")
	}
	for _, pid := range append([]uint32{r.Ready.PID}, waitTargetIDs(r.Input.Processes)...) {
		var matches []waitLifetime
		for _, life := range r.Processes {
			if life.PID != pid {
				continue
			}
			if pid == r.Ready.PID {
				if life.Rundown && life.Start <= r.Ready.Controls[0].Start && (life.End == 0 || life.End >= r.Ready.Controls[1].End) {
					matches = append(matches, life)
				}
				continue
			}
			p := r.Input.Processes[slices.IndexFunc(r.Input.Processes, func(v waitProcessInput) bool { return v.PID == pid })]
			timestamp := r.utc(life.Start)
			if !life.Rundown && !timestamp.Before(p.Before.Add(-5*time.Millisecond)) && !timestamp.After(p.After.Add(5*time.Millisecond)) && life.End >= life.Start {
				matches = append(matches, life)
			}
		}
		if len(matches) != 1 {
			return errors.New("process_lifetime_ambiguous")
		}
		selected := matches[0]
		until := selected.End
		if pid == r.Ready.PID {
			until = r.Ready.Controls[1].End
		}
		for _, other := range r.Processes {
			if other.PID == pid && other != selected && other.Start <= until && (other.End == 0 || other.End >= selected.Start) {
				return errors.New("process_lifetime_ambiguous")
			}
		}
		r.Owned[pid] = selected
	}
	for _, p := range r.Input.Processes {
		life := r.Owned[p.PID]
		if life.End == 0 || r.utc(life.End).After(p.Exit.Add(5*time.Millisecond)) {
			return errors.New("process_exit_join")
		}
	}
	for _, c := range r.Ready.Controls {
		pid, ok := r.thread(c.Thread, c.Start)
		if !ok || pid != c.PID {
			return errors.New("control_thread_join")
		}
		pid, ok = r.thread(c.Thread, c.End)
		if !ok || pid != c.PID {
			return errors.New("control_thread_lifetime")
		}
	}
	r.Result.Metadata["process_lifetime_joins"] = len(r.Owned)
	return nil
}
func waitTargetIDs(processes []waitProcessInput) []uint32 {
	var result []uint32
	for _, p := range processes {
		result = append(result, p.PID)
	}
	return result
}
func (r *waitReducer) thread(tid uint32, raw uint64) (uint32, bool) {
	life, ok := r.threadLife(tid, raw)
	return life.PID, ok
}
func (r *waitReducer) threadLife(tid uint32, raw uint64) (waitLifetime, bool) {
	var selected waitLifetime
	count := 0
	for _, life := range r.ThreadIndex[tid] {
		if raw >= life.Start && (life.End == 0 || raw <= life.End) {
			selected = life
			count++
		}
	}
	if count != 1 {
		return waitLifetime{}, false
	}
	owner, ok := r.Owned[selected.PID]
	return selected, ok && raw >= owner.Start && (owner.End == 0 || raw <= owner.End)
}

func (r *waitReducer) readyingActor(tid uint32, raw uint64) (uint32, bool) {
	// Idle threads have no unique process lifetime. Exact stack joins still need PID zero.
	if tid == 0 {
		return 0, true
	}
	var pid uint32
	count := 0
	for _, life := range r.ThreadIndex[tid] {
		if raw >= life.Start && (life.End == 0 || raw <= life.End) {
			pid = life.PID
			count++
		}
	}
	return pid, count == 1
}
func (r *waitReducer) scheduling(e waitEvent) error {
	add := func(kind string, tid, state, reason, waker uint32) error {
		pid, ok := r.thread(tid, e.Raw)
		if !ok {
			return nil
		}
		if e.Raw == 0 {
			return errors.New("missing_raw_timestamp")
		}
		life, _ := r.threadLife(tid, e.Raw)
		r.Transitions = append(r.Transitions, waitTransition{Lifetime: life.Start, Kind: kind, Raw: e.Raw, PID: pid, TID: tid, State: state, Reason: reason, Waker: waker})
		return nil
	}
	if e.GUID == waitThreadGUID && e.Opcode == 36 {
		old, err := waitID(e, "OldThreadId")
		if err != nil {
			return err
		}
		next, err := waitID(e, "NewThreadId")
		if err != nil {
			return err
		}
		state, err := waitID(e, "OldThreadState")
		if err != nil {
			return err
		}
		reason, err := waitID(e, "OldThreadWaitReason")
		if err != nil {
			return err
		}
		if err = add("out", old, state, reason, 0); err != nil {
			return err
		}
		if err = add("in", next, 0, 0, 0); err != nil {
			return err
		}
		if pid, ok := r.thread(old, e.Raw); ok {
			r.Wanted[waitKey{e.Raw, old}] = pid
		}
	}
	if e.GUID == waitThreadGUID && e.Opcode == 50 {
		target, err := waitID(e, "TThreadId")
		if err != nil {
			return err
		}
		if _, ok := r.thread(target, e.Raw); ok {
			if err = add("ready", target, 0, 0, e.TID); err != nil {
				return err
			}
			// Kernel event headers can omit the readying process identity.
			// Resolve the actor through its thread lifetime instead of assigning the target process.
			if actor, valid := r.readyingActor(e.TID, e.Raw); valid {
				r.Wanted[waitKey{e.Raw, e.TID}] = actor
			}
		}
	}
	if e.GUID == waitPerfGUID && e.Opcode == 46 {
		tid, err := waitID(e, "ThreadId")
		if err != nil {
			return err
		}
		if pid, ok := r.thread(tid, e.Raw); ok {
			key := waitKey{e.Raw, tid}
			r.Samples = append(r.Samples, key)
			r.Wanted[key] = pid
		}
	}
	if len(r.Transitions) > waitRecordLimit || len(r.Samples) > waitRecordLimit || len(r.Wanted) > waitRecordLimit {
		return errors.New("schedule_count_bound")
	}
	return nil
}
func (r *waitReducer) stack(e waitEvent) error {
	if e.GUID != waitStackGUID || e.Opcode != 32 {
		return nil
	}
	raw, err := waitField(e, "EventTimeStamp")
	if err != nil {
		return err
	}
	tid, err := waitID(e, "StackThread")
	if err != nil {
		return err
	}
	key := waitKey{raw, tid}
	expected, wanted := r.Wanted[key]
	if !wanted {
		return nil
	}
	pid, err := waitID(e, "StackProcess")
	if err != nil || pid != expected {
		return errors.New("stack_identity_mismatch")
	}
	if _, exists := r.Stacks[key]; exists {
		return errors.New("stack_join_ambiguous")
	}
	var addresses []uint64
	for i := 1; i <= 192; i++ {
		value, exists := e.Fields[fmt.Sprintf("stack%d", i)]
		if !exists {
			if i == 1 {
				return errors.New("empty_stack")
			}
			break
		}
		address, ok := waitNumber(value)
		if !ok {
			return errors.New("stack_address_schema")
		}
		if address != 0 {
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		return errors.New("empty_stack")
	}
	r.Stacks[key] = waitStack{PID: pid, Addresses: addresses}
	return nil
}
func (r *waitReducer) calculate() {
	sort.SliceStable(r.Transitions, func(i, j int) bool { return r.Transitions[i].Raw < r.Transitions[j].Raw })
	type state struct {
		run, out, ready uint64
		reason          uint32
		waiting         bool
		actor           waitKey
		pid             uint32
	}
	type identity struct {
		pid, tid uint32
		start    uint64
	}
	states := map[identity]state{}
	for _, e := range r.Transitions {
		key := identity{e.PID, e.TID, e.Lifetime}
		s := states[key]
		s.pid = e.PID
		switch e.Kind {
		case "in":
			if s.run != 0 {
				r.Censored[e.PID]++
			}
			if s.waiting && s.out != 0 && s.ready >= s.out {
				r.Intervals = append(r.Intervals, waitInterval{Kind: "blocked", Start: s.out, End: s.ready, In: e.Raw, PID: e.PID, TID: e.TID, Reason: s.reason, Stack: waitKey{s.out, e.TID}, Readying: s.actor})
			} else if s.waiting {
				r.Censored[e.PID]++
			}
			if s.ready != 0 && e.Raw >= s.ready {
				r.Intervals = append(r.Intervals, waitInterval{Kind: "runnable", Start: s.ready, End: e.Raw, PID: e.PID, TID: e.TID})
			}
			s = state{run: e.Raw, pid: e.PID}
		case "out":
			if s.run != 0 && e.Raw >= s.run {
				r.Intervals = append(r.Intervals, waitInterval{Kind: "cpu", Start: s.run, End: e.Raw, PID: e.PID, TID: e.TID})
			} else {
				r.Censored[e.PID]++
			}
			s = state{out: e.Raw, reason: e.Reason, pid: e.PID}
			if e.State == 5 {
				s.waiting = true
			} else if e.State == 1 {
				s.ready = e.Raw
			} else if e.State != 4 {
				r.Censored[e.PID]++
			}
		case "ready":
			if s.ready != 0 || s.run != 0 {
				r.Censored[e.PID]++
				break
			}
			if !s.waiting {
				r.Censored[e.PID]++
			}
			s.ready = e.Raw
			s.actor = waitKey{e.Raw, e.Waker}
		}
		states[key] = s
	}
	for _, s := range states {
		if s.run != 0 || s.waiting || s.ready != 0 {
			r.Censored[s.pid]++
		}
	}
}
func (r *waitReducer) frames(key waitKey) []waitFrame {
	stack, ok := r.Stacks[key]
	if !ok {
		return nil
	}
	var result []waitFrame
	for _, address := range stack.Addresses {
		if len(result) == 32 {
			break
		}
		frame := waitFrame{Module: "unresolved"}
		matches := 0
		for _, image := range r.Images {
			if (image.PID == stack.PID || ((image.PID == 0 || image.PID == 4) && address&(uint64(1)<<63) != 0)) && key.Raw >= image.Start && (image.End == 0 || key.Raw <= image.End) && address >= image.Base && address-image.Base < image.Size {
				matches++
				frame = waitFrame{Module: image.Name, Offset: fmt.Sprintf("0x%x", address-image.Base)}
			}
		}
		if matches != 1 {
			frame = waitFrame{Module: "unresolved"}
		}
		result = append(result, frame)
	}
	return result
}
func (r *waitReducer) summarize() {
	for _, c := range r.Ready.Controls {
		samples, stacks := 0, 0
		for _, sample := range r.Samples {
			if sample.TID == c.Thread && sample.Raw >= c.Start && sample.Raw <= c.End {
				samples++
				if len(r.Stacks[sample].Addresses) > 0 {
					stacks++
				}
			}
		}
		waits := 0
		var longest int64
		switchStacks, readyStacks := 0, 0
		for _, interval := range r.Intervals {
			if interval.Kind == "blocked" && interval.TID == c.Thread && interval.Start >= c.Start && interval.In <= c.End {
				waits++
				ns, _ := waitNS(interval.End-interval.Start, r.Frequency)
				if ns > longest {
					longest = ns
				}
				if len(r.Stacks[interval.Stack].Addresses) > 0 {
					switchStacks++
				}
				if len(r.Stacks[interval.Readying].Addresses) > 0 {
					readyStacks++
				}
			}
		}
		duration, _ := waitNS(c.End-c.Start, r.Frequency)
		valid := samples > 0 && stacks > 0
		if c.Kind == "sleep" {
			valid = waits > 0 && longest >= int64(100*time.Millisecond) && longest <= duration && switchStacks > 0 && readyStacks > 0
		}
		r.Result.Controls = append(r.Result.Controls, map[string]any{"kind": c.Kind, "pid": c.PID, "thread_id": c.Thread, "duration_ns": duration, "samples": samples, "exact_sample_stacks": stacks, "complete_wait_intervals": waits, "longest_blocked_ns": longest, "exact_switchout_stacks": switchStacks, "exact_readying_stacks": readyStacks, "accepted": valid})
	}
	for _, p := range r.Input.Processes {
		record := map[string]any{"role": p.Role, "pid": p.PID, "process_start_utc": r.utc(r.Owned[p.PID].Start), "process_end_utc": r.utc(r.Owned[p.PID].End), "censored_intervals": r.Censored[p.PID]}
		bounds := []struct {
			name       string
			start, end time.Time
		}{{"start", p.Before, p.After}, {"before_script", p.After, p.Script}, {"after_script", p.Script, p.Exit}}
		if p.Script.IsZero() {
			bounds = []struct {
				name       string
				start, end time.Time
			}{{"start", p.Before, p.After}, {"script_not_entered", p.After, p.Exit}}
		}
		var phases []map[string]any
		for _, bound := range bounds {
			totals := map[string]int64{}
			counts := map[string]int{}
			for _, interval := range r.Intervals {
				if interval.PID != p.PID {
					continue
				}
				a, b := r.utc(interval.Start), r.utc(interval.End)
				if a.Before(bound.start) {
					a = bound.start
				}
				if b.After(bound.end) {
					b = bound.end
				}
				if b.After(a) {
					totals[interval.Kind] += b.Sub(a).Nanoseconds()
					counts[interval.Kind]++
				}
			}
			phases = append(phases, map[string]any{"phase": bound.name, "start_utc": bound.start, "end_utc": bound.end, "summed_thread_ns": totals, "phase_wall_ns": bound.end.Sub(bound.start).Nanoseconds(), "complete_intervals": counts})
		}
		record["phases"] = phases
		var waits []waitInterval
		for _, interval := range r.Intervals {
			if interval.PID == p.PID && interval.Kind == "blocked" {
				waits = append(waits, interval)
			}
		}
		sort.Slice(waits, func(i, j int) bool { return waits[i].End-waits[i].Start > waits[j].End-waits[j].Start })
		var longest []map[string]any
		for _, interval := range waits[:min(10, len(waits))] {
			blocked, _ := waitNS(interval.End-interval.Start, r.Frequency)
			ready, _ := waitNS(interval.In-interval.End, r.Frequency)
			longest = append(longest, map[string]any{"thread_id": interval.TID, "start_utc": r.utc(interval.Start), "ready_utc": r.utc(interval.End), "switch_in_utc": r.utc(interval.In), "blocked_ns": blocked, "runnable_ns": ready, "wait_reason": interval.Reason, "switchout_stack": r.frames(interval.Stack), "readying_thread_id": interval.Readying.TID, "readying_stack": r.frames(interval.Readying)})
		}
		record["longest_observed_waits"] = longest
		r.Result.Processes = append(r.Result.Processes, record)
	}
	r.Result.Metadata["exact_stack_joins"] = len(r.Stacks)
	r.Result.Metadata["requested_stack_joins"] = len(r.Wanted)
}
func (r *waitReducer) validate() error {
	if len(r.Result.Controls) != 2 {
		return errors.New("missing_control_results")
	}
	for _, control := range r.Result.Controls {
		if control["accepted"] != true {
			return errors.New("native_schema_control_failed")
		}
	}
	if (r.Status.EventsLost != nil && *r.Status.EventsLost != 0) || r.Result.Metadata["eventslost"] != uint64(0) || r.Result.Metadata["bufferslost"] != uint64(0) {
		return errors.New("recorded_event_loss")
	}
	for _, p := range r.Input.Processes {
		observed := false
		for _, sample := range r.Samples {
			pid, ok := r.thread(sample.TID, sample.Raw)
			if ok && pid == p.PID && len(r.Stacks[sample].Addresses) > 0 {
				observed = true
				break
			}
		}
		if !observed {
			return errors.New("target_sampled_stack_coverage")
		}
		if r.Last < r.Owned[p.PID].End {
			return errors.New("target_retention_gap")
		}
	}
	r.Result.SchemaAccepted = true
	r.Result.ComparisonComplete = len(r.Input.Processes) == 2 && !r.Input.Processes[0].Script.IsZero() && !r.Input.Processes[1].Script.IsZero()
	if !r.Result.ComparisonComplete {
		r.Result.State = "partial"
		return errors.New("partial_target_coverage")
	}
	return nil
}

type waitBudgetReader struct {
	source    io.Reader
	ctx       context.Context
	remaining int
}

func (r *waitBudgetReader) Read(data []byte) (int, error) {
	if r.ctx.Err() != nil {
		return 0, errors.New("reduction_deadline")
	}
	if r.remaining <= 0 {
		return 0, errors.New("xml_event_bound")
	}
	if len(data) > r.remaining {
		data = data[:r.remaining]
	}
	n, err := r.source.Read(data)
	r.remaining -= n
	return n, err
}

type waitUTF16Reader struct {
	source  *bufio.Reader
	order   binary.ByteOrder
	pending []byte
}

func (r *waitUTF16Reader) Read(data []byte) (int, error) {
	written := 0
	for written < len(data) {
		if len(r.pending) > 0 {
			n := copy(data[written:], r.pending)
			written += n
			r.pending = r.pending[n:]
			continue
		}
		var pair [2]byte
		_, err := io.ReadFull(r.source, pair[:])
		if err != nil {
			if written > 0 && err == io.EOF {
				return written, nil
			}
			return written, err
		}
		value := r.order.Uint16(pair[:])
		var char rune
		if value >= 0xd800 && value <= 0xdbff {
			if _, err := io.ReadFull(r.source, pair[:]); err != nil {
				return written, errors.New("xml_utf16")
			}
			low := r.order.Uint16(pair[:])
			if low < 0xdc00 || low > 0xdfff {
				return written, errors.New("xml_utf16")
			}
			char = utf16.DecodeRune(rune(value), rune(low))
		} else {
			if value >= 0xdc00 && value <= 0xdfff {
				return written, errors.New("xml_utf16")
			}
			char = rune(value)
		}
		r.pending = utf8.AppendRune(r.pending, char)
	}
	return written, nil
}

func waitXMLDecoder(ctx context.Context, path string) (*xml.Decoder, *waitBudgetReader, func(), error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, errors.New("xml_open")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > waitXMLLimit {
		file.Close()
		return nil, nil, nil, errors.New("xml_file_bound")
	}
	buffered := bufio.NewReader(file)
	var source io.Reader = buffered
	utf16Decoded := false
	prefix, _ := buffered.Peek(2)
	if len(prefix) == 2 && ((prefix[0] == 0xff && prefix[1] == 0xfe) || (prefix[0] == 0xfe && prefix[1] == 0xff)) {
		utf16Decoded = true
		buffered.Discard(2)
		var order binary.ByteOrder = binary.LittleEndian
		if prefix[0] == 0xfe {
			order = binary.BigEndian
		}
		source = &waitUTF16Reader{source: buffered, order: order}
	}
	budget := &waitBudgetReader{source: source, ctx: ctx, remaining: 128 << 10}
	decoder := xml.NewDecoder(budget)
	decoder.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		if (strings.EqualFold(label, "utf-16") && utf16Decoded) || strings.EqualFold(label, "utf-8") {
			return input, nil
		}
		return nil, errors.New("xml_encoding")
	}
	return decoder, budget, func() { file.Close() }, nil
}

func waitStream(ctx context.Context, path string, visit func(waitEvent) error) error {
	decoder, budget, closeFile, err := waitXMLDecoder(ctx, path)
	if err != nil {
		return err
	}
	defer closeFile()
	events := 0
	for {
		budget.remaining = 128 << 10
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("xml_schema")
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Event" {
			continue
		}
		var wire waitXMLEvent
		budget.remaining = 128 << 10
		if decoder.DecodeElement(&wire, &start) != nil {
			return errors.New("xml_event_schema")
		}
		events++
		if events > 2000000 || len(wire.Data) > 256 {
			return errors.New("xml_count_bound")
		}
		guid := wire.Extended.GUID
		if guid == "" {
			guid = wire.System.Provider.GUID
		}
		guid = strings.ToLower(strings.Trim(guid, "{} \r\n\t"))
		if !waitGUID.MatchString(guid) {
			return errors.New("provider_guid_schema")
		}
		raw := uint64(0)
		if wire.System.Clock.Raw != "" {
			var ok bool
			raw, ok = waitNumber(wire.System.Clock.Raw)
			if !ok {
				return errors.New("raw_timestamp_schema")
			}
		}
		event := waitEvent{GUID: guid, Opcode: wire.System.Opcode, Version: wire.System.Version, PID: wire.System.Execution.PID, TID: wire.System.Execution.TID, CPU: wire.System.Execution.CPU, Raw: raw, Fields: map[string]string{}}
		for _, field := range wire.Data {
			if !waitIdentifier.MatchString(field.Name) {
				continue
			}
			name := strings.ToLower(field.Name)
			if _, exists := event.Fields[name]; exists {
				return errors.New("duplicate_field")
			}
			event.Fields[name] = strings.TrimSpace(field.Value)
		}
		if err := visit(event); err != nil {
			return err
		}
	}
	if events == 0 {
		return errors.New("empty_xml_events")
	}
	return nil
}
func waitErrorCode(err error) string {
	value := err.Error()
	if regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(value) {
		return value
	}
	return "reduction_failed"
}

// Shape inventory never constructs process or thread state.
// It completes before semantic passes, preserving later shapes after a semantic rejection.
type waitXMLInventory struct {
	Complete bool           `json:"complete"`
	Events   int            `json:"events"`
	Tokens   int            `json:"tokens"`
	Shapes   map[string]int `json:"element_attribute_shapes"`
	Fields   map[string]int `json:"field_names"`
	Types    map[string]int `json:"declared_types"`
	Failure  string         `json:"failure,omitempty"`
}

func waitInventory(ctx context.Context, path string) (waitXMLInventory, error) {
	result := waitXMLInventory{Shapes: map[string]int{}, Fields: map[string]int{}, Types: map[string]int{}}
	reject := func(code string) (waitXMLInventory, error) { result.Failure = code; return result, errors.New(code) }
	decoder, budget, closeFile, err := waitXMLDecoder(ctx, path)
	if err != nil {
		return reject(waitErrorCode(err))
	}
	defer closeFile()
	var elements []string
	for {
		budget.remaining = 128 << 10
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return reject("xml_inventory_schema")
		}
		result.Tokens++
		if result.Tokens > 50000000 {
			return reject("xml_inventory_token_bound")
		}
		switch value := token.(type) {
		case xml.StartElement:
			name := value.Name.Local
			if !waitIdentifier.MatchString(name) {
				name = "unrecognized_element"
			}
			elements = append(elements, name)
			if len(elements) > 32 || len(value.Attr) > 64 {
				return reject("xml_inventory_depth_or_attribute_bound")
			}
			if name == "Event" {
				result.Events++
				if result.Events > 2000000 {
					return reject("xml_inventory_event_bound")
				}
			}
			var attributes []string
			for _, attr := range value.Attr {
				attrName := attr.Name.Local
				if !waitIdentifier.MatchString(attrName) {
					attrName = "unrecognized_attribute"
				}
				attributes = append(attributes, attrName)
				if name == "Data" && attrName == "Name" && waitIdentifier.MatchString(attr.Value) {
					if _, exists := result.Fields[attr.Value]; !exists && len(result.Fields) >= 512 {
						return reject("xml_inventory_field_bound")
					}
					result.Fields[attr.Value]++
				}
				if attrName == "Type" {
					kind := attr.Value
					base := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(kind, "win:"), "xs:"), "xsd:")
					if !slices.Contains([]string{"UnicodeString", "AnsiString", "UInt8", "UInt16", "UInt32", "UInt64", "Int8", "Int16", "Int32", "Int64", "HexInt32", "HexInt64", "Pointer", "Boolean", "Float", "Double", "Binary", "GUID", "FILETIME", "SYSTEMTIME", "SID", "SizeT", "unsignedInt", "unsignedLong", "long", "int", "string", "dateTime", "hexBinary", "base64Binary"}, base) {
						kind = "unrecognized_type"
					}
					result.Types[kind]++
				}
			}
			sort.Strings(attributes)
			shape := strings.Join(elements, "/") + "[" + strings.Join(attributes, ",") + "]"
			if len(shape) > 512 {
				return reject("xml_inventory_shape_length_bound")
			}
			if _, exists := result.Shapes[shape]; !exists && len(result.Shapes) >= 512 {
				return reject("xml_inventory_shape_count_bound")
			}
			result.Shapes[shape]++
		case xml.EndElement:
			if len(elements) == 0 {
				return reject("xml_inventory_nesting")
			}
			elements = elements[:len(elements)-1]
		}
	}
	if result.Events == 0 || len(elements) != 0 {
		return reject("xml_inventory_empty_or_incomplete")
	}
	result.Complete = true
	return result, nil
}
