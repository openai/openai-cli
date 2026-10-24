package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedWaitXperfV2ImageControls(t *testing.T) {
	const imageFields = "TimeStamp,Process Name ( PID),BaseAddr,EndAddr,FileName"
	const samples = "SampledProfile,TimeStamp,PID,ThreadID,PrgrmCtr,CPU,Count\n"
	header := "BeginHeader\n"
	for _, kind := range []string{"I-Start", "I-End", "I-DCStart", "I-DCEnd"} {
		header += kind + "," + imageFields + "\n"
	}
	header += samples + "EndHeader\n"
	fixture := func(t *testing.T, text string) xperfEvidence {
		t.Helper()
		dir := t.TempDir()
		values := map[string]any{
			"wait-trace-ready.json":         map[string]any{"ready": true, "recorder_pid": 100, "controls": []map[string]any{{"kind": "cpu", "pid": 100, "thread_id": 7}, {"kind": "sleep", "pid": 100, "thread_id": 7}}},
			"wait-trace-input.json":         map[string]any{"processes": []map[string]any{{"role": "powershell_direct", "pid": 200}, {"role": "powershell_pipeline", "pid": 300}}},
			"wait-xperf-export-status.json": map[string]any{"state": "exported", "completion": "complete", "cleanup": "confirmed", "csv_bytes": len(text), "csv_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(text))), "help_validation": "recognized_command_shape"},
		}
		for name, value := range values {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "private-events.csv"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		return readXperfEvidence(t.Context(), dir)
	}
	sample := func(stamp, address string) string { return "SampledProfile," + stamp + ",200,8," + address + ",0,1\n" }
	frame := func(t *testing.T, r xperfEvidence, stamp string) map[string]any {
		t.Helper()
		for _, row := range r.Rows {
			s := r.Schemas[row[0].(int)]
			if s.Kind != "SampledProfile" {
				continue
			}
			if row[4] != stamp {
				continue
			}
			value, ok := row[7].(map[string]any)
			if !ok {
				t.Fatal("missing pointer token")
			}
			return value
		}
		t.Fatal("missing sample")
		return nil
	}
	t.Run("declared_image_names_and_private_range", func(t *testing.T) {
		r := fixture(t, header+"I-Start,1,powershell.exe ( 200),0x1000,0x2000,C:\\private\\ntdll.dll\n"+sample("2", "0x1010"))
		if !r.Complete || len(r.CandidateImages) != 1 {
			t.Fatal("missing image")
		}
		im := r.CandidateImages[0]
		if im.Kind != "I-Start" || im.Span != 4096 || im.Size != 0 || im.Endpoint != "unknown_strict_interior_only" || im.endToken != "0x2000" {
			t.Fatal("range interpretation changed")
		}
		if frame(t, r, "2")["offset"] != "0x10" {
			t.Fatal("missing interior mapping")
		}
		b, _ := json.Marshal(r)
		for _, private := range []string{"0x1000", "0x2000", "0x1010", `C:\private`} {
			if strings.Contains(string(b), private) {
				t.Fatal("private range escaped")
			}
		}
		if r.Schemas[0].Kind != "I-Start" || r.Schemas[0].Declared[3] != "EndAddr" {
			t.Fatal("declaration changed")
		}
	})
	t.Run("both_endpoints_remain_unmapped", func(t *testing.T) {
		r := fixture(t, header+"I-Start,1,powershell.exe (200),0x1000,0x2000,ntdll.dll\n"+sample("2", "0x1000")+sample("3", "0x2000"))
		for _, stamp := range []string{"2", "3"} {
			if _, ok := frame(t, r, stamp)["image"]; ok {
				t.Fatal("unknown endpoint mapped")
			}
		}
	})
	t.Run("rundown_start_is_not_an_earlier_load", func(t *testing.T) {
		r := fixture(t, header+"I-DCStart,3,powershell.exe (200),0x1000,0x2000,ntdll.dll\n"+sample("2", "0x1010")+sample("4", "0x1010"))
		if r.CandidateImages[0].Kind != "I-DCStart" {
			t.Fatal("rundown identity lost")
		}
		if _, ok := frame(t, r, "2")["image"]; ok {
			t.Fatal("rundown invented earlier lifetime")
		}
		if _, ok := frame(t, r, "4")["image"]; !ok {
			t.Fatal("post-rundown candidate missing")
		}
	})
	t.Run("rundown_end_preserves_observation_without_mapping", func(t *testing.T) {
		r := fixture(t, header+"I-DCEnd,5,powershell.exe (200),0x1000,0x2000,ntdll.dll\n"+sample("4", "0x1010"))
		if len(r.CandidateImages) != 1 || r.CandidateImages[0].Kind != "I-DCEnd" || r.CandidateImages[0].Observed != "5" || r.CandidateImages[0].Start != "" {
			t.Fatal("rundown end lost")
		}
		if _, ok := frame(t, r, "4")["image"]; ok {
			t.Fatal("rundown end invented lifetime")
		}
	})
	t.Run("unload_stops_mapping_and_retains_observation", func(t *testing.T) {
		r := fixture(t, header+"I-Start,1,powershell.exe (200),0x1000,0x2000,ntdll.dll\nI-End,3,powershell.exe (200),0x1000,0x2000,ntdll.dll\n"+sample("2", "0x1010")+sample("4", "0x1010"))
		if len(r.CandidateImages) != 2 || r.CandidateImages[1].Kind != "I-End" || r.CandidateImages[0].End != "3" {
			t.Fatal("unload identity lost")
		}
		if _, ok := frame(t, r, "2")["image"]; !ok {
			t.Fatal("pre-unload candidate missing")
		}
		if _, ok := frame(t, r, "4")["image"]; ok {
			t.Fatal("unloaded image mapped")
		}
	})
	t.Run("reversed_and_empty_ranges_reject_mapping", func(t *testing.T) {
		for _, end := range []string{"0x0800", "0x1000"} {
			r := fixture(t, header+"I-Start,1,powershell.exe (200),0x1000,"+end+",ntdll.dll\n")
			if len(r.CandidateImages) != 0 {
				t.Fatal("invalid range accepted")
			}
		}
	})
	t.Run("conflicting_base_aliases_reject_mapping", func(t *testing.T) {
		text := "BeginHeader\nI-Start,TimeStamp,PID,BaseAddr,ImageBase,EndAddr,FileName\nEndHeader\nI-Start,1,200,0x1000,0x1100,0x2000,ntdll.dll\n"
		r := fixture(t, text)
		if len(r.CandidateImages) != 0 {
			t.Fatal("conflicting aliases accepted")
		}
	})
	t.Run("duplicate_endaddr_rejects_schema", func(t *testing.T) {
		r := fixture(t, "BeginHeader\nI-Start,TimeStamp,PID,BaseAddr,EndAddr,EndAddr,FileName\nEndHeader\nI-Start,1,200,0x1000,0x2000,0x3000,ntdll.dll\n")
		if len(r.CandidateImages) != 0 || r.Schemas[0].Failure != "duplicate_known_column" {
			t.Fatal("duplicate endpoint accepted")
		}
	})
	t.Run("unreviewed_image_kind_remains_count_only", func(t *testing.T) {
		r := fixture(t, strings.ReplaceAll(header, "I-Start", "I-Other")+"I-Other,1,powershell.exe (200),0x1000,0x2000,ntdll.dll\n")
		if len(r.CandidateImages) != 0 || r.UnknownKinds == 0 {
			t.Fatal("unknown family gained interpretation")
		}
	})
	t.Run("reservation_budgets_and_unique_order", func(t *testing.T) {
		r := fixture(t, header+"I-Start,1,powershell.exe (200),0x1000,0x2000,ntdll.dll\n"+sample("2", "0x1010"))
		total, tail := 0, 0
		for _, b := range r.Reservations {
			total += b.Limit
			tail += b.TailLimit
			if b.Retained+b.Omitted != b.Matched {
				t.Fatal("membership counts differ")
			}
		}
		if total != 2<<20 || tail != 192<<10 || len(r.Reservations) != 12 {
			t.Fatal("reservation budgets changed")
		}
		if r.Matched != len(r.Rows)+r.Omitted {
			t.Fatal("unique counts differ")
		}
		last := 0
		for _, row := range r.Rows {
			ordinal := row[1].(int)
			if ordinal <= last {
				t.Fatal("rows lost unique order")
			}
			last = ordinal
		}
		b, _ := json.Marshal(r)
		if len(b) > xperfEvidenceLimit {
			t.Fatal("artifact bound exceeded")
		}
	})
}
