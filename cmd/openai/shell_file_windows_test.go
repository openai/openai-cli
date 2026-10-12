//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"unsafe"

	"golang.org/x/sys/windows"
)

// This uses real Windows shells and the built CLI, independently of shell text conversion.
func TestMainShellWindowsNative(t *testing.T) {
	work := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(work, "openai.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go.exe"), "build", "-p", "2", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build native CLI: %v\n%s", err, output)
	}
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("hash native CLI: %v, %v", copyErr, closeErr)
	}
	version := windows.RtlGetVersion()
	t.Logf("native Windows %d.%d build %d, %s, %s, CLI SHA256=%x", version.MajorVersion, version.MinorVersion, version.BuildNumber, runtime.GOARCH, runtime.Version(), hash.Sum(nil))

	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte{0, 255, 254, 26, '\r', '\n'}, []byte("synthetic Unicode Ω\n")...)
	prompt := []byte("synthetic Unicode Ω\n")
	for name, data := range map[string][]byte{
		"payload.bin": payload, "sample with spaces.wav": payload, "@sample.wav": payload, "-": payload,
		"prompt.txt": prompt, "field.txt": []byte("{\"text\":\"still field text\"}\n"),
		"request.json": []byte(`{"model":"from-pipe","input":"whole request"}`),
		"request.yaml": []byte("model: from-pipe\ninput: whole request\n"),
	} {
		if err := os.WriteFile(filepath.Join(work, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	shells := []nativeShell{
		{"cmd", "cmd.exe", "", `set "OPENAI_API_KEY=fake-native-shell-key" && `, []string{"/D", "/C"}},
		{"powershell", "powershell.exe", "", `$env:OPENAI_API_KEY = 'fake-native-shell-key'; `, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
		{"pwsh", "pwsh.exe", "", `$env:OPENAI_API_KEY = 'fake-native-shell-key'; `, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
	}
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range shells {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required native shell unavailable: %v", err)
				}
				t.Skipf("native shell unavailable: %v", err)
			}
			shell.executable = helper
			shell.args = append([]string{"-test.run=^TestShellFileWindowsProcessHelper$", "--", path}, shell.args...)
			home := t.TempDir()
			command := func(executable string, args ...string) string {
				values := append([]string{executable}, args...)
				for i, value := range values {
					if shell.name == "cmd" {
						values[i] = `"` + value + `"`
					} else {
						values[i] = "'" + strings.ReplaceAll(value, "'", "''") + "'"
					}
				}
				prefix := ""
				if shell.name != "cmd" {
					prefix = "& "
				}
				return prefix + strings.Join(values, " ")
			}
			transport := func(mode, path string) string {
				return command(helper, "-test.run=^TestShellFileWindowsTransportHelper$", "--", mode, path)
			}
			versionScript := "ver"
			if shell.name != "cmd" {
				versionScript = "$PSVersionTable.PSVersion.ToString()"
			}
			gotVersion := runNativeShell(t, shell, work, home, "http://127.0.0.1:1", versionScript)
			if gotVersion.code != 0 || gotVersion.stderr != "" {
				t.Fatalf("shell version: %+v", gotVersion)
			}
			t.Logf("%s version: %s", shell.name, strings.TrimSpace(gotVersion.stdout))
			preservesNativeBytes := shell.name == "cmd"
			if shell.name != "cmd" {
				fields := strings.Split(strings.TrimSpace(gotVersion.stdout), ".")
				if len(fields) < 2 {
					t.Fatal("unrecognized PowerShell version")
				}
				major, majorErr := strconv.Atoi(fields[0])
				minor, minorErr := strconv.Atoi(fields[1])
				if majorErr != nil || minorErr != nil {
					t.Fatal("unrecognized PowerShell version")
				}
				preservesNativeBytes = major > 7 || major == 7 && minor >= 4
			}
			t.Run("field and whole request", func(t *testing.T) {
				requests := make(chan map[string]any, 8)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					requests <- body
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"resp_synthetic","object":"response","output":[]}`)
				}))
				defer server.Close()
				cli := command(binary, "responses", "create", "--model", "explicit-model")
				for _, input := range []string{"request.json", "request.yaml"} {
					got := runNativeShell(t, shell, work, home, server.URL, shell.setKey+transport("produce", input)+" | "+cli)
					body := windowsShellRequest(t, requests, got)
					if got.code != 0 || body["input"] != "whole request" || body["model"] != "explicit-model" {
						t.Fatalf("whole request %s: %+v body=%v", input, got, body)
					}
				}
				capture := "field-" + shell.name + ".bin"
				got := runNativeShell(t, shell, work, home, server.URL, transport("produce", "field.txt")+" | "+transport("capture", capture))
				if got.code != 0 {
					t.Fatalf("capture native shell input: %+v", got)
				}
				observed, err := os.ReadFile(filepath.Join(work, capture))
				if err != nil {
					t.Fatal(err)
				}
				if len(observed) == 0 {
					t.Fatal("shell field control produced no bytes")
				}
				got = runNativeShell(t, shell, work, home, server.URL, shell.setKey+transport("produce", "field.txt")+" | "+cli+" --input "+commandArgument(shell, "@-"))
				if body := windowsShellRequest(t, requests, got); body["input"] != string(observed) {
					t.Fatalf("explicit JSON-looking field: %+v body=%v expected=%q", got, body, observed)
				}
				got = runNativeShell(t, shell, work, home, server.URL, shell.setKey+cli+" --input "+commandArgument(shell, "@prompt.txt"))
				if body := windowsShellRequest(t, requests, got); body["input"] != string(prompt) {
					t.Fatalf("text file: %+v body=%v", got, body)
				}
			})
			t.Run("binary input and literal paths", func(t *testing.T) {
				type upload struct {
					name string
					data []byte
				}
				requests := make(chan upload, 8)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reader, err := r.MultipartReader()
					if err != nil {
						t.Error(err)
						http.Error(w, "synthetic multipart failure", 400)
						return
					}
					for {
						part, err := reader.NextPart()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Error(err)
							return
						}
						data, err := io.ReadAll(part)
						if err != nil {
							t.Error(err)
						}
						if part.FormName() == "file" {
							requests <- upload{part.FileName(), data}
						}
					}
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"file_synthetic","object":"file"}`)
				}))
				defer server.Close()
				cli := command(binary, "files", "create", "--purpose", "user_data", "--file")
				for _, path := range []string{"sample with spaces.wav", "@sample.wav", `.\-`} {
					got := runNativeShell(t, shell, work, home, server.URL, shell.setKey+cli+" "+commandArgument(shell, path))
					value := windowsShellRequest(t, requests, got)
					if got.code != 0 || !bytes.Equal(value.data, payload) || value.name != filepath.Base(path) {
						t.Fatalf("literal upload %s: %+v filename=%q bytes=%x", path, got, value.name, value.data)
					}
				}
				producers := []struct {
					name, script string
					exact        bool
				}{{"native", transport("produce", "payload.bin"), preservesNativeBytes}}
				if shell.name != "cmd" {
					producers = append(producers, struct {
						name, script string
						exact        bool
					}{"objects", "[IO.File]::ReadAllBytes('payload.bin')", false})
				}
				for _, producer := range producers {
					capture := "input-" + shell.name + "-" + producer.name + ".bin"
					got := runNativeShell(t, shell, work, home, server.URL, producer.script+" | "+transport("capture", capture))
					if got.code != 0 {
						t.Fatalf("capture shell bytes: %+v", got)
					}
					observed, err := os.ReadFile(filepath.Join(work, capture))
					if err != nil {
						t.Fatal(err)
					}
					if len(observed) == 0 {
						t.Fatal("shell pipeline control produced no bytes")
					}
					t.Logf("%s %s pipeline byte-preserving=%t source=%x received=%x", shell.name, producer.name, bytes.Equal(observed, payload), sha256.Sum256(payload), sha256.Sum256(observed))
					if producer.exact && !bytes.Equal(observed, payload) {
						t.Fatal("native byte pipeline changed bytes")
					}
					got = runNativeShell(t, shell, work, home, server.URL, shell.setKey+producer.script+" | "+cli+" -")
					if value := windowsShellRequest(t, requests, got); value.name != "anonymous_file" || !bytes.Equal(value.data, observed) {
						t.Fatalf("CLI changed shell-supplied binary input: %+v", got)
					}
				}
			})
			t.Run("downloads and managed saves", func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/octet-stream")
					if strings.Contains(r.URL.Path, "truncated") {
						w.Header().Set("Content-Length", strconv.Itoa(len(payload)+100))
					}
					w.Write(payload)
				}))
				defer server.Close()
				control := "redirect-control-" + shell.name + ".bin"
				got := runNativeShell(t, shell, work, home, server.URL, transport("produce", "payload.bin")+" > "+control)
				if got.code != 0 {
					t.Fatalf("redirection control: %+v", got)
				}
				observed, err := os.ReadFile(filepath.Join(work, control))
				if err != nil {
					t.Fatal(err)
				}
				if len(observed) == 0 {
					t.Fatal("shell redirection control produced no bytes")
				}
				t.Logf("%s redirection byte-preserving=%t source=%x redirected=%x", shell.name, bytes.Equal(observed, payload), sha256.Sum256(payload), sha256.Sum256(observed))
				if preservesNativeBytes && !bytes.Equal(observed, payload) {
					t.Fatal("native redirection changed bytes")
				}
				for _, extra := range []string{"", " --output -"} {
					got = runNativeShell(t, shell, work, home, server.URL, shell.setKey+command(binary, "files", "content", "file_synthetic")+extra+" > redirected.bin")
					data, err := os.ReadFile(filepath.Join(work, "redirected.bin"))
					if got.code != 0 || err != nil || !bytes.Equal(data, observed) {
						t.Fatalf("CLI redirection: %+v read=%v", got, err)
					}
				}
				path := filepath.Join(work, "saved with spaces.bin")
				for _, fileID := range []string{"file_synthetic", "truncated"} {
					if err := os.WriteFile(path, []byte("GOOD"), 0600); err != nil {
						t.Fatal(err)
					}
					got = runNativeShell(t, shell, work, home, server.URL, shell.setKey+command(binary, "files", "content", fileID, "--output", path))
					data, err := os.ReadFile(path)
					if fileID == "truncated" {
						if got.code == 0 || got.stdout != "" || !strings.Contains(got.stderr, "Download incomplete") || string(data) != "GOOD" {
							t.Fatalf("incomplete save: %+v bytes=%x", got, data)
						}
					} else {
						if got.code != 0 || !bytes.Equal(data, payload) || got.stdout != "" || !strings.Contains(got.stderr, "Wrote output to: "+path) {
							t.Fatalf("managed save: %+v bytes=%x", got, data)
						}
						t.Logf("%s native receipt exact-stderr=%t", shell.name, got.stderr == "Wrote output to: "+path+"\n")
					}
					if err != nil {
						t.Fatal(err)
					}
					assertWindowsDownloadStagesAbsent(t, work)
				}
			})
		})
	}
	for _, existing := range []bool{false, true} {
		for _, event := range []uint32{windows.CTRL_C_EVENT, windows.CTRL_BREAK_EVENT} {
			t.Run(fmt.Sprintf("console cancellation/existing=%t/event=%d", existing, event), func(t *testing.T) { testWindowsDownloadConsoleCancellation(t, binary, event, existing) })
		}
	}
}

func commandArgument(shell nativeShell, value string) string {
	if shell.name == "cmd" {
		return `"` + value + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func windowsShellRequest[T any](t *testing.T, requests <-chan T, result mainDispatchResult) T {
	t.Helper()
	if result.code != 0 {
		t.Fatalf("native command failed before expected HTTP request: %+v", result)
	}
	select {
	case value := <-requests:
		return value
	case <-time.After(2 * time.Second):
		t.Fatalf("expected loopback request did not arrive: %+v", result)
	}
	var zero T
	return zero
}

// The helper joins the job before starting children. Process exit closes the job
// handle, so a timed-out shell cannot leave descendants holding capture pipes.
func ownWindowsTestProcessTree(t *testing.T) {
	t.Helper()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		t.Fatal(err)
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(job)
		t.Fatal(err)
	}
	// Keep this handle open until process exit; closing it would kill this helper.
}

func TestShellFileWindowsProcessHelper(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) < 2 {
		t.Fatal("invalid shell helper arguments")
	}
	ownWindowsTestProcessTree(t)
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, args[0], args[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := child.Run()
	if ctx.Err() != nil {
		t.Fatalf("native shell timed out: %v", ctx.Err())
	}
	if err == nil {
		os.Exit(0)
	}
	if exit, ok := err.(*exec.ExitError); ok {
		os.Exit(exit.ExitCode())
	}
	t.Fatal(err)
}

// A plain native producer/consumer exposes shell transformations without CLI parsing.
func TestShellFileWindowsTransportHelper(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) != 2 {
		t.Fatal("invalid transport helper arguments")
	}
	var err error
	switch args[0] {
	case "produce":
		var data []byte
		data, err = os.ReadFile(args[1])
		if err == nil {
			_, err = os.Stdout.Write(data)
		}
	case "capture":
		var data []byte
		data, err = io.ReadAll(os.Stdin)
		if err == nil {
			err = os.WriteFile(args[1], data, 0600)
		}
	default:
		t.Fatal("unknown transport helper operation")
	}
	if err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func assertWindowsDownloadStagesAbsent(t *testing.T, directory string) {
	t.Helper()
	stages, err := filepath.Glob(filepath.Join(directory, ".openai-download-*.tmp"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("remaining staging files=%v error=%v", stages, err)
	}
}

func testWindowsDownloadConsoleCancellation(t *testing.T, binary string, event uint32, existing bool) {
	work := t.TempDir()
	destination := filepath.Join(work, "canceled.bin")
	if existing {
		if err := os.WriteFile(destination, []byte("GOOD"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.Write([]byte("PARTIAL"))
		w.(http.Flusher).Flush()
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release) // The source stays blocked until the child exits; EOF cannot imitate cancellation.
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, helper, "-test.run=^TestShellFileWindowsConsoleHelper$", "--", binary, "files", "content", "file_synthetic", "--output", destination)
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			process.Env = append(process.Env, name+"="+value)
		}
	}
	process.Env = append(process.Env, "HOME="+work, "USERPROFILE="+work, "APPDATA="+work, "LOCALAPPDATA="+work, "TEMP="+work, "TMP="+work, "OPENAI_API_KEY=sk-fake-windows-save", "OPENAI_BASE_URL="+server.URL, "NO_COLOR=1", "TERM=dumb", "GOMAXPROCS=2")
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
	defer func() {
		input.Close() // EOF instructs the isolated helper to kill and reap its CLI child.
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			process.Process.Kill()
			<-done
		}
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("console child exited before request: %v stderr=%s", err, &stderr)
	case <-time.After(5 * time.Second):
		t.Fatal("console child did not start a request")
	}
	deadline, staged := time.Now().Add(5*time.Second), false
	for time.Now().Before(deadline) {
		names, _ := filepath.Glob(filepath.Join(work, ".openai-download-*.tmp"))
		for _, name := range names {
			data, _ := os.ReadFile(name)
			staged = staged || string(data) == "PARTIAL"
		}
		if staged {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !staged {
		t.Fatal("active download staging was not observed")
	}
	if _, err := input.Write([]byte{byte(event)}); err != nil {
		t.Fatal(err)
	}
	input.Close()
	err = <-done
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 130 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "Download incomplete") {
		t.Fatalf("event=%d result=%v stdout=%q stderr=%q", event, err, &stdout, &stderr)
	}
	data, readErr := os.ReadFile(destination)
	if existing {
		if readErr != nil || string(data) != "GOOD" {
			t.Fatalf("prior destination changed: %q %v", data, readErr)
		}
	} else if !os.IsNotExist(readErr) {
		t.Fatalf("incomplete new destination remains: %q %v", data, readErr)
	}
	assertWindowsDownloadStagesAbsent(t, work)
}

// Only this helper and its CLI child share the newly allocated console.
func TestShellFileWindowsConsoleHelper(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) < 2 {
		t.Fatal("invalid console helper arguments")
	}
	ownWindowsTestProcessTree(t)
	setHandler := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
	if ok, _, err := setHandler.Call(0, 0); ok == 0 {
		t.Fatalf("enable console input: %v", err)
	}
	callback := syscall.NewCallback(func(uint32) uintptr { return 1 })
	if ok, _, err := setHandler.Call(callback, 1); ok == 0 {
		t.Fatalf("isolate console helper: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, args[0], args[1:]...)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	events := make(chan byte, 1)
	go func() {
		var event [1]byte
		if _, err := io.ReadFull(os.Stdin, event[:]); err != nil {
			events <- 255
		} else {
			events <- event[0]
		}
	}()
	var result error
	select {
	case result = <-done:
	case event := <-events:
		if event > 1 {
			child.Process.Kill()
			<-done
			os.Exit(125)
		}
		if err := windows.GenerateConsoleCtrlEvent(uint32(event), 0); err != nil {
			child.Process.Kill()
			<-done
			t.Fatal(err)
		}
		result = <-done
	}
	if result == nil {
		os.Exit(0)
	}
	if exit, ok := result.(*exec.ExitError); ok {
		os.Exit(exit.ExitCode())
	}
	t.Fatal(result)
}
