//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Temporary probes preserve the original cases first. Hashing warms file data;
// fresh paths cannot recreate original-host scheduling or executable scanning.
func probeTokenizerRestorationStartup(t *testing.T, binary string, run func(*testing.T, string, string)) {
	source, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	_, readErr := io.Copy(digest, source)
	closeErr := source.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("hash probe source: read=%v close=%v", readErr, closeErr)
	}
	for _, probe := range []struct {
		name      string
		scenarios []string
	}{
		{"probe_cp437_first", []string{"cancel_repeat_cp437", "cancel_cp65001"}},
		{"probe_cp65001_first", []string{"cancel_cp65001", "cancel_repeat_cp437"}},
		{"probe_output_first", []string{"probe_output_first_cp437"}},
	} {
		t.Run(probe.name, func(t *testing.T) {
			t.Setenv("OPENAI_CLI_NATIVE_SHELL_BINARY", binary)
			t.Setenv("OPENAI_CLI_NATIVE_SHELL_SHA256", fmt.Sprintf("%x", digest.Sum(nil)))
			copy := filepath.Join(t.TempDir(), "openai.exe")
			if copied, err := copyNativeShellCLI(copy); err != nil || !copied {
				t.Fatalf("copy probe CLI: copied=%t error=%v", copied, err)
			}
			adminConsoleLogExecutable(t, probe.name+" source", binary)
			adminConsoleLogExecutable(t, probe.name+" copy", copy)
			for _, scenario := range probe.scenarios {
				run(t, scenario, copy)
			}
		})
	}
}

var tokenizerRestorationCheckpointPath string // Set only inside the isolated console owner.

func tokenizerRestorationCheckpoint(t *testing.T, phase string, evidence tokenizerRestorationEvidence) {
	t.Helper()
	file, err := os.OpenFile(tokenizerRestorationCheckpointPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 30<<10 {
		t.Fatalf("checkpoint file exceeded its bound: %v", err)
	}
	err = json.NewEncoder(file).Encode(struct {
		Phase    string                       `json:"phase"`
		Time     int64                        `json:"unix_ns"`
		Evidence tokenizerRestorationEvidence `json:"evidence"`
	}{phase, time.Now().UnixNano(), evidence})
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("persist checkpoint: write=%v close=%v", err, closeErr)
	}
}

func logTokenizerRestorationCheckpoints(t *testing.T, path string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > 32<<10 {
		t.Fatalf("read bounded checkpoints: read=%v close=%v bytes=%d", readErr, closeErr, len(data))
	}
	if len(data) != 0 {
		t.Logf("durable tokenizer phases: %s", data)
	}
}

func preflightTokenizerRestorationOutput(t *testing.T, binary string, input, output *os.File, seed tokenizerRestorationState) {
	t.Helper()
	evidence := tokenizerRestorationEvidence{Before: tokenizerRestorationSnapshot(t, input, output)}
	if evidence.Before != seed {
		t.Fatalf("preflight seed differs: got %+v want %+v", evidence.Before, seed)
	}
	tokenizerRestorationCheckpoint(t, "preflight_before_start", evidence)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "tokenizer", "__output")
	command.Stdout, command.WaitDelay = output, 3*time.Second
	// Match internal/terminal.outputEnvironment exactly; inherit no CLI environment.
	command.Env = []string{fmt.Sprintf("GOMAXPROCS=%d", runtime.GOMAXPROCS(0))}
	if root := os.Getenv("SystemRoot"); root != "" {
		command.Env = append(command.Env, "SystemRoot="+root)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cleanupLocalUtilityPipe(t, stdin)
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	cleanupLocalUtilityPipe(t, stderr)
	starting := time.Now()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	evidence.StartElapsed, evidence.PID = time.Since(starting), command.Process.Pid
	readinessStarted, waited := time.Now(), false
	defer func() {
		cancel()
		if !waited {
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("terminated preflight did not exit before cleanup deadline")
			}
		}
	}()
	tokenizerRestorationCheckpoint(t, "preflight_started", evidence)
	var ready [8]byte
	if _, err := io.ReadFull(stderr, ready[:]); err != nil || string(ready[:]) != "TOKOUT\x01\x00" {
		t.Fatalf("preflight readiness failed: %v; deadline=%v; process_start=%s; frame=%x", err, ctx.Err(), evidence.StartElapsed, ready)
	}
	evidence.ReadyElapsed = time.Since(readinessStarted)
	tokenizerRestorationCheckpoint(t, "preflight_ready", evidence)
	exiting := time.Now()
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	tail, readErr := io.ReadAll(io.LimitReader(stderr, 1024))
	waitErr := command.Wait()
	waited = true
	evidence.ExitElapsed, evidence.Exit = time.Since(exiting), command.ProcessState.ExitCode()
	evidence.After = tokenizerRestorationSnapshot(t, input, output)
	tokenizerRestorationCheckpoint(t, "preflight_exited", evidence)
	if readErr != nil || len(tail) != 0 || waitErr != nil || ctx.Err() != nil || evidence.Exit != 0 || evidence.After != seed {
		t.Fatalf("preflight failed: read=%v tail=%x wait=%v deadline=%v evidence=%+v", readErr, tail, waitErr, ctx.Err(), evidence)
	}
}
