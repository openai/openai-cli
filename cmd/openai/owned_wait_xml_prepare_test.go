package main

// Temporary XML release transaction. The original reducer remains unchanged.
import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type ownedXMLBinding struct {
	XMLHash   string `json:"xml_sha256"`
	TraceHash string `json:"trace_sha256"`
	InputHash string `json:"input_sha256"`
	ReadyHash string `json:"readiness_sha256"`
	Bytes     int64  `json:"xml_bytes"`
}

type ownedXMLPreparation struct {
	Cleanup        string              `json:"cleanup"`
	ReceiptFailure bool                `json:"receipt_publish_failed"`
	Version        int                 `json:"schema_version"`
	Binding        ownedXMLBinding     `json:"binding"`
	State          string              `json:"state"`
	Failure        string              `json:"failure,omitempty"`
	Complete       bool                `json:"scan_complete"`
	Released       int64               `json:"released_bytes"`
	Events         int                 `json:"events_scanned"`
	AnchorState    string              `json:"anchor_state"`
	Anchors        []map[string]uint64 `json:"anchors"`
}

type ownedXMLFile interface {
	io.Reader
	Stat() (os.FileInfo, error)
	Close() error
	MarkDelete() error
}

func ownedXMLReadJSON(directory, name string, value any) ([]byte, error) {
	file, err := os.Open(filepath.Join(directory, name))
	if err != nil {
		return nil, errors.New("input_read")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return nil, errors.New("input_bound")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, value) != nil {
		return nil, errors.New("input_schema")
	}
	return data, nil
}

func ownedXMLInputs(directory string) (ownedXMLBinding, map[uint32]bool, error) {
	var binding ownedXMLBinding
	var exported struct {
		State string `json:"state"`
		Hash  string `json:"xml_sha256"`
		Bytes int64  `json:"xml_bytes"`
	}
	var trace struct {
		State string `json:"state"`
		Hash  string `json:"trace_sha256"`
	}
	var ready waitReady
	var input waitInput
	if _, err := ownedXMLReadJSON(directory, "wait-export-status.json", &exported); err != nil {
		return binding, nil, err
	}
	if _, err := ownedXMLReadJSON(directory, "wait-trace-status.json", &trace); err != nil {
		return binding, nil, err
	}
	readyData, err := ownedXMLReadJSON(directory, "wait-trace-ready.json", &ready)
	if err != nil {
		return binding, nil, err
	}
	inputData, err := ownedXMLReadJSON(directory, "wait-trace-input.json", &input)
	if err != nil {
		return binding, nil, err
	}
	if exported.State != "exported" || !xperfHash.MatchString(exported.Hash) || exported.Bytes <= 0 || exported.Bytes > waitXMLLimit || trace.State != "recorded" || !xperfHash.MatchString(trace.Hash) {
		return binding, nil, errors.New("input_binding")
	}
	if !ready.Ready || ready.PID == 0 || len(ready.Controls) != 2 || len(input.Processes) > 2 {
		return binding, nil, errors.New("input_identity")
	}
	pids := map[uint32]bool{ready.PID: true}
	for _, control := range ready.Controls {
		if control.PID != ready.PID || control.Thread == 0 {
			return binding, nil, errors.New("control_identity")
		}
	}
	for _, process := range input.Processes {
		if process.PID == 0 || pids[process.PID] {
			return binding, nil, errors.New("target_identity")
		}
		pids[process.PID] = true
	}
	digest := func(data []byte) string { hash := sha256.Sum256(data); return hex.EncodeToString(hash[:]) }
	binding = ownedXMLBinding{exported.Hash, trace.Hash, digest(inputData), digest(readyData), exported.Bytes}
	return binding, pids, nil
}

func ownedXMLPublish(directory string, result ownedXMLPreparation) error {
	data, err := json.Marshal(result)
	if err != nil || len(data) > 1<<20 {
		return errors.New("safe_encoding")
	}
	path := filepath.Join(directory, "wait-xml-preparation.json")
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return errors.New("safe_write")
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return errors.New("safe_publish")
	}
	return nil
}

func prepareOwnedXML(ctx context.Context, directory string, open func(string) (ownedXMLFile, error), publish func(ownedXMLPreparation) error) (result ownedXMLPreparation) {
	result = ownedXMLPreparation{Version: 1, State: "failed", AnchorState: "unavailable", Cleanup: "not_opened"}
	fail := func(code string) ownedXMLPreparation {
		result.State, result.Failure = "failed", code
		if publish(result) != nil {
			result.ReceiptFailure = true
		}
		return result
	}
	binding, pids, err := ownedXMLInputs(directory)
	if err != nil {
		return fail(err.Error())
	}
	result.Binding = binding
	if ctx.Err() != nil {
		return fail("cancelled")
	}
	file, err := open(filepath.Join(directory, "private-wait.xml"))
	if err != nil {
		return fail("xml_open_identity")
	}
	closed := false
	result.Cleanup = "handle_open"
	defer func() {
		if !closed {
			result.Cleanup = "confirmed"
			if file.Close() != nil {
				result.Cleanup = "close_failed"
			}
			if publish(result) != nil {
				result.ReceiptFailure = true
			}
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != binding.Bytes {
		return fail("xml_size_mismatch")
	}
	hash := sha256.New()
	counted := &ownedXMLCountReader{reader: io.TeeReader(io.LimitReader(file, binding.Bytes+1), hash)}
	if err := scanOwnedXML(ctx, counted, pids, &result); err != nil {
		return fail(err.Error())
	}
	if counted.bytes != binding.Bytes || hex.EncodeToString(hash.Sum(nil)) != binding.XMLHash {
		return fail("xml_hash_mismatch")
	}
	if ctx.Err() != nil {
		return fail("cancelled")
	}
	result.Complete, result.State = true, "prepared"
	if publish(result) != nil {
		return fail("prepared_publish")
	}
	if ctx.Err() != nil {
		return fail("cancelled")
	}
	if file.MarkDelete() != nil {
		return fail("xml_disposition")
	}
	err = file.Close()
	closed = true
	result.Cleanup = "confirmed"
	if err != nil {
		result.Cleanup = "close_failed"
		return fail("xml_close")
	}
	if _, err := os.Lstat(filepath.Join(directory, "private-wait.xml")); !os.IsNotExist(err) {
		return fail("xml_release_unconfirmed")
	}
	result.State, result.Released = "released", binding.Bytes
	if publish(result) != nil {
		return fail("released_publish")
	}
	return result
}

type ownedXMLCountReader struct {
	reader io.Reader
	bytes  int64
}

func (r *ownedXMLCountReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	return n, err
}

func scanOwnedXML(ctx context.Context, reader io.Reader, pids map[uint32]bool, result *ownedXMLPreparation) error {
	buffered := bufio.NewReader(reader)
	var source io.Reader = buffered
	utf16Decoded := false
	prefix, _ := buffered.Peek(2)
	if len(prefix) == 2 && ((prefix[0] == 0xff && prefix[1] == 0xfe) || (prefix[0] == 0xfe && prefix[1] == 0xff)) {
		utf16Decoded = true
		_, _ = buffered.Discard(2)
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
	depth, roots := 0, 0
	for {
		if ctx.Err() != nil {
			return errors.New("cancelled")
		}
		budget.remaining = 128 << 10
		token, err := decoder.Token()
		if err == io.EOF {
			if depth != 0 || roots != 1 {
				return errors.New("xml_document")
			}
			if len(result.Anchors) > 0 {
				result.AnchorState = "observed_unverified"
			}
			return nil
		}
		if err != nil {
			return errors.New("xml_scan")
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				roots++
				if roots != 1 {
					return errors.New("xml_document")
				}
			}
			if value.Name.Local != "Event" || depth != 2 {
				continue
			}
			result.Events++
			if result.Events > 2000000 {
				return errors.New("event_bound")
			}
			var event ownedXMLAnchorEvent
			budget.remaining = 128 << 10
			if decoder.DecodeElement(&event, &value) != nil {
				return errors.New("xml_event")
			}
			depth--
			guid, opcode, raw, valid := event.identity()
			if !valid || guid != waitProcessGUID || !slices.Contains([]uint64{1, 2, 3, 4}, opcode) {
				continue
			}
			var pid uint64
			seen := false
			for _, field := range event.Data {
				if strings.EqualFold(field.Name, "ProcessId") {
					if seen {
						valid = false
					}
					number, ok := waitNumber(field.Value)
					pid = number
					valid = valid && ok
					seen = true
				}
			}
			if !valid || !seen || pid > math.MaxUint32 || !pids[uint32(pid)] {
				continue
			}
			if len(result.Anchors) >= 64 {
				return errors.New("anchor_bound")
			}
			result.Anchors = append(result.Anchors, map[string]uint64{"pid": pid, "opcode": opcode, "raw_qpc": raw})
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(value)) != "" {
				return errors.New("xml_document")
			}
		case xml.Directive:
			return errors.New("xml_directive")
		}
	}
}

func (r *xperfReader) xmlAnchors(directory string) {
	r.result.AnchorState = "unavailable"
	var prepared ownedXMLPreparation
	if _, err := ownedXMLReadJSON(directory, "wait-xml-preparation.json", &prepared); err != nil {
		return
	}
	binding, _, err := ownedXMLInputs(directory)
	if err != nil || prepared.Binding != binding {
		r.result.AnchorState = "saved_binding_mismatch"
		return
	}
	if prepared.Version != 1 || prepared.State != "released" || !prepared.Complete || prepared.Released != binding.Bytes || prepared.Failure != "" || prepared.Cleanup != "confirmed" || prepared.ReceiptFailure {
		r.result.AnchorState = "release_unconfirmed"
		return
	}
	if !slices.Contains([]string{"unavailable", "observed_unverified"}, prepared.AnchorState) || len(prepared.Anchors) > 64 {
		r.result.AnchorState = "saved_anchor_schema"
		return
	}
	if (len(prepared.Anchors) == 0) != (prepared.AnchorState == "unavailable") {
		r.result.AnchorState = "saved_anchor_schema"
		return
	}
	anchors := make([]map[string]uint64, 0, len(prepared.Anchors))
	for _, anchor := range prepared.Anchors {
		pid, pidOK := anchor["pid"]
		opcode, opcodeOK := anchor["opcode"]
		raw, rawOK := anchor["raw_qpc"]
		if len(anchor) != 3 || !pidOK || !opcodeOK || !rawOK || pid > math.MaxUint32 || !r.pids[uint32(pid)] || !slices.Contains([]uint64{1, 2, 3, 4}, opcode) {
			r.result.AnchorState = "saved_anchor_schema"
			return
		}
		anchors = append(anchors, map[string]uint64{"pid": pid, "opcode": opcode, "raw_qpc": raw})
	}
	r.result.Anchors = anchors
	r.result.AnchorState = prepared.AnchorState
}

// Unknown numeric metadata cannot prevent a complete structural XML scan.
// Anchors require exactly one reviewed identity element and attribute.
type ownedXMLAnchorEvent struct {
	System []struct {
		Provider []struct {
			Attributes []xml.Attr `xml:",any,attr"`
		} `xml:"Provider"`
		Opcode []string `xml:"Opcode"`
		Clock  []struct {
			Attributes []xml.Attr `xml:",any,attr"`
		} `xml:"TimeCreated"`
	} `xml:"System"`
	Data     []waitXMLValue `xml:"EventData>Data"`
	Extended []struct {
		GUIDs []string `xml:"EventGuid"`
	} `xml:"ExtendedTracingInfo"`
}

func (event ownedXMLAnchorEvent) identity() (string, uint64, uint64, bool) {
	if len(event.System) != 1 || len(event.Extended) > 1 {
		return "", 0, 0, false
	}
	system := event.System[0]
	if len(system.Opcode) != 1 || len(system.Clock) != 1 {
		return "", 0, 0, false
	}
	attribute := func(attributes []xml.Attr, name string) (string, bool) {
		value, count := "", 0
		for _, item := range attributes {
			if item.Name.Local == name {
				value = item.Value
				count++
			}
		}
		return value, count == 1
	}
	guid := ""
	if len(event.Extended) == 1 {
		if len(event.Extended[0].GUIDs) != 1 {
			return "", 0, 0, false
		}
		guid = event.Extended[0].GUIDs[0]
	} else {
		if len(system.Provider) != 1 {
			return "", 0, 0, false
		}
		var valid bool
		guid, valid = attribute(system.Provider[0].Attributes, "Guid")
		if !valid {
			return "", 0, 0, false
		}
	}
	text, valid := attribute(system.Clock[0].Attributes, "RawTime")
	raw, rawOK := waitNumber(text)
	opcode, opcodeOK := waitNumber(system.Opcode[0])
	return strings.ToLower(strings.Trim(guid, "{} \r\n\t")), opcode, raw, valid && rawOK && opcodeOK
}
