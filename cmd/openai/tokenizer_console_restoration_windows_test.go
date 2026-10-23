//go:build windows

package main

import (
	"context"
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
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

type tokenizerRestorationState struct {
	InputMode  uint32 `json:"input_mode"`
	OutputMode uint32 `json:"output_mode"`
	InputCP    uint32 `json:"input_cp"`
	OutputCP   uint32 `json:"output_cp"`
}

type tokenizerRestorationEvidence struct {
	Invocation   int                       `json:"invocation"`
	PID          int                       `json:"cli_pid"`
	Exit         int                       `json:"cli_exit"`
	FreshFrame   bool                      `json:"fresh_frame"`
	StartElapsed time.Duration             `json:"process_start_ns"`
	ReadyElapsed time.Duration             `json:"readiness_ns"`
	ExitElapsed  time.Duration             `json:"exit_ns"`
	Before       tokenizerRestorationState `json:"before"`
	Active       tokenizerRestorationState `json:"active"`
	After        tokenizerRestorationState `json:"after"`
}

// Existing native CI selects TestMainDispatch on Windows x64 and ARM64.
// These are console-process checks; they do not inspect a terminal application.
func TestMainDispatchTokenizerConsoleRestoration(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai.exe")
	runTokenizerRestorationHelper(t, "build", binary)
	adminConsoleLogExecutable(t, "tokenizer restoration production CLI", binary)
	for _, scenario := range []string{"cancel_repeat_cp437", "cancel_cp65001", "invalid_utf16_cp65001"} {
		t.Run(scenario, func(t *testing.T) {
			output := runTokenizerRestorationHelper(t, scenario, binary)
			line, remainder, found := strings.Cut(output, "\n")
			var evidence []tokenizerRestorationEvidence
			if !found || json.Unmarshal([]byte(line), &evidence) != nil || strings.TrimSpace(remainder) != "PASS" {
				t.Fatalf("console owner omitted its complete evidence: %q", output)
			}
			wantCount, wantExit := 1, 130
			if scenario == "cancel_repeat_cp437" {
				wantCount = 2
			} else if scenario == "invalid_utf16_cp65001" {
				wantExit = 1
			}
			if len(evidence) != wantCount {
				t.Fatalf("observed %d invocations, want %d", len(evidence), wantCount)
			}
			for i, run := range evidence {
				if run.Invocation != i+1 || run.PID <= 0 || run.Exit != wantExit || !run.FreshFrame || run.Before != run.After {
					t.Errorf("production CLI restoration failed: %+v", run)
				}
			}
			t.Logf("native production restoration, shared stdout/stderr console buffer: %s", line)
		})
	}
}

func runTokenizerRestorationHelper(t *testing.T, scenario, binary string) string {
	t.Helper()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	timeout := 15 * time.Second
	if scenario == "build" {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, helper, "-test.run=^TestTokenizerConsoleRestorationProcess$", "--", scenario, binary)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if scenario != "build" {
		command.SysProcAttr.CreationFlags = windows.CREATE_NEW_CONSOLE
	}
	home := t.TempDir()
	command.Env = append(adminConsoleEnvironment(home), "TEMP="+home, "TMP="+home,
		"OPENAI_BASE_URL=http://127.0.0.1:1", "GOTOOLCHAIN=local")
	command.WaitDelay = 3 * time.Second
	var stdout, stderr adminConsoleCapture
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	out, diagnostic := stdout.checkedText(t), stderr.checkedText(t)
	if err != nil || ctx.Err() != nil || diagnostic != "" {
		t.Fatalf("console owner %s failed: %v; deadline=%v; stdout=%q stderr=%q", scenario, err, ctx.Err(), out, diagnostic)
	}
	return out
}

// This owner outlives the actual CLI, including main's os.Exit paths.
// Its original stdout remains a pipe. Only the CLI receives console handles.
func TestTokenizerConsoleRestorationProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) != 2 {
		t.Fatal("invalid tokenizer restoration helper arguments")
	}
	scenario, binary := args[0], args[1]
	ownWindowsTestProcessTree(t)
	if scenario == "build" {
		ctx, cancel := context.WithTimeout(t.Context(), 110*time.Second)
		defer cancel()
		build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go.exe"), "build", "-p", "2", "-o", binary, ".")
		build.WaitDelay = 3 * time.Second
		var output adminConsoleCapture
		build.Stdout, build.Stderr = &output, &output
		if err := build.Run(); err != nil || ctx.Err() != nil {
			t.Fatalf("build production CLI: %v; deadline=%v; %s", err, ctx.Err(), output.checkedText(t))
		}
		output.checkedText(t)
		return
	}
	seed := tokenizerRestorationState{InputMode: 0x87, OutputMode: 0x7, InputCP: 65001, OutputCP: 437}
	runs, character, wantExit := 1, uint16(3), 130
	switch scenario {
	case "cancel_repeat_cp437":
		seed = tokenizerRestorationState{InputMode: 0xa7, OutputMode: 0x3, InputCP: 437, OutputCP: 65001}
		runs = 2
	case "cancel_cp65001":
	case "invalid_utf16_cp65001":
		character, wantExit = 0xdc00, 1
	default:
		t.Fatal("unknown tokenizer restoration scenario")
	}
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Error(err)
		}
	})
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := output.Close(); err != nil {
			t.Error(err)
		}
	})
	if !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		t.Fatal("restoration requires genuine Windows console handles")
	}
	if err := windows.SetConsoleMode(windows.Handle(input.Fd()), seed.InputMode); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleMode(windows.Handle(output.Fd()), seed.OutputMode); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleCP(seed.InputCP); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleOutputCP(seed.OutputCP); err != nil {
		t.Fatal(err)
	}
	var evidence []tokenizerRestorationEvidence
	for i := range runs {
		// Clear only this owned screen. Never reset modes or code pages between runs.
		clearTokenizerRestorationScreen(t, output)
		evidence = append(evidence, observeTokenizerRestoration(t, binary, input, output, seed, character, wantExit, i+1))
	}
	if err := json.NewEncoder(os.Stdout).Encode(evidence); err != nil {
		t.Fatal(err)
	}
}

func tokenizerRestorationSnapshot(t *testing.T, input, output *os.File) tokenizerRestorationState {
	t.Helper()
	var state tokenizerRestorationState
	if err := windows.GetConsoleMode(windows.Handle(input.Fd()), &state.InputMode); err != nil {
		t.Fatal(err)
	}
	if err := windows.GetConsoleMode(windows.Handle(output.Fd()), &state.OutputMode); err != nil {
		t.Fatal(err)
	}
	var err error
	if state.InputCP, err = windows.GetConsoleCP(); err != nil {
		t.Fatal(err)
	}
	if state.OutputCP, err = windows.GetConsoleOutputCP(); err != nil {
		t.Fatal(err)
	}
	return state
}

func observeTokenizerRestoration(t *testing.T, binary string, input, output *os.File, seed tokenizerRestorationState, character uint16, wantExit, invocation int) tokenizerRestorationEvidence {
	t.Helper()
	evidence := tokenizerRestorationEvidence{Invocation: invocation, Before: tokenizerRestorationSnapshot(t, input, output)}
	if evidence.Before != seed {
		t.Fatalf("console seed or previous restoration differs: got %+v, want %+v", evidence.Before, seed)
	}
	const frameMarker = "Type or paste text"
	screen, err := readOutputConsole(output)
	if err != nil || strings.Contains(screen, frameMarker) {
		t.Fatalf("fresh screen prerequisite failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "tokenizer")
	command.Stdin, command.Stdout, command.Stderr = input, output, output
	command.WaitDelay = 3 * time.Second
	starting := time.Now()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	evidence.StartElapsed = time.Since(starting)
	readinessStarted := time.Now()
	evidence.PID = command.Process.Pid
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("terminated tokenizer did not exit before cleanup deadline")
			}
		}
	}()
	active := seed
	active.InputMode = (active.InputMode &^ 0x7) | 0x200
	active.OutputMode |= 0xc
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		evidence.Active = tokenizerRestorationSnapshot(t, input, output)
		screen, err = readOutputConsole(output)
		if err != nil {
			t.Fatal(err)
		}
		// The first frame follows input-bridge startup flushing. Raw mode alone does not.
		if evidence.Active == active && strings.Contains(screen, frameMarker) {
			evidence.FreshFrame = true
			evidence.ReadyElapsed = time.Since(readinessStarted)
			break
		}
		select {
		case err := <-done:
			waited = true
			t.Fatalf("tokenizer exited before active mode and fresh-frame readiness: %v; invocation=%d; process_start=%s; readiness=%s; frame=%t; state=%+v", err, invocation, evidence.StartElapsed, time.Since(readinessStarted), strings.Contains(screen, frameMarker), evidence.Active)
		case <-ctx.Done():
			t.Fatalf("tokenizer readiness deadline: %v; invocation=%d; process_start=%s; readiness=%s; frame=%t; state=%+v", ctx.Err(), invocation, evidence.StartElapsed, time.Since(readinessStarted), strings.Contains(screen, frameMarker), evidence.Active)
		case <-ticker.C:
		}
	}
	exitStarted := time.Now()
	writeTokenizerRestorationRecord(t, input, character)
	err = <-done
	waited = true
	evidence.ExitElapsed = time.Since(exitStarted)
	// Query before closing handles, fixture cleanup, or any subsequent process.
	evidence.After = tokenizerRestorationSnapshot(t, input, output)
	evidence.Exit = command.ProcessState.ExitCode()
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exit) || evidence.Exit != wantExit {
		t.Fatalf("tokenizer exit=%d, want=%d; error=%v; deadline=%v; evidence=%+v", evidence.Exit, wantExit, err, ctx.Err(), evidence)
	}
	if evidence.After != seed {
		t.Fatalf("production tokenizer did not restore modes or preserve code pages: %+v", evidence)
	}
	if wantExit == 1 {
		screen, err := readOutputConsole(output)
		if err != nil || !strings.Contains(screen, "Could not display the tokenizer editor") {
			t.Fatalf("input error lacked the public editor failure diagnostic: %v", err)
		}
	}
	return evidence
}

func writeTokenizerRestorationRecord(t *testing.T, input *os.File, character uint16) {
	t.Helper()
	// Encode KEY_EVENT_RECORD explicitly; the ASCII helper subtracts 32 for its virtual key.
	record := struct {
		kind, padding         uint16
		down                  int32
		repeat, key, scan, ch uint16
		controls              uint32
	}{kind: windows.KEY_EVENT, down: 1, repeat: 1, key: 0xe7, ch: character}
	if character == 3 {
		record.key, record.controls = 'C', 0x8 // Ctrl+C as input, not a broadcast console signal.
	}
	var written uint32
	function := windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")
	ok, _, err := function.Call(input.Fd(), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&written)))
	if ok == 0 {
		t.Fatal(err)
	}
	if written != 1 {
		t.Fatal(io.ErrShortWrite)
	}
}

func clearTokenizerRestorationScreen(t *testing.T, output *os.File) {
	t.Helper()
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(output.Fd()), &info); err != nil {
		t.Fatal(err)
	}
	// Match readOutputConsole's observed rows, without clearing unused scrollback.
	length := int(info.Size.X) * (max(int(info.CursorPosition.Y), int(info.Window.Bottom)) + 1)
	if info.Window.Right-info.Window.Left+1 < 40 || info.Window.Bottom-info.Window.Top+1 < 12 || length < 1 || length > 1<<20 {
		t.Fatalf("unsupported private console geometry: %+v", info)
	}
	var written uint32
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	ok, _, err := kernel.NewProc("FillConsoleOutputCharacterW").Call(output.Fd(), ' ', uintptr(length), 0, uintptr(unsafe.Pointer(&written)))
	if ok == 0 || int(written) != length {
		t.Fatal(fmt.Errorf("clear private screen: wrote %d of %d characters: %v", written, length, err))
	}
	if ok, _, err := kernel.NewProc("SetConsoleCursorPosition").Call(output.Fd(), 0); ok == 0 {
		t.Fatal(err)
	}
}
