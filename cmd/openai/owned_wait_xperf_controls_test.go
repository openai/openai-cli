package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedWaitXperfEvidenceControls(t *testing.T) {
	const header = "BeginHeader\nT-Start,TimeStamp,Process Name (PID),ThreadID\nP-Start,TimeStamp,Process Name (PID),ParentId\nCSwitch,TimeStamp,New TID,Old TID,OldState,Wait Reason,CPU\nReadyThread,TimeStamp,ThreadID,TThreadId,CPU\nSampledProfile,TimeStamp,Process Name (PID),ThreadID,PrgrmCtr,CPU,Image!Function,Count,PrivateData\nStack,TimeStamp,ThreadID,No.,Address,Image!Function\nImageLoad,TimeStamp,ProcessID,ImageBase,ImageSize,FileName\nImageUnload,TimeStamp,ProcessID,ImageBase,ImageSize,FileName\nEndHeader\n"
	const data = "T-Start,1,secret-runner.exe (100),7\nT-Start,1,powershell.exe (200),8\nImageLoad,1,100,0x1000,4096,C:\\private\\ntdll.dll\nSampledProfile,2.125,secret-runner.exe (100),7,0x1010,0,ntdll.dll!secret_symbol,1,secret-payload\nStack,2.125,7,0,0x1010,ntdll.dll!secret_symbol\nCSwitch,3,8,99,Waiting,DelayExecution,0\nReadyThread,4,99,7,0\n"
	write := func(t *testing.T, csvText string, mutate func(map[string]any)) string {
		t.Helper()
		dir := t.TempDir()
		records := map[string]any{
			"wait-trace-ready.json": map[string]any{"ready": true, "recorder_pid": 100, "controls": []map[string]any{{"kind": "cpu", "pid": 100, "thread_id": 7}, {"kind": "sleep", "pid": 100, "thread_id": 7}}},
			"wait-trace-input.json": map[string]any{"processes": []map[string]any{{"role": "powershell_direct", "pid": 200}}},
		}
		status := map[string]any{"state": "exported", "completion": "complete", "cleanup": "confirmed", "csv_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(csvText))), "csv_bytes": len(csvText), "help_validation": "recognized_command_shape"}
		if mutate != nil {
			mutate(status)
		}
		records["wait-xperf-export-status.json"] = status
		for name, record := range records {
			b, _ := json.Marshal(record)
			if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "private-events.csv"), []byte(csvText), 0600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	rows := func(r xperfEvidence, kind string) [][]any {
		out := [][]any{}
		for _, row := range r.Rows {
			if r.Schemas[row[0].(int)].Kind == kind {
				out = append(out, row)
			}
		}
		return out
	}
	pointer := func(r xperfEvidence, row []any, label string) any {
		schema := r.Schemas[row[0].(int)]
		for i, column := range schema.Columns {
			if column == label {
				return row[i]
			}
		}
		return nil
	}
	t.Run("partial_numeric_evidence_and_private_values", func(t *testing.T) {
		r := readXperfEvidence(t.Context(), write(t, header+data, nil))
		b, _ := json.Marshal(r)
		if !r.Complete || r.Accepted || r.Clock != "unverified_exact_decimal_only" {
			t.Fatalf("unexpected state: %s", r.Failure)
		}
		for _, bad := range []string{"secret-runner", "secret_symbol", "secret-payload", `C:\\private`, "0x1010", "0x1000"} {
			if strings.Contains(string(b), bad) {
				t.Fatalf("private value escaped: %q", bad)
			}
		}
		sample := rows(r, "SampledProfile")
		if len(sample) != 1 {
			t.Fatal("missing sampled row")
		}
		frame, ok := pointer(r, sample[0], "PrgrmCtr").(map[string]any)
		if !ok || frame["offset"] != "0x10" || r.MappedFrames != 2 {
			t.Fatal("missing candidate module offset")
		}
		if len(r.CandidateImages) != 1 || r.CandidateImages[0].Module != "ntdll.dll" {
			t.Fatal("missing candidate image")
		}
		if len(rows(r, "CSwitch")) != 1 || len(rows(r, "ReadyThread")) != 1 {
			t.Fatal("target scheduling identifiers were omitted")
		}
	})
	t.Run("parent_only_match_is_not_selected", func(t *testing.T) {
		r := readXperfEvidence(t.Context(), write(t, header+"P-Start,1,private-child.exe (300),100\n", nil))
		if len(rows(r, "P-Start")) != 0 {
			t.Fatal("parent selected child")
		}
	})
	t.Run("unowned_overlap_has_no_scoped_pointer", func(t *testing.T) {
		text := header + "T-Start,1,unowned.exe (300),7\n" + data
		r := readXperfEvidence(t.Context(), write(t, text, nil))
		sample := rows(r, "SampledProfile")
		if len(sample) != 1 || sample[0][2].(uint32) != 0 || pointer(r, sample[0], "PrgrmCtr") != nil {
			t.Fatal("ambiguous thread gained pointer scope")
		}
	})
	t.Run("payload_timestamp_does_not_create_header", func(t *testing.T) {
		r := readXperfEvidence(t.Context(), write(t, header+strings.Replace(strings.Replace(data, "secret-payload", "TimeStamp", 1), "secret_symbol", "PRIVATE_SENTINEL", 1), nil))
		encoded, _ := json.Marshal(r)
		if !r.Complete || len(rows(r, "SampledProfile")) != 1 || strings.Contains(string(encoded), "PRIVATE_SENTINEL") {
			t.Fatal("payload became a declaration")
		}
	})
	t.Run("missing_declared_header_keeps_counts", func(t *testing.T) {
		r := readXperfEvidence(t.Context(), write(t, "SampledProfile,1,private.exe (100),7,0x1234\n", nil))
		if r.Failure != "no_known_headers" || len(r.Rows) != 0 || r.UnknownHeaders != 1 {
			t.Fatal("unknown shape leaked values")
		}
	})
	t.Run("unknown_identity_column_keeps_counts", func(t *testing.T) {
		text := "BeginHeader\nSampledProfile,TimeStamp,UnknownIdentity,Address\nEndHeader\nSampledProfile,1,100,0x1234\n"
		r := readXperfEvidence(t.Context(), write(t, text, nil))
		if len(r.Rows) != 0 || len(r.Schemas) != 1 || r.Schemas[0].Rows != 1 || r.Schemas[0].Supported {
			t.Fatal("unknown identity gained interpretation")
		}
	})
	t.Run("duplicate_column_rejects_observations", func(t *testing.T) {
		text := "BeginHeader\nSampledProfile,TimeStamp,ThreadID,ThreadID\nEndHeader\nSampledProfile,1,7,7\n"
		r := readXperfEvidence(t.Context(), write(t, text, nil))
		if len(r.Rows) != 0 || r.Schemas[0].Failure != "duplicate_known_column" {
			t.Fatal("duplicate accepted")
		}
	})
	t.Run("hostile_export_hash_is_not_emitted", func(t *testing.T) {
		hostile := strings.Repeat("SECRET", 10) + "LEAK"
		r := readXperfEvidence(t.Context(), write(t, header+data, func(s map[string]any) { s["csv_sha256"] = hostile }))
		b, _ := json.Marshal(r)
		if r.Failure != "export_incomplete" || r.ExportHash != "" || strings.Contains(string(b), hostile) {
			t.Fatal("hostile hash emitted")
		}
	})
	t.Run("mismatched_export_hash_rejects_binding", func(t *testing.T) {
		r := readXperfEvidence(t.Context(), write(t, header+data, func(s map[string]any) { s["csv_sha256"] = strings.Repeat("0", 64) }))
		if r.Failure != "csv_changed" || r.Complete {
			t.Fatal("mismatch accepted")
		}
	})
	t.Run("unknown_help_never_accepts_attribution", func(t *testing.T) {
		r := readXperfEvidence(t.Context(), write(t, header+data, func(s map[string]any) { s["help_validation"] = "unavailable" }))
		if !r.Complete || r.Accepted || r.HelpState != "unavailable" {
			t.Fatal("help failure lost observations or accepted attribution")
		}
	})
	t.Run("unknown_enums_are_redacted", func(t *testing.T) {
		r := readXperfEvidence(t.Context(), write(t, header+strings.Replace(data, "DelayExecution", "PRIVATE_REASON", 1), nil))
		b, _ := json.Marshal(r)
		if len(rows(r, "CSwitch")) != 1 || r.RedactedCells != 1 || strings.Contains(string(b), "PRIVATE_REASON") {
			t.Fatal("unknown enum handling failed")
		}
	})
	t.Run("overlapping_images_have_no_mapping", func(t *testing.T) {
		text := header + "ImageLoad,1,100,0x1000,4096,C:\\private\\kernel32.dll\n" + data
		r := readXperfEvidence(t.Context(), write(t, text, nil))
		sample := rows(r, "SampledProfile")
		frame := pointer(r, sample[0], "PrgrmCtr").(map[string]any)
		if _, ok := frame["image"]; ok || r.AmbiguousFrames != 2 {
			t.Fatal("overlapping images mapped")
		}
	})
	t.Run("unloaded_image_does_not_map_later_sample", func(t *testing.T) {
		text := header + "ImageLoad,1,100,0x1000,4096,ntdll.dll\nImageUnload,2,100,0x1000,4096,ntdll.dll\nT-Start,1,pwsh.exe (100),7\nSampledProfile,3,pwsh.exe (100),7,0x1010,0,ntdll.dll!private,1,private\n"
		r := readXperfEvidence(t.Context(), write(t, text, nil))
		sample := rows(r, "SampledProfile")
		frame := pointer(r, sample[0], "PrgrmCtr").(map[string]any)
		if _, ok := frame["image"]; ok {
			t.Fatal("unloaded image mapped")
		}
	})
	t.Run("malicious_header_label_is_hashed", func(t *testing.T) {
		text := strings.Replace(header, "PrivateData", `C:\private\SECRET`, 1) + data
		r := readXperfEvidence(t.Context(), write(t, text, nil))
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "SECRET") || !strings.Contains(string(b), "rejected_sha256_") {
			t.Fatal("unsafe header exposed")
		}
	})
	t.Run("context_cancellation_is_bounded", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r := readXperfEvidence(ctx, write(t, header+data, nil))
		if r.Failure != "csv_deadline" {
			t.Fatal("cancellation ignored")
		}
	})
	t.Run("large_cell_is_bounded", func(t *testing.T) {
		text := header + "SampledProfile,1," + strings.Repeat("x", 200000) + ",7,0x1234,0,module,1,private\n"
		r := readXperfEvidence(t.Context(), write(t, text, nil))
		if r.Complete || r.Failure != "csv_syntax" {
			t.Fatal("record bound missing")
		}
	})
	t.Run("output_truncation_preserves_counts", func(t *testing.T) {
		text := header + "T-Start,1,pwsh.exe (100),7\n" + strings.Repeat("SampledProfile,2.125,pwsh.exe (100),7,0x1234,0,ntdll.dll!private,1,private\n", 40000)
		r := readXperfEvidence(t.Context(), write(t, text, nil))
		b, _ := json.Marshal(r)
		if !r.Complete || !r.Truncated || r.Omitted == 0 || len(r.Tail) != 64 || len(b) > xperfEvidenceLimit {
			t.Fatal("safe output bound failed")
		}
	})
	t.Run("padded_display_pid", func(t *testing.T) {
		r := readXperfEvidence(t.Context(), write(t, header+strings.ReplaceAll(data, "(100)", "( 100)"), nil))
		if !r.Complete || len(rows(r, "SampledProfile")) != 1 || r.MappedFrames != 2 {
			t.Fatal("padded PID lost evidence")
		}
	})
	t.Run("conflicting_thread_aliases_disable_pointer_scope", func(t *testing.T) {
		for _, text := range []string{
			"BeginHeader\nT-Start,TimeStamp,PID,ProcessID,ThreadID\nStack,TimeStamp,ThreadID,No.,Address\nEndHeader\nT-Start,1,100,300,7\nStack,2,7,0,0x1234\n",
			"BeginHeader\nT-Start,TimeStamp,PID,ThreadID,TID\nStack,TimeStamp,ThreadID,No.,Address\nEndHeader\nT-Start,1,100,7,8\nStack,2,7,0,0x1234\n",
		} {
			r := readXperfEvidence(t.Context(), write(t, text, nil))
			stack := rows(r, "Stack")
			if len(stack) != 1 || stack[0][2].(uint32) != 0 || pointer(r, stack[0], "Address") != nil {
				t.Fatal("conflicting alias gained scope")
			}
		}
	})
	t.Run("malformed_header_sections_reject", func(t *testing.T) {
		for _, text := range []string{"BeginHeader\nBeginHeader\n", "EndHeader\n", header + "BeginHeader\n"} {
			r := readXperfEvidence(t.Context(), write(t, text, nil))
			if r.Complete || r.Failure != "header_section_order" {
				t.Fatal("malformed sections accepted")
			}
		}
	})

}
