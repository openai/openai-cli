//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode"
	"unicode/utf16"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/windows"
)

const adminConsoleSyntheticKey = "sk-admin-SYNTHETIC-console-only"

type adminConsoleTestModes struct {
	Stdin  uint32 `json:"stdin"`
	Stdout uint32 `json:"stdout"`
	Stderr uint32 `json:"stderr"`
}

type adminConsoleTestFrame struct {
	Event, Phase, Nonce              string
	Scenario                         string
	Original, Active, Restored       adminConsoleTestModes
	Exit, Started, Completed, Phases int
	Passed                           bool
	ReaderType                       string          `json:"reader_type"`
	ActiveReads                      int             `json:"active_reads"`
	ReadBytes                        int             `json:"read_bytes"`
	LastReadBytes                    int             `json:"last_read_bytes"`
	LastReadError                    string          `json:"last_read_error"`
	FirstByteCategory                string          `json:"first_byte_category"`
	LastByteCategory                 string          `json:"last_byte_category"`
	ByteCategories                   map[string]int  `json:"byte_categories"`
	ReadErrorCategory                string          `json:"read_error_category"`
	KeyEmpty                         bool            `json:"key_empty"`
	RestoredModesMatch               bool            `json:"restored_modes_match"`
	ObserverHealthy                  bool            `json:"observer_healthy"`
	ControlValid                     bool            `json:"control_valid"`
	Predicates                       map[string]bool `json:"predicates"`
	CancelCalls                      int             `json:"cancel_calls"`
	CancelReturned                   *bool           `json:"cancel_returned"`
	CloseCalls                       int             `json:"close_calls"`
	CloseStartedReads                int             `json:"close_started_reads"`
	CloseCompletedReads              int             `json:"close_completed_reads"`
	CloseActiveReads                 int             `json:"close_active_reads"`
	ReadsCompleteAtClose             bool            `json:"reads_complete_at_close"`
	CloseSucceeded                   *bool           `json:"close_succeeded"`
	KeyMatches                       bool            `json:"key_matches"`
	ContextCanceled                  bool            `json:"context_canceled"`
	ReadsComplete                    bool            `json:"reads_complete"`
}

// Existing Windows CI selects TestMainDispatch. The outer job owns the worker,
// ConPTY hosts, and CLI children, including any blocked console teardown.
func TestMainDispatchAdminSetupConsole(t *testing.T) {
	if os.Getenv("OPENAI_ADMIN_CONSOLE_WORKER") == "1" {
		adminConsoleWorkerGate(t)
		// Compile inside the owned job so watchdog cleanup also owns Go children.
		buildContext, cancelBuild := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancelBuild()
		build := exec.CommandContext(buildContext, "go", "test", "-c", "-p", "2", "-o", os.Getenv("OPENAI_ADMIN_CONSOLE_OBSERVER"), "./pkg/custom")
		build.Dir = filepath.Clean(filepath.Join("..", ".."))
		build.Env = append(os.Environ(), "GOFLAGS=-p=2")
		build.WaitDelay = 5 * time.Second
		var output adminConsoleCapture
		build.Stdout, build.Stderr = &output, &output
		if err := build.Run(); err != nil {
			t.Fatalf("compile native observer: %v\n%s", err, output.checkedText(t))
		}
		output.checkedText(t)
		adminConsoleLogExecutable(t, "private reader observer test executable", os.Getenv("OPENAI_ADMIN_CONSOLE_OBSERVER"))
		adminConsoleRunCases(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	adminConsoleLogExecutable(t, "controller test executable", executable)
	directory := t.TempDir()
	observer := filepath.Join(directory, "custom-console.test.exe")
	listener, nonce := adminConsoleListen(t)
	defer listener.Close()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	worker := exec.CommandContext(ctx, executable, "-test.run=^TestMainDispatchAdminSetupConsole$", "-test.v")
	worker.Env = append(adminConsoleEnvironment(directory),
		"OPENAI_ADMIN_CONSOLE_WORKER=1", "OPENAI_ADMIN_CONSOLE_OBSERVER="+observer,
		"OPENAI_ADMIN_CONSOLE_GATE="+listener.Addr().String(), "OPENAI_ADMIN_CONSOLE_NONCE="+nonce)
	worker.Cancel = func() error { return windows.TerminateJobObject(job, 1) }
	worker.WaitDelay = 5 * time.Second
	var output adminConsoleCapture
	worker.Stdout, worker.Stderr = &output, &output
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(worker.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if err != nil {
		_ = worker.Process.Kill()
		_ = worker.Wait()
		t.Fatalf("own console worker job: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- worker.Wait() }()
	reaped := false
	defer func() {
		if !reaped {
			_ = windows.TerminateJobObject(job, 1)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("owned console worker could not be reaped")
			}
		}
	}()
	connection := adminConsoleAccept(t, listener, nonce)
	adminConsoleSend(t, connection, map[string]any{"action": "start", "nonce": nonce})
	connection.Close()
	err = <-done
	reaped = true
	workerOutput := output.checkedText(t)
	// Preserve sanitized partial evidence even when a watchdog makes the test fail.
	t.Log(workerOutput)
	if ctx.Err() != nil {
		t.Fatal("console worker watchdog terminated the owned job")
	}
	if err != nil {
		t.Fatalf("native console worker failed: %v", err)
	}
}

func adminConsoleWorkerGate(t *testing.T) {
	t.Helper()
	connection := adminConsoleDial(t, os.Getenv("OPENAI_ADMIN_CONSOLE_GATE"))
	defer connection.Close()
	nonce := os.Getenv("OPENAI_ADMIN_CONSOLE_NONCE")
	adminConsoleSend(t, connection, map[string]any{"event": "hello", "nonce": nonce})
	var start struct{ Action, Nonce string }
	if connection.decoder.Decode(&start) != nil || start.Action != "start" || start.Nonce != nonce {
		t.Fatal("console worker did not receive its job-ownership acknowledgement")
	}
}

func adminConsoleRunCases(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	adminConsoleLogExecutable(t, "actual main via TestMainDispatchProcess", executable)
	for _, test := range []struct {
		name, input    string
		exit, requests int
	}{
		{"success", adminConsoleSyntheticKey + "\r", 0, 1},
		{"ctrl_c", "\x03", 130, 0},
		{"incomplete_paste_ctrl_c", "\x1b[200~sk-admin-SYNTHETIC\x03", 130, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/organization/projects" || r.URL.RawQuery != "limit=1" || r.Header.Get("Authorization") != "Bearer "+adminConsoleSyntheticKey {
					t.Error("verification request changed its method, path, limit, or synthetic authorization")
				}
				w.Header().Set("Content-Type", "application/json")
				if _, err := io.WriteString(w, `{"object":"list","data":[],"has_more":false}`); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			session := adminConsoleStart(t, executable, "TestMainDispatchAdminSetupConsoleHost", []string{"OPENAI_BASE_URL=" + server.URL + "/v1"})
			hello := session.hello
			if hello.Original.Stdin&windows.ENABLE_ECHO_INPUT == 0 {
				t.Fatal("fresh console did not start with echo enabled")
			}
			session.send(t, map[string]any{"action": "start", "nonce": session.nonce})
			active := session.receive(t)
			if active.Event != "active" || !adminConsoleHiddenInput(active.Active.Stdin) {
				t.Fatal("CLI did not establish hidden console input")
			}
			session.waitPrompt(t)
			session.write(t, test.name, test.input)
			completed := session.receive(t)
			if completed.Event != "completed" || completed.Exit != test.exit || completed.Restored != hello.Original {
				t.Fatalf("CLI console lifecycle failed: exit=%d, expected=%d, modes_restored=%t", completed.Exit, test.exit, completed.Restored == hello.Original)
			}
			session.send(t, map[string]any{"action": "ack"})
			output := session.finish(t)
			if requests.Load() != int32(test.requests) {
				t.Errorf("verification requests=%d, want %d", requests.Load(), test.requests)
			}
			want := "Admin setup canceled."
			if test.exit == 0 {
				want = "Admin access verified."
			}
			if !strings.Contains(ansi.Strip(output), want) {
				t.Error("CLI did not display its expected result")
			}
			t.Logf("console handles verified; original=%+v active=%+v restored=%+v exit=%d requests=%d", hello.Original, active.Active, completed.Restored, completed.Exit, requests.Load())
		})
	}
	for _, scenario := range []struct {
		name, mode              string
		phases                  []string
		plainCancel, splitFinal bool
	}{
		{"native_reader_lifecycle", "original", []string{"success1", "cancel_partial", "success2"}, false, false},
		{"native_reader_success_only", "success_only", []string{"success1", "success2"}, false, false},
		{"native_reader_cancel_first", "cancel_first", []string{"cancel_partial", "success2"}, false, false},
		{"native_reader_cancel_plain", "cancel_plain", []string{"success1", "cancel_partial", "success2"}, true, false},
		{"native_reader_split_final", "split_final", []string{"success1", "cancel_partial", "success2"}, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var extra []string
			if scenario.mode != "original" {
				extra = []string{"OPENAI_ADMIN_CONSOLE_SCENARIO=" + scenario.mode}
			}
			session := adminConsoleStart(t, os.Getenv("OPENAI_ADMIN_CONSOLE_OBSERVER"), "TestAdminSetupConsoleObserverChild", extra)
			if session.hello.Scenario != scenario.mode {
				t.Fatal("observer selected a different diagnostic scenario")
			}
			if scenario.mode != "original" {
				if session.connection.SetDeadline(time.Now().Add(10*time.Second)) != nil {
					t.Fatal("could not bound the diagnostic observer scenario")
				}
			}
			session.send(t, map[string]any{"action": "start", "nonce": session.nonce})
			sent := map[string]bool{}
			partialPaste := "\x1b[200~sk-admin-SYNTHETIC"
			if scenario.plainCancel {
				partialPaste = "sk-admin-SYNTHETIC"
			}
			canceled, phases := false, 0
			finalEnterSent := false
			for {
				frame := session.receive(t)
				if frame.Event != "done" && (phases >= len(scenario.phases) || frame.Phase != scenario.phases[phases]) {
					t.Fatal("observer phase order differs from the diagnostic scenario")
				}
				switch frame.Event {
				case "ready":
					if !adminConsoleHiddenInput(frame.Active.Stdin) || frame.Original != session.hello.Original {
						t.Fatal("reader did not establish hidden input from restored modes")
					}
				case "read_started":
					t.Logf("phase=%s read_started=%d read_completed=%d active=%d bytes=%d", frame.Phase, frame.Started, frame.Completed, frame.ActiveReads, frame.ReadBytes)
					if !sent[frame.Phase] {
						session.waitPrompt(t)
						input := adminConsoleSyntheticKey + "\r"
						label := frame.Phase + "/combined"
						if frame.Phase == "success2" {
							input = "sk-admin-SYNTHETIC-console-next\r"
							if scenario.splitFinal {
								input = strings.TrimSuffix(input, "\r")
								label = frame.Phase + "/prefix"
							}
						}
						if frame.Phase == "cancel_partial" {
							input = partialPaste
						}
						session.write(t, label, input)
						sent[frame.Phase] = true
					} else if frame.Phase == "cancel_partial" && frame.ReadBytes == len(partialPaste) && !canceled {
						session.send(t, map[string]any{"action": "cancel", "phase": frame.Phase})
						canceled = true
					}
				case "read_completed":
					t.Logf("phase=%s read_started=%d read_completed=%d active=%d bytes=%d last_read_bytes=%d last_read_error=%s first_category=%s last_category=%s categories=%v", frame.Phase, frame.Started, frame.Completed, frame.ActiveReads, frame.ReadBytes, frame.LastReadBytes, frame.LastReadError, frame.FirstByteCategory, frame.LastByteCategory, frame.ByteCategories)
					if scenario.splitFinal && frame.Phase == "success2" && !finalEnterSent {
						if frame.ByteCategories["CR"] != 0 {
							t.Error("CR reached the final reader before the controller sent this phase's Enter")
						}
						if frame.ReadBytes == 31 && frame.ByteCategories["ascii-printable"] == 31 && frame.LastReadError == "nil" {
							t.Log("final printable prefix consumed; now sending the separate Enter")
							session.write(t, frame.Phase+"/enter", "\r")
							finalEnterSent = true
						}
					}
				case "cancel_result":
					if frame.CancelReturned == nil {
						t.Fatal("observer omitted the actual Cancel result")
					}
					t.Logf("phase=%s actual Cancel=%t", frame.Phase, *frame.CancelReturned)
				case "close_result":
					if frame.CloseSucceeded == nil {
						t.Fatal("observer omitted the actual Close result")
					}
					t.Logf("phase=%s Close calls=%d started=%d completed=%d active=%d reads_complete_at_close=%t close_succeeded=%t", frame.Phase, frame.CloseCalls, frame.CloseStartedReads, frame.CloseCompletedReads, frame.CloseActiveReads, frame.ReadsCompleteAtClose, *frame.CloseSucceeded)
				case "phase_complete":
					adminConsoleLogPhaseDiagnostics(t, frame)
					if scenario.splitFinal && frame.Phase == "success2" && !finalEnterSent {
						t.Error("final helper returned before the controller sent this phase's Enter")
					}
					if frame.CloseCalls != 1 || frame.CloseActiveReads != 0 || frame.CloseStartedReads != frame.CloseCompletedReads || !frame.ReadsCompleteAtClose || frame.CloseSucceeded == nil || !*frame.CloseSucceeded {
						t.Fatalf("native reader Close ordering failed: phase=%s calls=%d started=%d completed=%d active=%d", frame.Phase, frame.CloseCalls, frame.CloseStartedReads, frame.CloseCompletedReads, frame.CloseActiveReads)
					}
					if !frame.Passed || !frame.ReadsComplete || frame.Started != frame.Completed || frame.ActiveReads != 0 || frame.CancelCalls != 1 || frame.CancelReturned == nil || frame.Restored != session.hello.Original || frame.ReaderType != "*uv.conInputReader" {
						t.Fatalf("native reader lifecycle failed: phase=%s started=%d completed=%d active=%d", frame.Phase, frame.Started, frame.Completed, frame.ActiveReads)
					}
					if (frame.Phase == "cancel_partial") != frame.ContextCanceled || (frame.Phase != "cancel_partial") != frame.KeyMatches {
						t.Fatal("native reader returned the wrong phase result")
					}
					phases++
					t.Logf("phase=%s reader=%s started=%d completed=%d active=%d modes_restored=true", frame.Phase, frame.ReaderType, frame.Started, frame.Completed, frame.ActiveReads)
					if frame.Phase == "success2" {
						t.Log("success2 verifies later-key ownership in the surviving reader process; it is not a shell read")
					}
				case "done":
					if !frame.Passed || frame.Phases != len(scenario.phases) || phases != len(scenario.phases) || canceled != (scenario.mode != "success_only") {
						t.Fatal("native reader did not complete the diagnostic scenario's required phases")
					}
					session.send(t, map[string]any{"action": "ack"})
					session.finish(t)
					return
				default:
					t.Fatal("observer sent an unexpected control event")
				}
			}
		})
	}
}

func adminConsoleLogPhaseDiagnostics(t *testing.T, frame adminConsoleTestFrame) {
	t.Helper()
	// Select only observed predicates, counters, modes, and fixed categories.
	// Exclude the control nonce and never stringify input bytes or raw errors.
	diagnostic := map[string]any{
		"phase": frame.Phase, "passed": frame.Passed, "predicates": frame.Predicates,
		"read_error_category": frame.ReadErrorCategory, "key_empty": frame.KeyEmpty, "key_matches": frame.KeyMatches,
		"context_canceled": frame.ContextCanceled, "control_valid": frame.ControlValid, "observer_healthy": frame.ObserverHealthy,
		"original": frame.Original, "active": frame.Active, "restored": frame.Restored, "restored_modes_match": frame.RestoredModesMatch,
		"reader_type": frame.ReaderType, "started": frame.Started, "completed": frame.Completed, "active_reads": frame.ActiveReads,
		"read_bytes": frame.ReadBytes, "last_read_bytes": frame.LastReadBytes, "last_read_error": frame.LastReadError,
		"first_byte_category": frame.FirstByteCategory, "last_byte_category": frame.LastByteCategory, "byte_categories": frame.ByteCategories,
		"reads_complete": frame.ReadsComplete, "cancel_calls": frame.CancelCalls, "cancel_returned": frame.CancelReturned,
		"close_calls": frame.CloseCalls, "close_started_reads": frame.CloseStartedReads, "close_completed_reads": frame.CloseCompletedReads,
		"close_active_reads": frame.CloseActiveReads, "reads_complete_at_close": frame.ReadsCompleteAtClose, "close_succeeded": frame.CloseSucceeded,
	}
	encoded, err := json.Marshal(diagnostic)
	if err != nil {
		t.Fatal("could not encode sanitized phase diagnostics")
	}
	t.Logf("native phase diagnostics: %s", encoded)
}

// This process stays attached after the real main child exits, so restored modes
// come from live console handles instead of handles already closed at process exit.
func TestMainDispatchAdminSetupConsoleHost(t *testing.T) {
	if os.Getenv("OPENAI_ADMIN_CONSOLE_CONTROL") == "" {
		return
	}
	connection := adminConsoleDial(t, os.Getenv("OPENAI_ADMIN_CONSOLE_CONTROL"))
	defer connection.Close()
	original := adminConsoleGetTestModes(t)
	nonce := os.Getenv("OPENAI_ADMIN_CONSOLE_NONCE")
	adminConsoleSend(t, connection, map[string]any{"event": "hello", "nonce": nonce, "original": original})
	decoder := connection.decoder
	var control struct{ Action, Nonce string }
	if decoder.Decode(&control) != nil || control.Action != "start" || control.Nonce != nonce {
		t.Fatal("console host authentication failed")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestMainDispatchProcess$", "--", "openai", "setup", "admin")
	child.Env = append(os.Environ(), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal("console host could not start the real main child")
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	activeSent := false
	for {
		select {
		case err := <-done:
			if ctx.Err() != nil {
				t.Fatal("real main child required watchdog termination")
			}
			if !activeSent {
				t.Fatal("real main child exited before hidden input")
			}
			exit := 0
			if err != nil {
				var status *exec.ExitError
				if !errors.As(err, &status) {
					t.Fatal("real main child wait failed")
				}
				exit = status.ExitCode()
			}
			adminConsoleSend(t, connection, map[string]any{"event": "completed", "exit": exit, "restored": adminConsoleGetTestModes(t)})
			if decoder.Decode(&control) != nil || control.Action != "ack" {
				t.Fatal("console host did not receive teardown acknowledgement")
			}
			return
		case <-ticker.C:
			active := adminConsoleGetTestModes(t)
			if !activeSent && adminConsoleHiddenInput(active.Stdin) {
				adminConsoleSend(t, connection, map[string]any{"event": "active", "active": active})
				activeSent = true
			}
		}
	}
}

func adminConsoleGetTestModes(t *testing.T) adminConsoleTestModes {
	t.Helper()
	var modes adminConsoleTestModes
	for _, item := range []struct {
		file *os.File
		mode *uint32
	}{{os.Stdin, &modes.Stdin}, {os.Stdout, &modes.Stdout}, {os.Stderr, &modes.Stderr}} {
		if windows.GetConsoleMode(windows.Handle(item.file.Fd()), item.mode) != nil {
			adminConsoleLogHandleFailure(t)
			t.Fatal("observer standard handle is not a Windows console")
		}
	}
	return modes
}

func adminConsoleLogHandleFailure(t *testing.T) {
	t.Helper()
	for _, item := range []struct {
		name     string
		file     *os.File
		standard uint32
	}{{"stdin", os.Stdin, windows.STD_INPUT_HANDLE}, {"stdout", os.Stdout, windows.STD_OUTPUT_HANDLE}, {"stderr", os.Stderr, windows.STD_ERROR_HANDLE}} {
		handle := windows.Handle(item.file.Fd())
		fileType, typeErr := windows.GetFileType(handle)
		var mode uint32
		modeErr := windows.GetConsoleMode(handle, &mode)
		standard, standardErr := windows.GetStdHandle(item.standard)
		var standardMode uint32
		standardModeErr := windows.GetConsoleMode(standard, &standardMode)
		t.Logf("console handle=%s file_type=%d file_type_error=%d mode=%d mode_error=%d std_error=%d go_equals_std=%t std_null=%t std_invalid=%t std_mode=%d std_mode_error=%d", item.name, fileType, adminConsoleErrorCode(typeErr), mode, adminConsoleErrorCode(modeErr), adminConsoleErrorCode(standardErr), handle == standard, standard == 0, standard == windows.InvalidHandle, standardMode, adminConsoleErrorCode(standardModeErr))
	}
}

func adminConsoleErrorCode(err error) uint32 {
	if err == nil {
		return 0
	}
	var code syscall.Errno
	if errors.As(err, &code) {
		return uint32(code)
	}
	return ^uint32(0)
}

func adminConsoleHiddenInput(mode uint32) bool {
	return mode&(windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT) == 0 && mode&windows.ENABLE_VIRTUAL_TERMINAL_INPUT != 0
}

type adminConsoleCapture struct {
	sync.Mutex
	data     []byte
	overflow bool
}

func (capture *adminConsoleCapture) Write(data []byte) (int, error) {
	capture.Lock()
	defer capture.Unlock()
	if len(capture.data)+len(data) <= 1024*1024 {
		capture.data = append(capture.data, data...)
	} else {
		capture.overflow = true
	}
	return len(data), nil // Continue draining even when a faulty child floods output.
}
func (capture *adminConsoleCapture) text() string {
	capture.Lock()
	defer capture.Unlock()
	return string(capture.data)
}

type adminConsoleSession struct {
	console, process windows.Handle
	input, output    *os.File
	connection       *adminConsoleConnection
	capture          adminConsoleCapture
	drained          chan error
	hello            adminConsoleTestFrame
	nonce            string
	finished         bool
	prompts          int
	writes           int
	acceptedBytes    int
}

func adminConsoleStart(t *testing.T, executable, testName string, extra []string) *adminConsoleSession {
	t.Helper()
	listener, nonce := adminConsoleListen(t)
	defer listener.Close()
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inputRead.Close()
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		inputWrite.Close()
		t.Fatal(err)
	}
	defer outputWrite.Close()
	session := &adminConsoleSession{input: inputWrite, output: outputRead, nonce: nonce, drained: make(chan error, 1)}
	t.Cleanup(func() { session.finish(t) })
	go func() { _, err := io.Copy(&session.capture, outputRead); session.drained <- err }()
	if err := windows.CreatePseudoConsole(windows.Coord{X: 120, Y: 40}, windows.Handle(inputRead.Fd()), windows.Handle(outputWrite.Fd()), 0, &session.console); err != nil {
		t.Fatalf("create native ConPTY: %v", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatal(err)
	}
	defer attributes.Delete()
	// This attribute takes the opaque HPCON value as lpValue, not &HPCON.
	// Reinterpret its stored bits without a Go uintptr-to-pointer conversion.
	// https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session
	consoleValue := *(*unsafe.Pointer)(unsafe.Pointer(&session.console))
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, consoleValue, unsafe.Sizeof(session.console)); err != nil {
		t.Fatal(err)
	}
	startup := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))}, ProcThreadAttributeList: attributes.List()}
	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		t.Fatal(err)
	}
	command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{executable, "-test.run=^" + testName + "$", "-test.v"}))
	if err != nil {
		t.Fatal(err)
	}
	environment := append(adminConsoleEnvironment(t.TempDir()), extra...)
	environment = append(environment, "OPENAI_ADMIN_CONSOLE_CONTROL="+listener.Addr().String(), "OPENAI_ADMIN_CONSOLE_NONCE="+nonce)
	sort.Slice(environment, func(i, j int) bool { return strings.ToUpper(environment[i]) < strings.ToUpper(environment[j]) })
	block := utf16.Encode([]rune(strings.Join(environment, "\x00") + "\x00\x00"))
	var information windows.ProcessInformation
	launchErr := adminConsoleWithFreshStandardHandles(func() error {
		return windows.CreateProcess(application, command, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, &block[0], nil, &startup.StartupInfo, &information)
	})
	// A child can start successfully even if restoring the worker table fails.
	// Transfer its ownership before reporting either launch or restoration errors.
	session.process = information.Process
	if information.Thread != 0 {
		windows.CloseHandle(information.Thread)
	}
	if launchErr != nil {
		t.Fatalf("start native console child: %v", launchErr)
	}
	inputRead.Close()
	outputWrite.Close()
	session.connection = adminConsoleAccept(t, listener, nonce)
	// Accept consumes hello while preserving all of its sanitized evidence.
	session.hello = session.connection.hello
	return session
}

// This isolated worker launches console children sequentially. Leave its Go
// os.Std* objects untouched; only prevent redirected table entries reaching the
// child before Windows supplies the new ConPTY's console handles.
// https://learn.microsoft.com/en-us/windows/console/getstdhandle
func adminConsoleWithFreshStandardHandles(start func() error) (err error) {
	if os.Getenv("OPENAI_ADMIN_CONSOLE_WORKER") != "1" {
		return errors.New("console standard-handle setup requires the isolated worker")
	}
	ids := [...]uint32{windows.STD_INPUT_HANDLE, windows.STD_OUTPUT_HANDLE, windows.STD_ERROR_HANDLE}
	var original [3]windows.Handle
	for i, id := range ids {
		original[i], err = windows.GetStdHandle(id)
		if err != nil {
			return err
		}
	}
	changed := 0
	defer func() {
		var restoreErr error
		for i := 0; i < changed; i++ {
			restoreErr = errors.Join(restoreErr, windows.SetStdHandle(ids[i], original[i]))
		}
		if restoreErr != nil {
			err = errors.Join(err, errors.New("could not restore the worker standard-handle table"), restoreErr)
		}
	}()
	for _, id := range ids {
		if err = windows.SetStdHandle(id, 0); err != nil {
			return err
		}
		changed++
	}
	return start()
}

// Keep hello with its accepted connection without global mutable test state.
type adminConsoleConnection struct {
	net.Conn
	decoder *json.Decoder
	hello   adminConsoleTestFrame
}

func adminConsoleListen(t *testing.T) (*net.TCPListener, string) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		listener.Close()
		t.Fatal(err)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		listener.Close()
		t.Fatal(err)
	}
	return listener, hex.EncodeToString(nonce[:])
}
func adminConsoleAccept(t *testing.T, listener *net.TCPListener, nonce string) *adminConsoleConnection {
	t.Helper()
	connection, err := listener.Accept()
	if err != nil {
		t.Fatal("console child did not connect before the watchdog")
	}
	if err := connection.SetDeadline(time.Now().Add(40 * time.Second)); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	control := &adminConsoleConnection{Conn: connection, decoder: json.NewDecoder(io.LimitReader(connection, 128*1024))}
	if control.decoder.Decode(&control.hello) != nil || control.hello.Event != "hello" || control.hello.Nonce != nonce {
		connection.Close()
		t.Fatal("console control authentication failed")
	}
	return control
}
func adminConsoleDial(t *testing.T, address string) *adminConsoleConnection {
	t.Helper()
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		t.Fatal("console control address must be loopback")
	}
	connection, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal("console control connection failed")
	}
	if connection.SetDeadline(time.Now().Add(40*time.Second)) != nil {
		connection.Close()
		t.Fatal("console control deadline failed")
	}
	return &adminConsoleConnection{Conn: connection, decoder: json.NewDecoder(io.LimitReader(connection, 128*1024))}
}
func adminConsoleSend(t *testing.T, connection net.Conn, frame any) {
	t.Helper()
	if json.NewEncoder(connection).Encode(frame) != nil {
		t.Fatal("console control write failed")
	}
}
func (session *adminConsoleSession) send(t *testing.T, frame any) {
	adminConsoleSend(t, session.connection, frame)
}
func (session *adminConsoleSession) receive(t *testing.T) adminConsoleTestFrame {
	t.Helper()
	var frame adminConsoleTestFrame
	if session.connection.decoder.Decode(&frame) != nil {
		t.Fatal("console control ended before the expected result")
	}
	return frame
}
func (session *adminConsoleSession) write(t *testing.T, label, input string) {
	t.Helper()
	n, err := io.WriteString(session.input, input)
	session.writes++
	session.acceptedBytes += n
	// Pipe acceptance is not evidence of delivery to the application's reader.
	t.Logf("console pipe acceptance write=%d label=%s requested_bytes=%d accepted_bytes=%d total_accepted_bytes=%d error_present=%t", session.writes, label, len(input), n, session.acceptedBytes, err != nil)
	if err != nil || n != len(input) {
		t.Fatal("console input pipe did not accept the complete synthetic write")
	}
}
func (session *adminConsoleSession) waitPrompt(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(ansi.Strip(session.capture.text()), "Admin API key (hidden):") > session.prompts {
			session.prompts++
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("console never displayed its hidden-input prompt")
}

func (session *adminConsoleSession) finish(t *testing.T) string {
	t.Helper()
	if session.finished {
		return session.capture.checkedText(t)
	}
	session.finished = true
	if session.connection != nil {
		defer session.connection.Close()
	}
	if session.input != nil {
		defer session.input.Close()
	}
	if session.output != nil {
		defer session.output.Close()
	}
	if session.process != 0 {
		defer windows.CloseHandle(session.process)
		state, err := windows.WaitForSingleObject(session.process, 10000)
		if err != nil || state != windows.WAIT_OBJECT_0 {
			t.Error("console child required watchdog termination")
			_ = windows.TerminateProcess(session.process, 1)
			if state, err := windows.WaitForSingleObject(session.process, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
				t.Error("terminated console child could not be reaped")
			}
		} else {
			var exit uint32
			if windows.GetExitCodeProcess(session.process, &exit) != nil || exit != 0 {
				t.Error("console observer child did not exit successfully")
			}
		}
	}
	if session.console != 0 {
		closed := make(chan struct{})
		go func() { windows.ClosePseudoConsole(session.console); close(closed) }()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Error("ConPTY close exceeded its watchdog")
			session.output.Close()
		}
		// Keep draining through ClosePseudoConsole's final frame, including teardown.
		select {
		case err := <-session.drained:
			if err != nil {
				t.Error("ConPTY output drain failed")
			}
		case <-time.After(5 * time.Second):
			t.Error("ConPTY output did not close after teardown")
		}
	}
	return session.capture.checkedText(t)
}

func (capture *adminConsoleCapture) checkedText(t *testing.T) string {
	t.Helper()
	output := capture.text()
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, ansi.Strip(output))
	if strings.Contains(output, adminConsoleSyntheticKey) || strings.Contains(normalized, "sk-admin-SYNTHETIC") {
		t.Error("synthetic key bytes appeared in console output")
		return "" // Never copy a privacy failure into the enclosing test's logs.
	}
	capture.Lock()
	overflow := capture.overflow
	capture.Unlock()
	if overflow {
		t.Error("console output exceeded the capture budget; privacy evidence is incomplete")
		return ""
	}
	return output
}

func adminConsoleEnvironment(home string) []string {
	var environment []string
	for _, name := range []string{"SystemRoot", "SystemDrive", "WINDIR", "PATH", "TEMP", "TMP", "GOMODCACHE", "ImageOS", "ImageVersion"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	// Keep the existing Go caches while isolating all application home paths.
	goPath := os.Getenv("GOPATH")
	if goPath == "" {
		originalHome, _ := os.UserHomeDir()
		goPath = filepath.Join(originalHome, "go")
	}
	goCache := os.Getenv("GOCACHE")
	if goCache == "" {
		originalCache, _ := os.UserCacheDir()
		goCache = filepath.Join(originalCache, "go-build")
	}
	return append(environment, "GOPATH="+goPath, "GOCACHE="+goCache, "HOME="+home, "USERPROFILE="+home, "APPDATA="+filepath.Join(home, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"), "GOMAXPROCS=2", "TERM=xterm-256color", "NO_COLOR=1")
}

func adminConsoleLogExecutable(t *testing.T, role, path string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("could not open console executable for identity verification")
	}
	digest := sha256.New()
	_, readErr := io.Copy(digest, file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal("could not hash the complete console executable")
	}
	version := windows.RtlGetVersion()
	t.Logf("console identity role=%q sha256=%s runtime=%q platform=%s/%s windows=%d.%d.%d ImageOS=%q ImageVersion=%q",
		role, hex.EncodeToString(digest.Sum(nil)), runtime.Version(), runtime.GOOS, runtime.GOARCH,
		version.MajorVersion, version.MinorVersion, version.BuildNumber, os.Getenv("ImageOS"), os.Getenv("ImageVersion"))
}
