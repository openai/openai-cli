//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

const consoleDeadlineDirectory = "OPENAI_CLI_TEST_CONSOLE_DEADLINE_DIRECTORY"
const consoleDeadlineMode = "OPENAI_CLI_TEST_CONSOLE_DEADLINE_MODE"
const consoleDeadlineBinary = "OPENAI_CLI_TEST_CONSOLE_DEADLINE_BINARY"

// Invoke explicitly: test.exe -test.run=^TestConsoleDeadlineExperiment$ -- variant production.exe evidence-directory.
func TestConsoleDeadlineExperiment(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	if len(os.Args[separator+1:]) != 3 {
		t.Fatal("console experiment requires variant, production binary, and evidence directory")
	}
	ownWindowsTestProcessTree(t)
	args := os.Args[separator+1:]
	variant, binary, output := args[0], args[1], args[2]
	if variant != "baseline" && variant != "candidate" {
		t.Fatal("invalid console experiment variant")
	}
	consoleDeadlineCheck(t, "create_evidence_directory", os.Mkdir(output, 0700))
	helper, err := os.Executable()
	consoleDeadlineCheck(t, "locate_helper", err)
	binding := map[string]any{"variant": variant, "architecture": runtime.GOARCH, "go_version": runtime.Version(), "production_sha256": consoleDeadlineHash(t, binary), "test_sha256": consoleDeadlineHash(t, helper)}
	data, err := json.Marshal(binding)
	consoleDeadlineCheck(t, "encode_binding", err)
	consoleDeadlineCheck(t, "write_binding", os.WriteFile(filepath.Join(output, "binding.json"), data, 0600))
	for _, mode := range []string{"delayed", "never_release", "never_ready", "descendant", "ordinary"} {
		t.Run(mode, func(t *testing.T) {
			directory := filepath.Join(output, mode)
			consoleDeadlineCheck(t, "create_control_directory", os.Mkdir(directory, 0700))
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, helper, "-test.v=test2json", "-test.run=^TestMainDispatchTokenizerConsoleRestoration$", "-test.count=1")
			command.Env = append(os.Environ(), consoleDeadlineDirectory+"="+directory, consoleDeadlineMode+"="+mode, consoleDeadlineBinary+"="+binary, "GOTOOLCHAIN=local")
			command.WaitDelay = 3 * time.Second
			var stdout, stderr adminConsoleCapture
			command.Stdout, command.Stderr = &stdout, &stderr
			consoleDeadlineCheck(t, "start_original_root", command.Start())
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			waited := false
			handles := map[string]windows.Handle{}
			defer func() {
				if !waited {
					_ = command.Process.Kill()
					<-done
				}
				for _, handle := range handles {
					if state, _ := windows.WaitForSingleObject(handle, 0); state != windows.WAIT_OBJECT_0 {
						_ = windows.TerminateProcess(handle, 125)
						_, _ = windows.WaitForSingleObject(handle, 2000)
					}
					_ = windows.CloseHandle(handle)
				}
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			var release <-chan time.Time
			var timer *time.Timer
			defer func() {
				if timer != nil {
					timer.Stop()
				}
			}()
			holdComplete, released := false, false
			var gateObserved time.Time
			var runErr error
		monitor:
			for {
				for _, role := range []string{"owner", "child", "descendant"} {
					if handles[role] == 0 && consoleDeadlineExists(t, directory, role) {
						handles[role] = consoleDeadlineAnchor(t, directory, role)
					}
				}
				if mode == "delayed" && timer == nil && consoleDeadlineExists(t, directory, "gate_entered") {
					gateObserved = time.Now()
					timer = time.NewTimer(5100 * time.Millisecond)
					release = timer.C
				}
				if holdComplete && !released && (variant == "candidate" || consoleDeadlineExists(t, directory, "deadline_observed")) {
					consoleDeadlineWrite(t, directory, "release", map[string]any{"controller_held_ns": time.Since(gateObserved).Nanoseconds()})
					released = true
				}
				select {
				case runErr = <-done:
					waited = true
					break monitor
				case <-release:
					holdComplete = true
					release = nil
				case <-ticker.C:
				}
			}
			out, diagnostic := stdout.checkedText(t), stderr.checkedText(t)
			convert := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go.exe"), "tool", "test2json", "-p", "console-deadline")
			convert.Stdin = strings.NewReader(out)
			var converted, conversionError adminConsoleCapture
			convert.Stdout, convert.Stderr = &converted, &conversionError
			consoleDeadlineCheck(t, "convert_original_events", convert.Run())
			if conversionError.checkedText(t) != "" {
				t.Fatal("event conversion produced diagnostics")
			}
			out = converted.checkedText(t)
			consoleDeadlineCheck(t, "save_original_events", os.WriteFile(filepath.Join(directory, "original-tests.jsonl"), []byte(out), 0600))
			consoleDeadlineCheck(t, "save_private_stderr", os.WriteFile(filepath.Join(directory, "private-stderr.txt"), []byte(diagnostic), 0600))
			if ctx.Err() != nil || diagnostic != "" {
				t.Fatalf("outer experiment failed: context=%v stderr_bytes=%d", ctx.Err(), len(diagnostic))
			}
			wantPass := mode == "ordinary" || mode == "delayed" && variant == "candidate"
			if (runErr == nil) != wantPass {
				t.Fatalf("unexpected original root outcome: want_pass=%t error_type=%T", wantPass, runErr)
			}
			consoleDeadlineEvents(t, out, wantPass)
			owner := consoleDeadlineRead(t, directory, "owner_finished")
			if owner["natural_eof"] != true || (owner["context"] == "deadline_exceeded") != (mode == "never_release") {
				t.Fatal("owner watchdog or natural EOF evidence mismatch")
			}
			if handles["owner"] == 0 {
				t.Fatal("owner lifetime was not anchored")
			}
			if mode == "delayed" {
				result := consoleDeadlineRead(t, directory, "start_returned")
				if !released || result["gate_hold_ns"].(float64) < float64(5*time.Second) {
					t.Fatal("controlled launch hold was not established")
				}
				if variant == "baseline" {
					if result["deadline_exceeded"] != true || result["pid"] != float64(0) || result["context"] != "deadline_exceeded" {
						t.Fatal("baseline did not reject the expired command context")
					}
				} else if result["context"] != "active" || result["deadline_exceeded"] != false || result["pid"].(float64) <= 0 {
					t.Fatal("candidate did not launch under an active lifetime")
				}
			}
			if mode == "never_release" {
				if !consoleDeadlineExists(t, directory, "gate_entered") || consoleDeadlineExists(t, directory, "start_returned") {
					t.Fatal("never-release control launched or missed its gate")
				}
			}
			if mode == "never_ready" || mode == "descendant" {
				if handles["child"] == 0 || mode == "descendant" && handles["descendant"] == 0 {
					t.Fatal("synthetic process identities were not anchored")
				}
				result := consoleDeadlineRead(t, directory, "start_returned")
				child := consoleDeadlineRead(t, directory, "child")
				if result["pid"] != child["pid"] {
					t.Fatal("synthetic process differs from the launched lifetime")
				}
				if !consoleDeadlineExists(t, directory, "cleanup_returned") || consoleDeadlineExists(t, directory, "input") {
					t.Fatal("never-ready control skipped cleanup or accepted readiness")
				}
				if !strings.Contains(out, "deadline=context deadline exceeded") && !strings.Contains(out, "tokenizer readiness deadline: context deadline exceeded") {
					t.Fatal("interaction deadline evidence is missing")
				}
			}
			for role, handle := range handles {
				state, err := windows.WaitForSingleObject(handle, 2000)
				if err != nil || state != windows.WAIT_OBJECT_0 {
					t.Fatalf("owned lifetime remained active: role=%s state=%d error_type=%T", role, state, err)
				}
			}
			consoleDeadlineWrite(t, directory, "accepted", map[string]any{"variant": variant, "mode": mode, "original_root_passed": wantPass, "anchored_lifetimes": len(handles), "natural_eof": true})
		})
	}
}

func consoleDeadlineStart(t *testing.T, ctx context.Context, command *exec.Cmd, seed tokenizerRestorationState, invocation int) error {
	if seed.InputCP != 437 || invocation != 1 {
		return command.Start()
	}
	mode := os.Getenv(consoleDeadlineMode)
	entered := time.Now()
	if mode == "delayed" || mode == "never_release" {
		consoleDeadlineSave(t, "gate_entered", map[string]any{"context": consoleDeadlineContext(ctx)})
		observed := false
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for !consoleDeadlineExists(t, os.Getenv(consoleDeadlineDirectory), "release") {
			if !observed && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				consoleDeadlineSave(t, "deadline_observed", map[string]any{"context": "deadline_exceeded"})
				observed = true
			}
			<-ticker.C
		}
		consoleDeadlineSave(t, "gate_released", map[string]any{"hold_ns": time.Since(entered).Nanoseconds(), "context": consoleDeadlineContext(ctx)})
	}
	if mode == "never_ready" || mode == "descendant" {
		helper, err := os.Executable()
		consoleDeadlineCheck(t, "locate_synthetic_helper", err)
		command.Path = helper
		command.Args = []string{helper, "-test.run=^TestConsoleDeadlineSyntheticProcess$", "--", mode}
	}
	before := consoleDeadlineContext(ctx)
	starting := time.Now()
	err := command.Start()
	startElapsed := time.Since(starting)
	pid := 0
	if command.Process != nil {
		pid = command.Process.Pid
	}
	consoleDeadlineSave(t, "start_returned", map[string]any{"context": before, "deadline_exceeded": errors.Is(err, context.DeadlineExceeded), "pid": pid, "start_ns": startElapsed.Nanoseconds(), "gate_hold_ns": starting.Sub(entered).Nanoseconds(), "error_type": fmt.Sprintf("%T", err)})
	return err
}

func TestConsoleDeadlineSyntheticProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	if len(os.Args[separator+1:]) != 1 {
		t.Fatal("invalid synthetic console arguments")
	}
	mode := os.Args[separator+1]
	for _, file := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		kind, err := windows.GetFileType(windows.Handle(file.Fd()))
		if err != nil || kind != windows.FILE_TYPE_CHAR || !term.IsTerminal(file.Fd()) {
			t.Fatal("synthetic process did not inherit console handles")
		}
	}
	role := "child"
	if mode == "descendant_child" {
		role = "descendant"
	}
	consoleDeadlineSaveIdentity(t, role, os.Getpid())
	if mode == "descendant" {
		child := exec.Command(os.Args[0], "-test.run=^TestConsoleDeadlineSyntheticProcess$", "--", "descendant_child")
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		consoleDeadlineCheck(t, "start_console_descendant", child.Start())
		_ = child.Wait()
		t.Fatal("console descendant exited before owner cleanup")
	}
	<-time.NewTimer(time.Hour).C
	t.Fatal("synthetic process exceeded containment")
}

func consoleDeadlineEnvironment() []string {
	return []string{consoleDeadlineDirectory + "=" + os.Getenv(consoleDeadlineDirectory), consoleDeadlineMode + "=" + os.Getenv(consoleDeadlineMode)}
}
func consoleDeadlineContext(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if ctx.Err() != nil {
		return "canceled"
	}
	return "active"
}
func consoleDeadlinePhase(t *testing.T, name string, seed tokenizerRestorationState, invocation int) {
	if seed.InputCP == 437 && invocation == 1 {
		consoleDeadlineSave(t, name, map[string]any{})
	}
}
func consoleDeadlineSave(t *testing.T, name string, value map[string]any) {
	consoleDeadlineWrite(t, os.Getenv(consoleDeadlineDirectory), name, value)
}
func consoleDeadlineWrite(t *testing.T, directory, name string, value map[string]any) {
	t.Helper()
	value["utc"], value["writer_pid"] = time.Now().UTC().Format(time.RFC3339Nano), os.Getpid()
	data, err := json.Marshal(value)
	consoleDeadlineCheck(t, "encode_record", err)
	path := filepath.Join(directory, name+".json")
	consoleDeadlineCheck(t, "write_record", os.WriteFile(path+".tmp", data, 0600))
	consoleDeadlineCheck(t, "publish_record", os.Rename(path+".tmp", path))
}
func consoleDeadlineExists(t *testing.T, directory, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(directory, name+".json"))
	if os.IsNotExist(err) {
		return false
	}
	consoleDeadlineCheck(t, "inspect_record", err)
	return true
}
func consoleDeadlineRead(t *testing.T, directory, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, name+".json"))
	consoleDeadlineCheck(t, "read_record", err)
	if len(data) > 16<<10 {
		t.Fatal("experiment record exceeded bound")
	}
	var result map[string]any
	consoleDeadlineCheck(t, "decode_record", json.Unmarshal(data, &result))
	return result
}
func consoleDeadlineSaveIdentity(t *testing.T, name string, pid int) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	consoleDeadlineCheck(t, "open_identity", err)
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	consoleDeadlineCheck(t, "read_identity", windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user))
	consoleDeadlineSave(t, name, map[string]any{"pid": pid, "creation": creation})
}
func consoleDeadlineAnchor(t *testing.T, directory, role string) windows.Handle {
	data, err := os.ReadFile(filepath.Join(directory, role+".json"))
	consoleDeadlineCheck(t, "read_identity_record", err)
	var identity struct {
		PID      uint32           `json:"pid"`
		Creation windows.Filetime `json:"creation"`
	}
	consoleDeadlineCheck(t, "decode_identity_record", json.Unmarshal(data, &identity))
	if identity.PID == 0 || identity.Creation == (windows.Filetime{}) {
		t.Fatal("incomplete lifetime identity")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, identity.PID)
	consoleDeadlineCheck(t, "anchor_lifetime", err)
	var creation, exit, kernel, user windows.Filetime
	err = windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user)
	if err != nil || creation != identity.Creation {
		windows.CloseHandle(handle)
		t.Fatal("lifetime identity changed")
	}
	if state, err := windows.WaitForSingleObject(handle, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		windows.CloseHandle(handle)
		t.Fatal("observed lifetime was not running when anchored")
	}
	return handle
}
func consoleDeadlineHash(t *testing.T, path string) string {
	file, err := os.Open(path)
	consoleDeadlineCheck(t, "open_binary", err)
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	consoleDeadlineCheck(t, "hash_binary", errors.Join(err, file.Close()))
	return fmt.Sprintf("%x", hash.Sum(nil))
}
func consoleDeadlineCopyProduction(t *testing.T, destination string) {
	source := os.Getenv(consoleDeadlineBinary)
	before := consoleDeadlineHash(t, source)
	input, err := os.Open(source)
	consoleDeadlineCheck(t, "open_production", err)
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		input.Close()
		consoleDeadlineCheck(t, "create_private_binary", err)
	}
	_, err = io.Copy(output, input)
	consoleDeadlineCheck(t, "copy_production", errors.Join(err, input.Close(), output.Close()))
	if consoleDeadlineHash(t, destination) != before {
		t.Fatal("production copy hash differs")
	}
	t.Logf("console experiment production_sha256=%s", before)
}
func consoleDeadlineCheck(t *testing.T, phase string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("console experiment phase=%s error_type=%T", phase, err)
	}
}

func consoleDeadlineEvents(t *testing.T, output string, success bool) {
	counts := map[string]map[string]int{}
	scanner := bufio.NewScanner(bytes.NewBufferString(output))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct{ Action, Test string }
		consoleDeadlineCheck(t, "parse_original_event", json.Unmarshal(scanner.Bytes(), &event))
		if counts[event.Test] == nil {
			counts[event.Test] = map[string]int{}
		}
		counts[event.Test][event.Action]++
	}
	consoleDeadlineCheck(t, "scan_original_events", scanner.Err())
	root := "TestMainDispatchTokenizerConsoleRestoration"
	for _, name := range []string{root, root + "/cancel_repeat_cp437", root + "/cancel_cp65001", root + "/invalid_utf16_cp65001"} {
		action := "pass"
		if !success && (name == root || name == root+"/cancel_repeat_cp437") {
			action = "fail"
		}
		if counts[name]["run"] != 1 || counts[name][action] != 1 || counts[name]["skip"] != 0 || counts[name]["pass"]+counts[name]["fail"] != 1 {
			t.Fatal("original named outcome mismatch")
		}
	}
}

type consoleDeadlineOwnerCapture struct {
	readers, writers []*os.File
	done             []chan error
	drained          []bool
}

func consoleDeadlineCaptureOwner(t *testing.T, command *exec.Cmd, stdout, stderr io.Writer) *consoleDeadlineOwnerCapture {
	capture := &consoleDeadlineOwnerCapture{}
	for _, stream := range []struct {
		output      io.Writer
		destination *io.Writer
	}{{stdout, &command.Stdout}, {stderr, &command.Stderr}} {
		reader, writer, err := os.Pipe()
		if err != nil {
			capture.close()
			consoleDeadlineCheck(t, "create_capture_pipe", err)
		}
		capture.readers, capture.writers = append(capture.readers, reader), append(capture.writers, writer)
		*stream.destination = writer
		done := make(chan error, 1)
		capture.done, capture.drained = append(capture.done, done), append(capture.drained, false)
		go func() { _, err := io.Copy(stream.output, reader); done <- err }()
	}
	return capture
}
func (capture *consoleDeadlineOwnerCapture) closeWriters(t *testing.T) {
	for _, writer := range capture.writers {
		consoleDeadlineCheck(t, "close_capture_writer", writer.Close())
	}
	capture.writers = nil
}
func (capture *consoleDeadlineOwnerCapture) drain(t *testing.T) {
	for index, done := range capture.done {
		select {
		case err := <-done:
			capture.drained[index] = true
			consoleDeadlineCheck(t, "natural_capture_eof", err)
		case <-time.After(3 * time.Second):
			t.Fatal("capture pipe did not reach natural EOF")
		}
	}
}
func (capture *consoleDeadlineOwnerCapture) close() {
	for _, writer := range capture.writers {
		_ = writer.Close()
	}
	for _, reader := range capture.readers {
		_ = reader.Close()
	}
	for index, done := range capture.done {
		if !capture.drained[index] {
			<-done
			capture.drained[index] = true
		}
	}
}
