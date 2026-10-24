package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ownedXMLFixtureDocument = `<Events><Event><System><Provider Guid="{3d6fa8d0-fe05-11d0-9dda-00c04fd7ba7c}"/><Opcode>1</Opcode><TimeCreated RawTime="42"/></System><EventData><Data Name="ProcessId">200</Data><Data Name="PrivateData">private-sentinel</Data></EventData></Event></Events>`

func ownedXMLFixture(t *testing.T, document string) string {
	t.Helper()
	dir := t.TempDir()
	hash := sha256.Sum256([]byte(document))
	records := map[string]any{
		"wait-export-status.json": map[string]any{"state": "exported", "xml_sha256": hex.EncodeToString(hash[:]), "xml_bytes": len(document)},
		"wait-trace-status.json":  map[string]any{"state": "recorded", "trace_sha256": strings.Repeat("1", 64)},
		"wait-trace-ready.json":   map[string]any{"ready": true, "recorder_pid": 100, "controls": []map[string]any{{"kind": "cpu", "pid": 100, "thread_id": 7}, {"kind": "sleep", "pid": 100, "thread_id": 7}}},
		"wait-trace-input.json":   map[string]any{"processes": []map[string]any{{"role": "powershell_direct", "pid": 200}}},
	}
	for name, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "private-wait.xml"), []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

type ownedXMLControlFile struct {
	*os.File
	marked          bool
	deleteError     bool
	closeError      bool
	readError       bool
	pendingDeletion bool
	onRead          func()
}

func (f *ownedXMLControlFile) Read(p []byte) (int, error) {
	if f.onRead != nil {
		callback := f.onRead
		f.onRead = nil
		callback()
	}
	if f.readError {
		return 0, errors.New("private-read-error")
	}
	return f.File.Read(p)
}
func (f *ownedXMLControlFile) MarkDelete() error {
	if f.deleteError {
		return errors.New("private-deletion-error")
	}
	f.marked = true
	return nil
}
func (f *ownedXMLControlFile) Close() error {
	err := f.File.Close()
	if err == nil && f.marked && !f.pendingDeletion {
		err = os.Remove(f.Name())
	}
	if f.closeError {
		return errors.New("private-close-error")
	}
	return err
}

func TestOwnedWaitXMLPreparationControls(t *testing.T) {
	cases := []struct {
		name, document, failure string
		mutate                  string
		failPublish             int
		failDelete              bool
		failRead                bool
		failClose               bool
		pendingDeletion         bool
		cancelPhase             string
		removed                 bool
	}{
		{name: "success", document: ownedXMLFixtureDocument, removed: true},
		{name: "hash_mismatch", document: ownedXMLFixtureDocument, mutate: "hash", failure: "xml_hash_mismatch"},
		{name: "size_mismatch", document: ownedXMLFixtureDocument, mutate: "size", failure: "xml_size_mismatch"},
		{name: "invalid_xml", document: "<Events><Event></Events>", failure: "xml_event"},
		{name: "prepared_publication_failure", document: ownedXMLFixtureDocument, failPublish: 1, failure: "prepared_publish"},
		{name: "release_publication_failure", document: ownedXMLFixtureDocument, failPublish: 2, failure: "released_publish", removed: true},
		{name: "deletion_failure", document: ownedXMLFixtureDocument, failDelete: true, failure: "xml_disposition"},
		{name: "cancel_before_open", document: ownedXMLFixtureDocument, cancelPhase: "before", failure: "cancelled"},
		{name: "cancel_after_prepared", document: ownedXMLFixtureDocument, cancelPhase: "prepared", failure: "cancelled"},
		{name: "missing_decoded_anchor", document: "<Events><Event><BinaryEventData>private-sentinel</BinaryEventData></Event></Events>", removed: true},
		{name: "invalid_decoded_anchor", document: strings.ReplaceAll(ownedXMLFixtureDocument, "RawTime=\"42\"", "RawTime=\"invalid-private-value\""), removed: true},
		{name: "unknown_opcode", document: strings.ReplaceAll(ownedXMLFixtureDocument, "<Opcode>1</Opcode>", "<Opcode>private-unknown</Opcode>"), removed: true},
		{name: "duplicate_clock", document: strings.ReplaceAll(ownedXMLFixtureDocument, `RawTime="42"`, `RawTime="42" RawTime="43"`), removed: true},
		{name: "duplicate_pid", document: strings.ReplaceAll(ownedXMLFixtureDocument, `<Data Name="ProcessId">200</Data>`, `<Data Name="ProcessId">200</Data><Data Name="ProcessId">200</Data>`), removed: true},

		{name: "read_failure", document: ownedXMLFixtureDocument, failRead: true, failure: "xml_scan"},
		{name: "close_failure", document: ownedXMLFixtureDocument, failClose: true, failure: "xml_close", removed: true},
		{name: "primary_and_cleanup_failure", document: ownedXMLFixtureDocument, mutate: "hash", failClose: true, failure: "xml_hash_mismatch"},
		{name: "cancel_during_read", document: ownedXMLFixtureDocument, cancelPhase: "read", failure: "cancelled"},

		{name: "pending_deletion", document: ownedXMLFixtureDocument, pendingDeletion: true, failure: "xml_release_unconfirmed"},
		{name: "multiple_roots", document: "<Events/><Events/>", failure: "xml_document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := ownedXMLFixture(t, tc.document)
			if tc.mutate != "" {
				var status map[string]any
				path := filepath.Join(dir, "wait-export-status.json")
				data, _ := os.ReadFile(path)
				json.Unmarshal(data, &status)
				if tc.mutate == "hash" {
					status["xml_sha256"] = strings.Repeat("0", 64)
				} else {
					status["xml_bytes"] = len(tc.document) + 1
				}
				data, _ = json.Marshal(status)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelPhase == "before" {
				cancel()
			}
			publications := 0
			result := prepareOwnedXML(ctx, dir, func(path string) (ownedXMLFile, error) {
				file, err := os.Open(path)
				if err != nil {
					return nil, err
				}
				control := &ownedXMLControlFile{File: file, deleteError: tc.failDelete, readError: tc.failRead, closeError: tc.failClose, pendingDeletion: tc.pendingDeletion}
				if tc.cancelPhase == "read" {
					control.onRead = cancel
				}
				return control, nil
			}, func(result ownedXMLPreparation) error {
				publications++
				if publications == tc.failPublish {
					return errors.New("private-publication-error")
				}
				err := ownedXMLPublish(dir, result)
				if result.State == "prepared" && tc.cancelPhase == "prepared" {
					cancel()
				}
				return err
			})
			if result.Failure != tc.failure || (result.State == "released") != (tc.failure == "") {
				t.Fatalf("unexpected state=%s failure=%s", result.State, result.Failure)
			}
			_, statErr := os.Stat(filepath.Join(dir, "private-wait.xml"))
			if os.IsNotExist(statErr) != tc.removed {
				t.Fatal("unexpected XML preservation")
			}
			data, err := os.ReadFile(filepath.Join(dir, "wait-xml-preparation.json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"private-sentinel", "private-publication-error", "private-deletion-error", "private-read-error", "private-close-error", "invalid-private-value", dir} {
				if strings.Contains(string(data), secret) {
					t.Fatal("private value escaped")
				}
			}
			var saved ownedXMLPreparation
			if json.Unmarshal(data, &saved) != nil || saved.State != result.State || saved.Failure != result.Failure {
				t.Fatal("failure receipt mismatch")
			}
			if tc.failClose && (saved.Cleanup != "close_failed" || saved.Released != 0) {
				t.Fatal("close failure was hidden")
			}
			if tc.name == "success" {
				if !saved.Complete || saved.Released != int64(len(tc.document)) || len(saved.Anchors) != 1 {
					t.Fatal("incomplete successful release")
				}
				reader := xperfReader{pids: map[uint32]bool{100: true, 200: true}}
				reader.xmlAnchors(dir)
				if reader.result.AnchorState != "observed_unverified" || len(reader.result.Anchors) != 1 {
					t.Fatal("saved anchors require deleted XML")
				}
				path := filepath.Join(dir, "wait-trace-input.json")
				changed := []byte(`{"processes":[{"role":"powershell_pipeline","pid":200}]}`)
				if os.WriteFile(path, changed, 0600) != nil {
					t.Fatal("fixture replacement failed")
				}
				reader.result.Anchors = nil
				reader.xmlAnchors(dir)
				if reader.result.AnchorState != "saved_binding_mismatch" || len(reader.result.Anchors) != 0 {
					t.Fatal("manifest replay accepted")
				}
			}
			if tc.name == "missing_decoded_anchor" || tc.name == "invalid_decoded_anchor" || tc.name == "unknown_opcode" || tc.name == "duplicate_clock" || tc.name == "duplicate_pid" {
				if saved.AnchorState != "unavailable" || len(saved.Anchors) != 0 {
					t.Fatal("missing anchor became accepted")
				}
			}
		})
	}
}

func TestOwnedWaitXperfCSVRejectionControls(t *testing.T) {
	for _, tc := range []struct{ name, input, category, kind string }{
		{"bare_quote", "SampledProfile,1,private\"payload\n", "bare_quote", "SampledProfile"},
		{"unfinished_quote", "ReadyThread,1,\"private-payload", "quote", "ReadyThread"},
		{"unknown_family", "private-family,1,private\"payload\n", "bare_quote", ""},
		{"budget", "Stack,1,\"" + strings.Repeat("private-payload", 12000), "reader_budget", "Stack"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := xperfReader{ctx: t.Context()}
			_, err := reader.scan(strings.NewReader(tc.input), true)
			if err == nil || reader.result.CSVRejection == nil {
				t.Fatal("missing rejection")
			}
			rejection := reader.result.CSVRejection
			if rejection.Category != tc.category || rejection.Kind != tc.kind || rejection.Pass != "discovery" || rejection.Fields != 2 {
				t.Fatalf("wrong safe category: %+v", rejection)
			}
			if tc.category != "reader_budget" && (rejection.Line != 1 || rejection.StartLine != 1 || rejection.Column <= 0) {
				t.Fatal("missing parse positions")
			}
			data, _ := json.Marshal(rejection)
			if strings.Contains(string(data), "private") {
				t.Fatal("private CSV value escaped")
			}
		})
	}
}

func TestOwnedWaitXperfCSVDeclaredFailureControl(t *testing.T) {
	input := "BeginHeader\nP-Start,TimeStamp,Process Name (PID),CommandLine\nEndHeader\nP-Start,1,owned.exe (100),private\"payload\n"
	reader := xperfReader{ctx: t.Context(), index: map[string]int{}}
	_, err := reader.scan(strings.NewReader(input), true)
	if err == nil || reader.result.CSVRejection == nil {
		t.Fatal("missing CSV failure")
	}
	failure := reader.result.CSVRejection
	if failure.ColumnName != "CommandLine" || failure.Ordinal != 4 || failure.Offset <= 0 || failure.Line != 4 || failure.Fields != 3 || failure.Kind != "P-Start" {
		t.Fatalf("wrong safe coordinates: %+v", failure)
	}
	data, _ := json.Marshal(failure)
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "owned.exe") {
		t.Fatal("private CSV value escaped")
	}
}
