//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

// Use the existing isolated-console and kill-on-close job owner. Real console
// handles are essential: redirected handles bypass the loading signal owner.
func TestMainDispatchOutputWindowsTerminalLifecycle(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("hash native entrypoint test executable: %v %v", copyErr, closeErr)
	}
	version := windows.RtlGetVersion()
	t.Logf("native Windows %d.%d build %d, %s, %s, production-main test executable SHA256=%x", version.MajorVersion, version.MinorVersion, version.BuildNumber, runtime.GOARCH, runtime.Version(), hash.Sum(nil))
	for _, scenario := range []string{"models-cancel", "images-cancel", "images-progress", "explore-quit"} {
		for _, quiet := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/quiet=%t", scenario, quiet), func(t *testing.T) {
				testWindowsOutputTerminalLifecycle(t, scenario, quiet)
			})
		}
	}
}

func testWindowsOutputTerminalLifecycle(t *testing.T, scenario string, quiet bool) {
	t.Helper()
	home := t.TempDir()
	payload := imageGenerationPNG(t)
	started, closed, fixtureRelease := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// net/http starts disconnect observation after the request body reaches EOF.
		// Read and close it before the parent can send cancellation or viewer input.
		body, readErr := io.ReadAll(r.Body)
		closeErr := r.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Errorf("read synthetic request: %v; close: %v", readErr, closeErr)
			return
		}
		close(started)
		if strings.HasSuffix(scenario, "cancel") {
			select {
			case <-r.Context().Done():
				close(closed)
			case <-fixtureRelease:
				// Cleanup must not report a successful client-side source close.
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if scenario == "images-progress" {
			defer close(closed)
			var request map[string]any
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				return
			}
			if request["partial_images"] != float64(1) || request["stream"] != true {
				t.Errorf("quiet changed image request: %+v", request)
			}
			encoded := base64.StdEncoding.EncodeToString(payload)
			fmt.Fprintf(w, "data: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":0,\"b64_json\":%q}\n\n", encoded)
			w.(http.Flusher).Flush()
			fmt.Fprintf(w, "data: {\"type\":\"image_generation.completed\",\"b64_json\":%q}\n\n", encoded)
			return
		}
		// Cover the existing viewer preload at the native console width.
		for i := range 256 {
			writeStreamingTextEvent(w, fmt.Sprintf(`{"type":"response.output_text.delta","delta":"synthetic-viewer-%03d","sequence_number":%d}`, i, i))
		}
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(closed) // q must close the source; the fixture never sends EOF.
		case <-fixtureRelease:
			// Failure cleanup releases the handler without satisfying the assertion.
		}
	}))
	t.Cleanup(func() {
		close(fixtureRelease)
		server.CloseClientConnections()
		server.Close()
	})
	args := []string{"models", "list"}
	if strings.HasPrefix(scenario, "images") {
		args = []string{"images", "generate", "--prompt", "synthetic native lifecycle", "--output-dir", home, "--name", "final", "--inline", "off"}
		if scenario == "images-progress" {
			args = append(args[:len(args)-1], "on", "--partial-images", "1")
		}
	} else if scenario == "explore-quit" {
		args = streamingTextArgs("responses", "--format", "explore")
	}
	if quiet {
		args = append([]string{"--quiet"}, args...)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := []string{"-test.run=^TestShellFileWindowsConsoleHelper$", "--", helper,
		"-test.run=^TestMainDispatchOutputWindowsTerminalChild$", "--", "openai"}
	process := exec.CommandContext(ctx, helper, append(command, args...)...)
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			process.Env = append(process.Env, name+"="+value)
		}
	}
	process.Env = append(process.Env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home,
		"TEMP="+home, "TMP="+home, "OPENAI_API_KEY=sk-fake-output-lifecycle", "OPENAI_BASE_URL="+server.URL,
		"OPENAI_CLI_OUTPUT_CONSOLE_SCENARIO="+scenario, "TERM=xterm-256color", "GOMAXPROCS=2")
	if scenario != "images-progress" {
		process.Env = append(process.Env, "NO_COLOR=1")
	}
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	input, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait(); close(done) }()
	t.Cleanup(func() {
		input.Close()
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			process.Process.Kill()
			<-done
		}
	})
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("native console exited before request: %v stdout=%q stderr=%q", err, &stdout, &stderr)
	case <-ctx.Done():
		t.Fatal("native console request did not start")
	}
	canceled := strings.HasSuffix(scenario, "cancel")
	if canceled {
		if _, err := input.Write([]byte{byte(windows.CTRL_C_EVENT)}); err != nil {
			t.Fatal(err)
		}
		input.Close()
	}
	if scenario == "explore-quit" {
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		var childPID int
		for childPID == 0 {
			data, err := os.ReadFile(filepath.Join(home, "main-returned"))
			if err == nil {
				childPID, err = strconv.Atoi(string(data))
				if err != nil || childPID <= 0 {
					t.Fatalf("invalid explorer child barrier: %q %v", data, err)
				}
				break
			}
			select {
			case err := <-done:
				t.Fatalf("explorer process exited before the cleanup barrier: %v", err)
			case <-deadline.C:
				t.Fatal("explorer main did not return before its cleanup barrier")
			case <-time.After(10 * time.Millisecond):
			}
		}
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Fatal("explorer main returned with its HTTP source still open")
		}
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(childPID))
		if err != nil {
			t.Fatal(err)
		}
		var state uint32
		stateErr := windows.GetExitCodeProcess(handle, &state)
		closeErr := windows.CloseHandle(handle)
		if stateErr != nil || closeErr != nil || state != 259 { // STILL_ACTIVE
			t.Fatalf("process teardown could have closed the source: state=%d query=%v close=%v", state, stateErr, closeErr)
		}
		if err := os.WriteFile(filepath.Join(home, "release-child"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	err = <-done
	wantCode, code := 0, 0
	if canceled {
		wantCode = 130
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != wantCode || ctx.Err() != nil || stderr.Len() != 0 {
		t.Fatalf("native console result=%d want=%d err=%v stdout=%q stderr=%q", code, wantCode, err, &stdout, &stderr)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("native console exit retained the HTTP source")
	}
	if scenario == "images-progress" {
		data, err := os.ReadFile(filepath.Join(home, "final.png"))
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("final saved content changed: bytes=%x err=%v", data, err)
		}
		var screen string
		if err := json.Unmarshal(stdout.Bytes(), &screen); err != nil {
			t.Fatalf("native screen observation missing: %v output=%q", err, &stdout)
		}
		if strings.Contains(screen, "Progress preview") == quiet || !strings.Contains(screen, "final.png") {
			t.Fatalf("quiet changed selected final output or retained progress: %q", screen)
		}
	} else if scenario == "explore-quit" {
		observed, err := os.ReadFile(filepath.Join(home, "viewer-observed.json"))
		if err != nil || !bytes.Contains(observed, []byte("synthetic-viewer-")) || !bytes.Contains(observed, []byte("q/enter")) {
			t.Fatalf("q did not follow an observed viewer: %q %v", observed, err)
		}
	} else {
		if files, err := filepath.Glob(filepath.Join(home, "*.png")); err != nil || len(files) != 0 {
			t.Fatalf("cancellation saved an incomplete image: %v %v", files, err)
		}
	}
}

// The existing console owner launches this production-entrypoint child. Only
// this child changes its standard handles; the parent test runner stays piped.
func TestMainDispatchOutputWindowsTerminalChild(t *testing.T) {
	scenario := os.Getenv("OPENAI_CLI_OUTPUT_CONSOLE_SCENARIO")
	if scenario == "" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing native output arguments")
	}
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	console, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	if !term.IsTerminal(input.Fd()) || !term.IsTerminal(console.Fd()) {
		t.Fatal("native lifecycle requires real console input and output")
	}
	for kind, handle := range map[uint32]windows.Handle{
		windows.STD_INPUT_HANDLE:  windows.Handle(input.Fd()),
		windows.STD_OUTPUT_HANDLE: windows.Handle(console.Fd()),
		windows.STD_ERROR_HANDLE:  windows.Handle(console.Fd()),
	} {
		if err := windows.SetStdHandle(kind, handle); err != nil {
			t.Fatal(err)
		}
	}
	output := os.Stdout
	os.Stdin, os.Stdout, os.Stderr = input, console, console
	os.Args = os.Args[separator+1:]
	if scenario == "explore-quit" {
		go func() {
			deadline := time.Now().Add(7 * time.Second)
			for time.Now().Before(deadline) {
				screen, err := readOutputConsole(console)
				if err == nil && strings.Contains(screen, "synthetic-viewer-") && strings.Contains(screen, "q/enter") {
					data, _ := json.Marshal(screen)
					if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "viewer-observed.json"), data, 0600); err != nil {
						return
					}
					if err := writeOutputConsoleKey(input, 'q'); err != nil {
						fmt.Fprintln(output, "native viewer input failed:", err)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}
	main()
	if scenario == "explore-quit" {
		marker := filepath.Join(os.Getenv("HOME"), "main-returned")
		if err := os.WriteFile(marker+".tmp", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(125)
		}
		if err := os.Rename(marker+".tmp", marker); err != nil {
			os.Exit(125)
		}
		deadline := time.Now().Add(5 * time.Second)
		released := false
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "release-child")); err == nil {
				released = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !released {
			os.Exit(125)
		}
	}
	screen, err := readOutputConsole(console)
	if err != nil {
		fmt.Fprintln(output, err)
		os.Exit(125)
	}
	if err := json.NewEncoder(output).Encode(screen); err != nil {
		os.Exit(125)
	}
	os.Exit(0)
}

func readOutputConsole(console *os.File) (string, error) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(console.Fd()), &info); err != nil {
		return "", err
	}
	length := int(info.Size.X) * (max(int(info.CursorPosition.Y), int(info.Window.Bottom)) + 1)
	if length < 1 {
		return "", errors.New("empty console geometry")
	}
	buffer := make([]uint16, length)
	var read uint32
	function := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleOutputCharacterW")
	ok, _, err := function.Call(console.Fd(), uintptr(unsafe.Pointer(&buffer[0])), uintptr(length), 0, uintptr(unsafe.Pointer(&read)))
	if ok == 0 {
		return "", err
	}
	return string(utf16.Decode(buffer[:read])), nil
}

func writeOutputConsoleKey(input *os.File, value uint16) error {
	// INPUT_RECORD has a four-byte header followed by a KEY_EVENT_RECORD.
	record := struct {
		kind, padding         uint16
		down                  int32
		repeat, key, scan, ch uint16
		controls              uint32
	}{kind: windows.KEY_EVENT, down: 1, repeat: 1, key: value - 32, ch: value}
	var written uint32
	function := windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")
	ok, _, err := function.Call(input.Fd(), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&written)))
	if ok == 0 {
		return err
	}
	if written != 1 {
		return io.ErrShortWrite
	}
	return nil
}
