//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Temporary instrumentation for the unchanged native PowerShell controls.
func init() {
	// The temporary recorder runs only inside the existing native GitHub jobs.
	if os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("RUNNER_OS") == "Windows" {
		finiteJSONListControlObserver = beginOwnedWaitTrace
	}
}

type ownedWaitTarget struct {
	Role    string `json:"role"`
	PID     int    `json:"pid"`
	Before  string `json:"before_start_utc"`
	After   string `json:"after_start_utc"`
	Entered string `json:"script_entered_utc"`
	Exit    string `json:"exit_utc"`
}

type ownedWaitControl struct {
	pipeline bool
	trace    string
}

func beginOwnedWaitTrace(t *testing.T, shell nativeShell) (func(bool, string), func()) {
	if shell.name != "powershell" {
		return nil, func() {}
	}
	if _, err := exec.LookPath("pwsh.exe"); err != nil {
		if slices.Contains(strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ","), "pwsh") {
			t.Errorf("required trace shell unavailable: error_type=%T", err)
		}
		t.Log("owned-wait trace unavailable: PowerShell 7 is not installed")
		return nil, func() {}
	}
	directory := t.TempDir()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("trace working directory: error_type=%T", err)
	}
	script := filepath.Join(working, "..", "..", "scripts", "owned-wait-collector.ps1")
	profile := filepath.Join(working, "..", "..", "scripts", "owned-startup.wprp")
	helper, err := os.Executable()
	if err != nil {
		t.Fatalf("trace helper identity: error_type=%T", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 270*time.Second)
	command := exec.CommandContext(ctx, helper, "-test.run=^TestOwnedWaitToolProcess$", "--", "Record", script, directory, profile)
	command.WaitDelay = 3 * time.Second
	var stdout, stderr adminConsoleCapture
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start trace owner: error_type=%T", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var controls []ownedWaitControl
	var once sync.Once
	recorderWaited := false
	var recorderError error
	finish := func() {
		once.Do(func() {
			defer cancel()
			targets := make([]ownedWaitTarget, 0, len(controls))
			for _, control := range controls {
				target, err := readOwnedWaitTarget(control)
				if err != nil {
					t.Errorf("trace target checkpoint invalid: error_type=%T", err)
				}
				targets = append(targets, target)
			}
			input, err := json.Marshal(map[string]any{"processes": targets})
			if err != nil {
				t.Errorf("encode trace targets: error_type=%T", err)
			}
			if err := os.WriteFile(filepath.Join(directory, "wait-trace-input.json"), input, 0600); err != nil {
				t.Errorf("write trace targets: error_type=%T", err)
			}
			if err := signalOwnedWaitStop(directory); err != nil {
				t.Errorf("signal trace completion: error_type=%T", err)
			}
			if !recorderWaited {
				recorderError = <-done
				recorderWaited = true
			}
			t.Logf("owned-wait recorder error_type=%T context=%v stdout_bytes=%d stderr_bytes=%d", recorderError, ctx.Err(), len(stdout.checkedText(t)), len(stderr.checkedText(t)))
			cleanupOK := runOwnedWaitTool(t, helper, "Cleanup", script, directory, profile, 60*time.Second)
			exportOK := false
			xperfOK := false
			if recorderError == nil && cleanupOK {
				exportOK = runOwnedWaitTool(t, helper, "Export", script, directory, profile, 150*time.Second)
				xperfOK = runOwnedWaitTool(t, helper, "ExportXperf", script, directory, profile, 120*time.Second)
			}
			reductionOK := false
			if exportOK {
				reductionOK = runOwnedWaitAnalysis(t, helper, directory, "TestOwnedWaitReductionProcess")
			}
			xperfEvidenceOK := runOwnedWaitAnalysis(t, helper, directory, "TestOwnedWaitXperfEvidenceProcess")
			for _, name := range []string{"wait-trace-ready.json", "wait-trace-request.json", "wait-trace-owner.json", "wait-trace-status.json", "wait-trace-input.json", "wait-cleanup-status.json", "wait-export-status.json", "wait-reduction.json", "wait-xperf-export-status.json", "wait-xperf-evidence.json"} {
				emitOwnedWaitArtifact(t, directory, name)
			}
			if recorderError != nil || !cleanupOK || !exportOK || !reductionOK || !xperfOK || !xperfEvidenceOK {
				t.Error("owned-wait collection did not complete; inspect safe status artifacts")
			}
			// A fatal metadata startup failure must not discard the completed trace.
			captureOwnedWaitIdentity(t, shell, directory, filepath.Join(working, "..", "..", "scripts", "owned-module-identity.ps1"))
			emitOwnedWaitArtifact(t, directory, "wait-module-identity.json")
		})
	}
	// This also handles failure before the caller installs its deferred completion.
	t.Cleanup(finish)
	timer := time.NewTimer(75 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := readOwnedWaitBounded(filepath.Join(directory, "wait-trace-ready.json"), 1<<20)
		if err == nil {
			var ready struct {
				Ready bool `json:"ready"`
			}
			if json.Unmarshal(data, &ready) != nil || !ready.Ready {
				cancel()
				finish()
				t.Fatal("owned-wait recorder did not establish readiness")
			}
			return func(pipeline bool, trace string) { controls = append(controls, ownedWaitControl{pipeline, trace}) }, finish
		}
		if !os.IsNotExist(err) {
			cancel()
			finish()
			t.Fatalf("read trace readiness: error_type=%T", err)
		}
		select {
		case recorderError = <-done:
			recorderWaited = true
			finish()
			t.Fatal("owned-wait recorder exited before readiness")
		case <-timer.C:
			cancel()
			finish()
			t.Fatal("owned-wait readiness deadline")
		case <-ticker.C:
		}
	}
}

func runOwnedWaitAnalysis(t *testing.T, helper, directory, testName string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, helper, "-test.run=^"+testName+"$", "--", directory)
	command.WaitDelay = 3 * time.Second
	var stdout, stderr adminConsoleCapture
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	t.Logf("owned-wait analysis=%s error_type=%T context=%v stdout_bytes=%d stderr_bytes=%d", testName, err, ctx.Err(), len(stdout.checkedText(t)), len(stderr.checkedText(t)))
	return err == nil
}

func runOwnedWaitTool(t *testing.T, helper, mode, script, directory, profile string, timeout time.Duration) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, helper, "-test.run=^TestOwnedWaitToolProcess$", "--", mode, script, directory, profile)
	command.WaitDelay = 3 * time.Second
	var stdout, stderr adminConsoleCapture
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	t.Logf("owned-wait mode=%s error_type=%T context=%v stdout_bytes=%d stderr_bytes=%d", mode, err, ctx.Err(), len(stdout.checkedText(t)), len(stderr.checkedText(t)))
	return err == nil
}

func TestOwnedWaitToolProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) != 4 || !slices.Contains([]string{"Record", "Export", "ExportXperf", "Cleanup"}, args[0]) {
		t.Fatal("invalid trace tool arguments")
	}
	ownWindowsTestProcessTree(t)
	shell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Fatalf("trace shell missing: error_type=%T", err)
	}
	command := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", args[1], "-Mode", args[0], "-OutputDirectory", args[2], "-ProfilePath", args[3])
	command.WaitDelay = 3 * time.Second
	var stdout, stderr adminConsoleCapture
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if stdout.checkedText(t) != "" || stderr.checkedText(t) != "" {
		t.Error("trace tool emitted unexpected private output")
	}
	if err != nil {
		t.Fatalf("trace tool failed: error_type=%T", err)
	}
}

func readOwnedWaitTarget(control ownedWaitControl) (ownedWaitTarget, error) {
	target := ownedWaitTarget{Role: "powershell_direct"}
	if control.pipeline {
		target.Role = "powershell_pipeline"
	}
	data, err := readOwnedWaitBounded(control.trace, 8192)
	if err != nil {
		return target, err
	}
	patterns := []struct {
		expression string
		field      *string
	}{
		{`(?m)^before-shell-start supervisor_pid=[0-9]+ utc=(\S+)$`, &target.Before},
		{`(?m)^after-shell-start shell_pid=[0-9]+ process_start_ns=[0-9]+ supervisor_pid=[0-9]+ utc=(\S+)$`, &target.After},
		{`(?m)^after-shell-exit supervisor_pid=[0-9]+ utc=(\S+)$`, &target.Exit},
	}
	for _, pattern := range patterns {
		matches := regexp.MustCompile(pattern.expression).FindAllSubmatch(data, -1)
		if len(matches) != 1 {
			return target, fmt.Errorf("missing or duplicate trace phase")
		}
		stamp, err := time.Parse(time.RFC3339Nano, string(matches[0][1]))
		if err != nil {
			return target, err
		}
		*pattern.field = stamp.UTC().Format(time.RFC3339Nano)
	}
	pid := regexp.MustCompile(`(?m)^after-shell-start shell_pid=([0-9]+) `).FindAllSubmatch(data, -1)
	if len(pid) != 1 {
		return target, fmt.Errorf("missing or duplicate process identity")
	}
	target.PID, err = strconv.Atoi(string(pid[0][1]))
	if err != nil || target.PID <= 0 {
		return target, fmt.Errorf("invalid process identity")
	}
	entered, err := readOwnedWaitBounded(control.trace+".entered", 256)
	if err != nil {
		return target, err
	}
	stamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(entered)))
	if err != nil {
		return target, err
	}
	target.Entered = stamp.UTC().Format(time.RFC3339Nano)
	return target, nil
}

func readOwnedWaitBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("trace artifact exceeds bound")
	}
	return data, nil
}

func emitOwnedWaitArtifact(t *testing.T, directory, name string) {
	t.Helper()
	data, err := readOwnedWaitBounded(filepath.Join(directory, name), 4<<20)
	if os.IsNotExist(err) {
		t.Errorf("owned-wait artifact=%s unavailable=true", name)
		return
	}
	if err != nil || !json.Valid(data) {
		t.Errorf("owned-wait artifact=%s invalid=true error_type=%T", name, err)
		return
	}
	hash := sha256.Sum256(data)
	const chunk = 6000
	total := (len(data) + chunk - 1) / chunk
	for index, start := 1, 0; start < len(data); index, start = index+1, start+chunk {
		t.Logf("owned-wait artifact=%s part=%d total=%d bytes=%d sha256=%x data=%s", name, index, total, len(data), hash, base64.StdEncoding.EncodeToString(data[start:min(start+chunk, len(data))]))
	}
}

func signalOwnedWaitStop(directory string) error {
	temporary := filepath.Join(directory, "wait-trace-stop.pending")
	if err := os.WriteFile(temporary, []byte("{}\n"), 0600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(directory, "wait-trace-stop.json"))
}
