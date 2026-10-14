//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

type tokenizerConsoleTestFrame struct {
	Event, Scenario            string
	Passed                     bool
	Checks                     map[string]bool
	Original, Active, Restored adminConsoleTestModes
	InputSHA256                string `json:"input_sha256"`
	InputBytes                 int    `json:"input_bytes"`
	ModelExit                  int    `json:"model_exit"`
	FixtureWritten             bool   `json:"fixture_written"`
	OperationCompleted         bool   `json:"operation_completed"`
	DeadlineNotExceeded        bool   `json:"deadline_not_exceeded"`
	OriginalInputCP            uint32 `json:"original_input_cp"`
	OriginalOutputCP           uint32 `json:"original_output_cp"`
	RestoredInputCP            uint32 `json:"restored_input_cp"`
	RestoredOutputCP           uint32 `json:"restored_output_cp"`
	ActiveInputCP              uint32 `json:"active_input_cp"`
	ActiveOutputCP             uint32 `json:"active_output_cp"`
}

func TestMainDispatchTokenizerInputConsole(t *testing.T) {
	selection := os.Getenv("OPENAI_TOKENIZER_CONSOLE_CASES")
	if selection != "" && selection != "unicode" {
		t.Fatal("unknown tokenizer console case selection")
	}
	adminConsoleTestWorker(t, "TestMainDispatchTokenizerInputConsole", tokenizerConsoleRunCases,
		"OPENAI_TOKENIZER_CONSOLE_CASES="+selection)
}

func tokenizerConsoleRunCases(t *testing.T) {
	const paste = "漢字 👩‍💻 e\u0301 \ufffd"
	checks := []string{
		"fixture_written", "lifecycle_boundary_reached",
		"input_matches", "model_exit_matches", "operation_completed", "deadline_not_exceeded",
		"bridge_closed", "reader_joined", "close_succeeded", "repeated_close_succeeded",
		"close_bounded", "borrowed_input_open", "active_modes_unchanged", "active_codepages_unchanged",
		"restored_modes_match", "restored_codepages_match", "observer_healthy",
	}
	scenarios := []string{"unicode_cp437", "unicode_cp65001"}
	if os.Getenv("OPENAI_TOKENIZER_CONSOLE_CASES") == "" {
		scenarios = append(scenarios, "cancel_idle", "close_idle")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			want, exit, codePage := "", 0, uint32(437)
			if strings.HasPrefix(scenario, "unicode_") {
				want, exit = paste+"\ufffd", 130
			}
			if scenario == "unicode_cp65001" {
				codePage = 65001
			}
			session := adminConsoleStart(t, os.Getenv("OPENAI_ADMIN_CONSOLE_OBSERVER"), "TestTokenizerInputConsoleChild",
				[]string{"OPENAI_TOKENIZER_CONSOLE_SCENARIO=" + scenario})
			if session.hello.Scenario != "tokenizer/"+scenario {
				t.Fatal("tokenizer child selected an unexpected scenario")
			}
			session.send(t, map[string]any{"action": "start", "nonce": session.nonce})
			var ready, model, complete tokenizerConsoleTestFrame
			if session.connection.decoder.Decode(&ready) != nil || ready.Event != "ready" || ready.Scenario != session.hello.Scenario {
				t.Fatal("tokenizer child did not report its native input boundary")
			}
			if !adminConsoleHiddenInput(ready.Active.Stdin) || ready.ActiveInputCP != codePage {
				t.Fatal("tokenizer fixture did not establish its intended console input")
			}
			if session.connection.decoder.Decode(&model) != nil || model.Event != "model_complete" || model.Scenario != session.hello.Scenario {
				t.Fatal("tokenizer child did not report its model result")
			}
			modelEvidence, err := json.Marshal(model)
			if err != nil {
				t.Fatal("could not encode tokenizer model evidence")
			}
			// Retain text evidence if an older input source blocks during cleanup.
			t.Logf("native tokenizer model evidence: %s", modelEvidence)
			if !model.FixtureWritten || !model.OperationCompleted || !model.DeadlineNotExceeded {
				t.Error("tokenizer model did not complete the native fixture")
			}
			if session.connection.decoder.Decode(&complete) != nil || complete.Event != "tokenizer_complete" {
				t.Fatal("tokenizer child did not complete its native input case")
			}
			// Acknowledge before assertions so failed baseline cases can exit normally.
			session.send(t, map[string]any{"action": "ack"})
			encoded, err := json.Marshal(complete)
			if err != nil {
				t.Fatal("could not encode tokenizer console evidence")
			}
			t.Logf("native tokenizer evidence: %s", encoded)
			if len(complete.Checks) != len(checks) {
				t.Error("tokenizer child omitted required lifecycle predicates")
			}
			for _, name := range checks {
				if !complete.Checks[name] {
					t.Errorf("native tokenizer contract failed: %s", name)
				}
			}
			digest := sha256.Sum256([]byte(want))
			if model.InputSHA256 != complete.InputSHA256 || model.InputBytes != complete.InputBytes || model.ModelExit != complete.ModelExit {
				t.Error("tokenizer model result changed during cleanup")
			}
			if complete.InputSHA256 != fmt.Sprintf("%x", digest) || complete.InputBytes != len(want) {
				t.Errorf("native tokenizer text differs: bytes=%d, want=%d", complete.InputBytes, len(want))
			}
			if complete.ModelExit != exit || complete.Scenario != session.hello.Scenario || !complete.Passed {
				t.Error("tokenizer child did not preserve its expected result")
			}
			if complete.Original != session.hello.Original || complete.Restored != complete.Original || complete.ActiveInputCP != codePage || complete.ActiveOutputCP != ready.ActiveOutputCP {
				t.Error("tokenizer console mode or fixture identity changed")
			}
			if complete.OriginalInputCP == 0 || complete.OriginalOutputCP == 0 || complete.RestoredInputCP != complete.OriginalInputCP || complete.RestoredOutputCP != complete.OriginalOutputCP {
				t.Error("tokenizer console code pages did not restore")
			}
			session.finish(t)
		})
	}
}
