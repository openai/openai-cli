//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Temporary experiment. Lexical file order puts this before the original roots.
func TestMainDispatchColdStartupProfile(t *testing.T) {
	directory := t.TempDir()
	helper, err := os.Executable()
	startupProfileCheck(t, "locate_test_binary", err)
	working, err := os.Getwd()
	startupProfileCheck(t, "locate_working_directory", err)
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "windows-startup-collector.ps1"))
	startupProfileCheck(t, "locate_collector", err)
	bindings := map[string]any{"go_version": runtime.Version(), "architecture": runtime.GOARCH, "parent_pid": os.Getpid()}
	for label, path := range map[string]string{"test_binary": helper, "collector": script} {
		file, err := os.Open(path)
		startupProfileCheck(t, "open_binding_"+label, err)
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		startupProfileCheck(t, "hash_binding_"+label, errors.Join(copyErr, file.Close()))
		bindings[label+"_sha256"] = fmt.Sprintf("%x", hash.Sum(nil))
	}
	startupProfileSave(t, filepath.Join(directory, "source-bindings.json"), bindings)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, helper, "-test.run=^TestWindowsStartupProfilerProcess$", "-test.timeout=5m50s", "--", "owner", directory, script, helper, working)
	command.WaitDelay = 5 * time.Second
	owner := startupProfileStart(t, directory, "owner", command)
	err = owner.wait()
	if output, readErr := startupProfileRead(filepath.Join(directory, "owner.stdout"), 4<<20); readErr == nil {
		pattern := regexp.MustCompile(`(?m)^[ \t]+aa_startup_profiler_windows_test\.go:[0-9]+: (startup-profile (?:failure phase=[a-z0-9_-]{1,64} error_type=[A-Za-z0-9*._]+ win32=[0-9]+|phase=[a-z0-9_-]{1,64}|[a-z_]+))\r?$`)
		var diagnostics []string
		for _, match := range pattern.FindAllSubmatch(output, 40) {
			diagnostics = append(diagnostics, string(match[1]))
			t.Logf("%s", match[1])
		}
		startupProfileSave(t, filepath.Join(directory, "owner-diagnostics.json"), map[string]any{"records": diagnostics, "exit": owner.exitCode(), "context": startupProfileContext(ctx)})
	}
	// Publish only fixed artifacts. Raw collector streams and ETL stay private.
	for _, name := range []string{"source-bindings.json", "owner-diagnostics.json", "profile-summary.json", "nested-tests.stdout", "heartbeat.jsonl", "trace-status.json", "startup-system-summary.json"} {
		data, readErr := startupProfileRead(filepath.Join(directory, name), 4<<20)
		if os.IsNotExist(readErr) {
			t.Logf("startup-profile missing_artifact=%s", name)
			continue
		}
		startupProfileCheck(t, "read_artifact_"+name, readErr)
		sum := sha256.Sum256(data)
		parts := (len(data) + 5999) / 6000
		for offset, part := 0, 1; offset < len(data); offset, part = offset+6000, part+1 {
			block := data[offset:min(offset+6000, len(data))]
			t.Logf("startup-profile artifact=%s part=%d total=%d bytes=%d sha256=%x data=%s", name, part, parts, len(data), sum, base64.StdEncoding.EncodeToString(block))
		}
	}
	if err != nil || ctx.Err() != nil || owner.stdout.overflow || owner.stderr.overflow {
		t.Fatalf("startup-profile owner_failed exit=%d context=%v output_overflow=%t error_type=%T", owner.exitCode(), ctx.Err(), owner.stdout.overflow || owner.stderr.overflow, err)
	}
}

func TestWindowsStartupProfilerProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) == 1 && args[0] == "identity" {
		t.Logf("startup-profile-test-owner pid=%d utc=%s", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
		return
	}
	if len(args) == 2 && args[0] == "heartbeat" {
		startupProfileHeartbeat(t, args[1])
		return
	}
	if len(args) != 5 || args[0] != "owner" {
		t.Fatal("startup-profile invalid_helper_arguments")
	}
	directory, script, helper, working := args[1], args[2], args[3], args[4]
	t.Log("startup-profile phase=own_job")
	ownWindowsTestProcessTree(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute+30*time.Second)
	defer cancel()
	heartbeatCommand := exec.CommandContext(ctx, helper, "-test.run=^TestWindowsStartupProfilerProcess$", "--", "heartbeat", directory)
	heartbeatCommand.Env = append(os.Environ(), "GOMAXPROCS=1")
	heartbeatInput, err := heartbeatCommand.StdinPipe()
	startupProfileCheck(t, "create_heartbeat_lifeline", err)
	heartbeat := startupProfileStart(t, directory, "heartbeat", heartbeatCommand)
	defer func() {
		startupProfileCheck(t, "close_heartbeat_lifeline", heartbeatInput.Close())
		startupProfileCheck(t, "wait_heartbeat", heartbeat.wait())
	}()
	if startupProfileAwait(t, ctx, directory, "heartbeat-ready.json", "") != "ready" {
		t.Fatal("startup-profile heartbeat_readiness_deadline")
	}
	ready, err := startupProfileRead(filepath.Join(directory, "heartbeat-ready.json"), 4096)
	startupProfileCheck(t, "read_heartbeat_identity", err)
	var identity struct {
		PID int `json:"pid"`
	}
	startupProfileCheck(t, "parse_heartbeat_identity", json.Unmarshal(ready, &identity))
	if identity.PID != heartbeat.command.Process.Pid {
		t.Fatal("startup-profile heartbeat_identity_mismatch")
	}
	pwsh, err := exec.LookPath("pwsh.exe")
	startupProfileCheck(t, "locate_powershell7", err)
	// The fixed recorder interval completes even if an original test fails.
	recordContext, recordCancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer recordCancel()
	recordCommand := exec.CommandContext(recordContext, pwsh, "-NoProfile", "-NonInteractive", "-File", script, "-Mode", "Record", "-OutputDirectory", directory, "-Seconds", "90")
	recorder := startupProfileStart(t, directory, "recorder", recordCommand)
	defer func() { startupProfileCheck(t, "finish_recorder", recorder.wait()) }()
	traceReadiness := startupProfileAwait(t, ctx, directory, "trace-ready.json", "trace-status.json")
	nativeTemp := filepath.Join(directory, "native-temp")
	startupProfileCheck(t, "create_native_temp", os.Mkdir(nativeTemp, 0700))
	const selection = "^(TestWindowsStartupProfilerProcess|TestMainDispatchTokenizerConsoleRestoration|TestMainNativeShellFiniteJSONList)$"
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	command := exec.CommandContext(ctx, goBinary, "tool", "test2json", "-t", "-p", "openai-cli/startup-profile", helper, "-test.v=test2json", "-test.run="+selection, "-test.count=1", "-test.timeout=10m", "--", "identity")
	command.Dir = working
	command.Env = append(os.Environ(), "TEMP="+nativeTemp, "TMP="+nativeTemp, "GOTOOLCHAIN=local")
	started := time.Now().UTC()
	nested := startupProfileStart(t, directory, "nested-tests", command)
	nestedErr := nested.wait()
	finished := time.Now().UTC()
	data, err := startupProfileRead(filepath.Join(directory, "nested-tests.stdout"), 4<<20)
	startupProfileCheck(t, "read_nested_tests", err)
	processes, passes, parseErr := startupProfileIdentities(data)
	startupProfileSave(t, filepath.Join(directory, "owned-processes.json"), map[string]any{
		"test_start_utc": started.Format(time.RFC3339Nano), "test_end_utc": finished.Format(time.RFC3339Nano),
		"native_temp_root": nativeTemp, "helper_path": helper, "processes": processes,
		"pid_coverage": "observed_checkpoint_ids_only",
	})
	recordErr := recorder.wait()
	statusData, statusErr := startupProfileRead(filepath.Join(directory, "trace-status.json"), 1<<20)
	var traceStatus struct {
		State   string `json:"state"`
		Cleanup string `json:"recorder_cleanup"`
	}
	if statusErr == nil {
		statusErr = json.Unmarshal(statusData, &traceStatus)
	}
	reportCommand := exec.CommandContext(ctx, pwsh, "-NoProfile", "-NonInteractive", "-File", script, "-Mode", "Report", "-OutputDirectory", directory)
	report := startupProfileStart(t, directory, "report", reportCommand)
	reportErr := report.wait()
	summaryData, summaryErr := startupProfileRead(filepath.Join(directory, "startup-system-summary.json"), 1<<20)
	var systemSummary struct {
		ScanReport     string `json:"scan_report"`
		TraceWindow    string `json:"trace_window"`
		Truncated      bool   `json:"scan_report_truncated"`
		TimingComplete bool   `json:"scan_timing_complete"`
	}
	if summaryErr == nil {
		summaryErr = json.Unmarshal(summaryData, &systemSummary)
	}
	traceAccepted := statusErr == nil && summaryErr == nil && traceReadiness == "ready" && traceStatus.State == "recorded" && traceStatus.Cleanup == "confirmed" && systemSummary.ScanReport == "available" && systemSummary.TraceWindow == "covered" && !systemSummary.Truncated
	startupProfileSave(t, filepath.Join(directory, "profile-summary.json"), map[string]any{
		"owner_pid": os.Getpid(), "heartbeat_pid": heartbeat.command.Process.Pid, "recorder_pid": recorder.command.Process.Pid,
		"test_runner_pid": nested.command.Process.Pid, "test_start_utc": started.Format(time.RFC3339Nano), "test_end_utc": finished.Format(time.RFC3339Nano),
		"test_exit": nested.exitCode(), "recorder_exit": recorder.exitCode(), "report_exit": report.exitCode(), "root_passes": passes, "processes": processes,
		"nested_output_overflow": nested.stdout.overflow || nested.stderr.overflow,
		"recorder_state":         traceStatus.State, "recorder_cleanup": traceStatus.Cleanup,
		"trace_readiness": traceReadiness, "trace_coverage_schema_accepted": traceAccepted,
		"owned_scan_timing_available": systemSummary.TimingComplete, "absence_claim_supported": false,
		"context": startupProfileContext(ctx), "recorder_context": startupProfileContext(recordContext),
		"heartbeat_limit": "Independent scheduling gaps cannot identify every starvation cause.",
	})
	startupProfileCheck(t, "parse_nested_tests", parseErr)
	startupProfileCheck(t, "nested_tests", nestedErr)
	startupProfileCheck(t, "recorder", recordErr)
	startupProfileCheck(t, "report", reportErr)
	startupProfileCheck(t, "recorder_status", statusErr)
	startupProfileCheck(t, "report_summary", summaryErr)
	if traceStatus.Cleanup != "confirmed" && traceStatus.Cleanup != "not_started" {
		t.Fatal("startup-profile recorder_cleanup_unconfirmed")
	}
	if recorder.stdout.overflow || recorder.stderr.overflow || report.stdout.overflow || report.stderr.overflow {
		t.Fatal("startup-profile collector_output_limit")
	}
	if nested.stdout.overflow || nested.stderr.overflow {
		t.Fatal("startup-profile nested_output_limit")
	}
	required := []string{"TestWindowsStartupProfilerProcess", "TestMainDispatchTokenizerConsoleRestoration", "TestMainNativeShellFiniteJSONList"}
	for _, scenario := range []string{"cancel_repeat_cp437", "cancel_cp65001", "invalid_utf16_cp65001"} {
		required = append(required, "TestMainDispatchTokenizerConsoleRestoration/"+scenario)
	}
	for _, shell := range []string{"cmd", "powershell", "pwsh"} {
		root := "TestMainNativeShellFiniteJSONList/" + shell
		required = append(required, root)
		for _, name := range []string{"pipeline_json_multipage", "redirect_json_multipage", "redirect_json_empty", "pipeline_jsonl_multipage", "redirect_later_http_failure"} {
			required = append(required, root+"/"+name)
		}
	}
	for _, name := range required {
		if passes[name] != 1 {
			t.Fatal("startup-profile required_named_pass_missing")
		}
	}
}

func startupProfileHeartbeat(t *testing.T, directory string) {
	file, err := os.OpenFile(filepath.Join(directory, "heartbeat.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	startupProfileCheck(t, "open_heartbeat", err)
	defer func() { startupProfileCheck(t, "close_heartbeat", file.Close()) }()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(done) }()
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemTimes")
	startupProfileCheck(t, "find_system_times", proc.Find())
	started, previous := time.Now(), time.Now()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for sequence := 0; sequence < 3601; sequence++ {
		if sequence > 0 {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
		now := time.Now()
		var idle, kernel, user windows.Filetime
		ok, _, callErr := proc.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
		if ok == 0 {
			startupProfileCheck(t, "sample_system_times", callErr)
		}
		value := func(value windows.Filetime) uint64 { return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime) }
		record := map[string]any{"sequence": sequence, "pid": os.Getpid(), "utc": now.UTC().Format(time.RFC3339Nano), "elapsed_ns": now.Sub(started).Nanoseconds(), "gap_ns": now.Sub(previous).Nanoseconds(), "idle_100ns": value(idle), "kernel_100ns": value(kernel), "user_100ns": value(user)}
		startupProfileCheck(t, "write_heartbeat", json.NewEncoder(file).Encode(record))
		previous = now
		if sequence == 0 {
			startupProfileSave(t, filepath.Join(directory, "heartbeat-ready.json"), map[string]any{"pid": os.Getpid(), "utc": now.UTC().Format(time.RFC3339Nano)})
		}
	}
	t.Fatal("startup-profile heartbeat_record_limit")
}

type startupProfileCapture struct {
	file     *os.File
	written  int
	overflow bool
}

func (capture *startupProfileCapture) Write(data []byte) (int, error) {
	size := len(data)
	left := (4 << 20) - capture.written
	if size > left {
		capture.overflow = true
		data = data[:left]
	}
	n, err := capture.file.Write(data)
	capture.written += n
	if err != nil {
		return n, err
	}
	return size, nil
}

type startupProfileProcess struct {
	command        *exec.Cmd
	stdout, stderr *startupProfileCapture
	done           chan error
	waited         bool
	err            error
}

func startupProfileStart(t *testing.T, directory, label string, command *exec.Cmd) *startupProfileProcess {
	t.Helper()
	result := &startupProfileProcess{command: command}
	for _, stream := range []struct {
		name string
		out  **startupProfileCapture
		dest *io.Writer
	}{{"stdout", &result.stdout, &command.Stdout}, {"stderr", &result.stderr, &command.Stderr}} {
		file, err := os.OpenFile(filepath.Join(directory, label+"."+stream.name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		startupProfileCheck(t, "open_"+label+"_"+stream.name, err)
		*stream.out = &startupProfileCapture{file: file}
		*stream.dest = *stream.out
		t.Cleanup(func() { startupProfileCheck(t, "close_"+label+"_"+stream.name, file.Close()) })
	}
	command.WaitDelay = 5 * time.Second
	startupProfileCheck(t, "start_"+label, command.Start())
	result.done = make(chan error, 1)
	go func() { result.done <- command.Wait() }()
	t.Cleanup(func() {
		if !result.waited {
			_ = command.Process.Kill()
			_ = result.wait()
		}
	})
	return result
}

func (process *startupProfileProcess) wait() error {
	if !process.waited {
		process.err = <-process.done
		process.waited = true
	}
	return process.err
}

func startupProfileAwait(t *testing.T, parent context.Context, directory, ready, unavailable string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, name := range []string{ready, unavailable} {
			if name == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
				if name == unavailable {
					return "unavailable"
				}
				return "ready"
			} else if !os.IsNotExist(err) {
				startupProfileCheck(t, "observe_readiness", err)
			}
		}
		select {
		case <-ctx.Done():
			return "deadline"
		case <-ticker.C:
		}
	}
}

func (process *startupProfileProcess) exitCode() int {
	if process.command.ProcessState == nil {
		return -1
	}
	return process.command.ProcessState.ExitCode()
}

func startupProfileContext(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	return "active"
}

func startupProfileRead(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if int64(len(data)) > limit {
		return nil, errors.New("artifact exceeds its bound")
	}
	return data, errors.Join(readErr, closeErr)
}

func startupProfileSave(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	startupProfileCheck(t, "encode_owned_record", err)
	if len(data) > 1<<20 {
		t.Fatal("startup-profile owned_record_limit")
	}
	startupProfileCheck(t, "write_owned_record", os.WriteFile(path+".tmp", data, 0600))
	startupProfileCheck(t, "publish_owned_record", os.Rename(path+".tmp", path))
}

func startupProfileCheck(t *testing.T, phase string, err error) {
	t.Helper()
	if err != nil {
		var code syscall.Errno
		_ = errors.As(err, &code)
		t.Fatalf("startup-profile failure phase=%s error_type=%T win32=%d", phase, err, uintptr(code))
	}
}

type startupProfileIdentity struct {
	PID  int    `json:"pid"`
	Role string `json:"role"`
}

func startupProfileIdentities(data []byte) ([]startupProfileIdentity, map[string]int, error) {
	patterns := map[string]*regexp.Regexp{
		"test_owner":         regexp.MustCompile(`startup-profile-test-owner pid=([0-9]+)`),
		"cli":                regexp.MustCompile(`"cli_pid":([0-9]+)`),
		"shell_supervisor":   regexp.MustCompile(`supervisor_pid=([0-9]+)`),
		"native_producer":    regexp.MustCompile(`value="pid=([0-9]+) utc=`),
		"windows_powershell": regexp.MustCompile(`shell_pid=([0-9]+)`),
	}
	seen, passes := map[startupProfileIdentity]bool{}, map[string]int{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct{ Action, Test, Output string }
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, passes, err
		}
		if event.Action == "pass" {
			passes[event.Test]++
		}
		for role, pattern := range patterns {
			if role == "windows_powershell" && event.Test != "TestMainNativeShellFiniteJSONList/powershell" {
				continue
			}
			for _, match := range pattern.FindAllStringSubmatch(event.Output, -1) {
				pid, err := strconv.ParseUint(match[1], 10, 32)
				if err != nil || pid == 0 {
					continue
				}
				seen[startupProfileIdentity{PID: int(pid), Role: role}] = true
			}
		}
	}
	identities := make([]startupProfileIdentity, 0, len(seen))
	for identity := range seen {
		identities = append(identities, identity)
	}
	slices.SortFunc(identities, func(a, b startupProfileIdentity) int {
		if a.PID != b.PID {
			return a.PID - b.PID
		}
		return strings.Compare(a.Role, b.Role)
	})
	return identities, passes, scanner.Err()
}
