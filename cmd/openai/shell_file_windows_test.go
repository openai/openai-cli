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
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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
	copyShellTestCLI(t, binary)
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
			t.Logf("%s phase=version-discovery", shell.name)
			gotVersion := runNativeShell(t, shell, work, home, "http://127.0.0.1:1", versionScript)
			if gotVersion.code != 0 || gotVersion.stderr != "" {
				t.Fatalf("shell version: %+v", gotVersion)
			}
			t.Logf("%s version: %s", shell.name, strings.TrimSpace(gotVersion.stdout))
			preservesNativeBytes := shell.name == "cmd"
			legacyArguments := false
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
				legacyArguments = major < 7 || major == 7 && minor < 3
			}
			t.Run("request configuration", func(t *testing.T) {
				testWindowsRequestConfiguration(t, shell, work, binary, helper, command, legacyArguments)
			})
			t.Run("file upload receipt", func(t *testing.T) {
				testWindowsFilesConsoleReceipt(t, shell.name, path, binary, helper, command)
			})
			t.Run("field and whole request", func(t *testing.T) {
				requests := make(chan map[string]any, 8)
				var requestCount atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requestCount.Add(1)
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					requests <- body
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"resp_synthetic","object":"response","output":[]}`)
				}))
				defer server.Close()
				cliArgs := []string{"responses", "create", "--model", "explicit-model"}
				cli := command(binary, cliArgs...)
				prefilledStdin := command(helper, append([]string{
					"-test.run=^TestShellFileWindowsTransportHelper$", "--", "prefilled-stdin", binary,
				}, cliArgs...)...)
				for _, input := range []string{"request.json", "request.yaml"} {
					// Preserve shell text conversion, then prefill the CLI's owned pipe.
					producer := "type " + commandArgument(shell, input)
					if shell.name != "cmd" {
						producer = "$requestText = [IO.File]::ReadAllText(" + commandArgument(shell, input) + "); $requestText"
					}
					prefilled := command(helper, append([]string{
						"-test.run=^TestShellFileWindowsTransportHelper$", "--", "prefilled-input", input, binary,
					}, cliArgs...)...)
					for _, operation := range []struct{ name, script string }{
						{"shell text", producer + " | " + prefilledStdin},
						{"prefilled pipe", prefilled},
					} {
						before := requestCount.Load()
						got := runNativeShell(t, shell, work, home, server.URL, shell.setKey+operation.script)
						body := windowsShellRequest(t, requests, got)
						count := requestCount.Load() - before
						if got.code != 0 || got.stderr != "" || len(body) != 2 || body["input"] != "whole request" || body["model"] != "explicit-model" || count != 1 {
							t.Fatalf("whole request %s (%s): %+v body=%v requests=%d", input, operation.name, got, body, count)
						}
						select {
						case extra := <-requests:
							t.Fatalf("whole request %s (%s) sent an extra request: %v", input, operation.name, extra)
						default:
						}
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
	testWindowsStreamCancellation(t, binary)
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

const windowsShellDiagnosticTimeout = 100 * time.Millisecond

func TestShellFileWindowsProcessHelper(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	var trace *os.File
	if len(args) >= 3 && args[0] == "observe-start" {
		var err error
		trace, err = os.OpenFile(args[1], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		args = args[2:]
	}
	closeTrace := func() {
		if trace != nil {
			if err := trace.Close(); err != nil {
				t.Fatalf("close startup checkpoints: %v", err)
			}
			trace = nil
		}
	}
	defer closeTrace()
	checkpoint := func(phase string) {
		if trace != nil {
			if _, err := fmt.Fprintf(trace, "%s supervisor_pid=%d utc=%s\n", phase, os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
	}
	controlledTimeout := len(args) > 0 && args[0] == "timeout-after-ready"
	if controlledTimeout {
		args = args[1:]
	}
	if len(args) < 2 {
		t.Fatal("invalid shell helper arguments")
	}
	ownWindowsTestProcessTree(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	started := time.Now()
	var timeout atomic.Int64
	timeout.Store(int64(18 * time.Second))
	// Keep process creation inside the ordinary deadline and the caller's watchdog.
	watchdog := time.AfterFunc(18*time.Second, func() { cancel(context.DeadlineExceeded) })
	defer watchdog.Stop()
	child := exec.CommandContext(ctx, args[0], args[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if controlledTimeout {
		// The diagnostic parent signals only after both owned processes are ready.
		// Keep this control pipe separate from the stalled child's input.
		child.Stdin = nil
		go func() {
			var signal [1]byte
			if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil || signal[0] != '!' {
				cancel(errors.New("invalid diagnostic timeout signal"))
				return
			}
			if !watchdog.Stop() {
				cancel(errors.New("diagnostic fallback elapsed before readiness signal"))
				return
			}
			timeout.Store(int64(windowsShellDiagnosticTimeout))
			watchdog.Reset(windowsShellDiagnosticTimeout)
		}()
	}
	checkpoint("before-shell-start")
	starting := time.Now()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	startup := time.Since(starting)
	checkpoint(fmt.Sprintf("after-shell-start shell_pid=%d process_start_ns=%d", child.Process.Pid, startup.Nanoseconds()))
	err := child.Wait()
	checkpoint("after-shell-exit")
	closeTrace()
	if ctx.Err() != nil {
		pid, exit, userCPU, systemCPU := "unavailable", "unavailable", "unavailable", "unavailable"
		if child.Process != nil {
			pid = strconv.Itoa(child.Process.Pid)
		}
		if child.ProcessState != nil {
			exit = strconv.Itoa(child.ProcessState.ExitCode())
			userCPU, systemCPU = child.ProcessState.UserTime().String(), child.ProcessState.SystemTime().String()
		}
		t.Fatalf("native shell timed out: %v; timeout=%s startup=%s elapsed=%s process=%t process_state=%t pid=%s exit=%s user_cpu=%s system_cpu=%s error_type=%T",
			context.Cause(ctx), time.Duration(timeout.Load()), startup, time.Since(started), child.Process != nil, child.ProcessState != nil, pid, exit, userCPU, systemCPU, err)
	}
	if err == nil {
		os.Exit(0)
	}
	if exit, ok := err.(*exec.ExitError); ok {
		os.Exit(exit.ExitCode())
	}
	t.Fatal(err)
}

// These controls use this test executable, without building or starting the CLI.
func TestMainShellWindowsProcessDiagnostics(t *testing.T) {
	stdoutBytes := []byte("\x00\xff\x1a\r\n\x1b[31msynthetic-private-stdout\n")
	stderrBytes := []byte("\xfe\x00\x1b[0m\r\nsynthetic-private-stderr\n")
	type processIdentity struct {
		PID      uint32
		Creation windows.Filetime
	}
	if separator := slices.Index(os.Args, "--"); separator >= 0 {
		args := os.Args[separator+1:]
		if len(args) < 2 {
			t.Fatal("invalid diagnostic control arguments")
		}
		mode, work := args[0], args[1]
		ready := func(name string) {
			identity := processIdentity{PID: uint32(os.Getpid())}
			var exit, kernel, user windows.Filetime
			if err := windows.GetProcessTimes(windows.CurrentProcess(), &identity.Creation, &exit, &kernel, &user); err != nil {
				t.Fatalf("identify ready control process: error_type=%T", err)
			}
			data, err := json.Marshal(identity)
			if err != nil {
				t.Fatalf("encode control readiness: error_type=%T", err)
			}
			path := filepath.Join(work, name+".ready")
			if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
				t.Fatalf("write control readiness: error_type=%T", err)
			}
			if err := os.Rename(path+".tmp", path); err != nil {
				t.Fatalf("publish control readiness: error_type=%T", err)
			}
		}
		if mode == "descendant" {
			for _, stream := range []*os.File{os.Stdout, os.Stderr} {
				kind, err := windows.GetFileType(windows.Handle(stream.Fd()))
				if err != nil || kind != windows.FILE_TYPE_PIPE {
					t.Fatalf("descendant did not inherit a capture pipe: kind=%d error_type=%T", kind, err)
				}
			}
			ready("descendant")
			time.Sleep(time.Hour)
			os.Exit(125)
		}
		for _, stream := range []struct {
			file *os.File
			data []byte
		}{{os.Stdout, stdoutBytes}, {os.Stderr, stderrBytes}} {
			if n, err := stream.file.Write(stream.data); err != nil || n != len(stream.data) {
				t.Fatalf("write control stream: bytes=%d error_type=%T", n, err)
			}
		}
		switch mode {
		case "success":
			os.Exit(0)
		case "nonzero":
			os.Exit(37)
		case "timeout":
			ready("child")
			descendant := exec.Command(os.Args[0], "-test.run=^TestMainShellWindowsProcessDiagnostics$", "--", "descendant", work)
			descendant.Stdout, descendant.Stderr = os.Stdout, os.Stderr
			if err := descendant.Start(); err != nil {
				t.Fatalf("start control descendant: error_type=%T", err)
			}
			defer descendant.Process.Kill()
			// The process helper must kill this child; its Job Object must kill the descendant.
			err := descendant.Wait()
			t.Fatalf("control descendant exited before cancellation: error_type=%T", err)
		default:
			t.Fatal("unknown diagnostic control")
		}
		return
	}

	helper, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: error_type=%T", err)
	}
	for _, control := range []struct {
		name string
		code int
	}{{"success", 0}, {"nonzero", 37}, {"timeout", 1}} {
		t.Run(control.name, func(t *testing.T) {
			work := filepath.Join(t.TempDir(), "synthetic-private-directory")
			if err := os.Mkdir(work, 0700); err != nil {
				t.Fatalf("create control directory: error_type=%T", err)
			}
			const privateArgument = "synthetic-private-argument-\x1b[31m"
			const privateEnvironment = "synthetic-private-environment"
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, helper, "-test.run=^TestShellFileWindowsProcessHelper$", "--",
				helper, "-test.run=^TestMainShellWindowsProcessDiagnostics$", "--", control.name, work, privateArgument)
			var deadlineWriter *os.File
			if control.name == "timeout" {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatalf("create diagnostic control pipe: error_type=%T", err)
				}
				defer reader.Close()
				defer writer.Close()
				deadlineWriter, process.Stdin = writer, reader
				process.Args = slices.Insert(process.Args, 3, "timeout-after-ready")
			}
			checkpointPath := filepath.Join(work, "startup-checkpoints.txt")
			process.Args = slices.Insert(process.Args, 3, "observe-start", checkpointPath)
			process.Dir = work
			for _, name := range []string{"SystemRoot", "WINDIR"} {
				if value, ok := os.LookupEnv(name); ok {
					process.Env = append(process.Env, name+"="+value)
				}
			}
			process.Env = append(process.Env, "GOMAXPROCS=2", "OPENAI_CLI_TEST_PRIVATE="+privateEnvironment)
			stdoutRead, stdoutWrite, err := os.Pipe()
			if err != nil {
				t.Fatalf("create stdout capture pipe: error_type=%T", err)
			}
			defer stdoutRead.Close()
			defer stdoutWrite.Close()
			stderrRead, stderrWrite, err := os.Pipe()
			if err != nil {
				t.Fatalf("create stderr capture pipe: error_type=%T", err)
			}
			defer stderrRead.Close()
			defer stderrWrite.Close()
			var stdout, stderr bytes.Buffer
			process.Stdout, process.Stderr = stdoutWrite, stderrWrite
			started := time.Now()
			if err := process.Start(); err != nil {
				t.Fatalf("start process helper: error_type=%T", err)
			}
			done := make(chan error, 1)
			go func() { done <- process.Wait() }()
			stdoutDone, stderrDone := make(chan error, 1), make(chan error, 1)
			go func() {
				_, err := io.Copy(&stdout, stdoutRead)
				stdoutDone <- err
			}()
			go func() {
				_, err := io.Copy(&stderr, stderrRead)
				stderrDone <- err
			}()
			waited := false
			stdoutDrained, stderrDrained := false, false
			var handles []windows.Handle
			var pids []int
			defer func() {
				if !waited {
					process.Process.Kill()
				}
				for _, handle := range handles {
					state, err := windows.WaitForSingleObject(handle, 2000)
					if err != nil || state != windows.WAIT_OBJECT_0 {
						t.Errorf("owned control process remained active: wait=%d error_type=%T", state, err)
						windows.TerminateProcess(handle, 125)
						if state, err := windows.WaitForSingleObject(handle, 2000); err != nil || state != windows.WAIT_OBJECT_0 {
							t.Errorf("owned control process cleanup failed: wait=%d error_type=%T", state, err)
						}
					}
					if err := windows.CloseHandle(handle); err != nil {
						t.Errorf("close control process handle: error_type=%T", err)
					}
				}
				if !waited {
					<-done
				}
				// Closing readers only during cleanup cannot satisfy the natural EOF assertions.
				if err := stdoutRead.Close(); err != nil {
					t.Errorf("close stdout capture pipe: error_type=%T", err)
				}
				if err := stderrRead.Close(); err != nil {
					t.Errorf("close stderr capture pipe: error_type=%T", err)
				}
				if !stdoutDrained {
					<-stdoutDone
				}
				if !stderrDrained {
					<-stderrDone
				}
			}()
			stdoutCloseErr, stderrCloseErr := stdoutWrite.Close(), stderrWrite.Close()
			if stdoutCloseErr != nil || stderrCloseErr != nil {
				t.Fatalf("close parent capture writers: stdout_error_type=%T stderr_error_type=%T", stdoutCloseErr, stderrCloseErr)
			}
			var signaled time.Time
			if control.name == "timeout" {
				readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
				defer readyCancel()
				for _, name := range []string{"child", "descendant"} {
					for {
						data, readErr := os.ReadFile(filepath.Join(work, name+".ready"))
						if readErr != nil && !os.IsNotExist(readErr) {
							var errno syscall.Errno
							hasErrno := errors.As(readErr, &errno)
							t.Fatalf("read control readiness: error_type=%T errno_available=%t errno=%d", readErr, hasErrno, uintptr(errno))
						}
						if readErr == nil {
							var identity processIdentity
							if err := json.Unmarshal(data, &identity); err != nil || identity.PID == 0 || identity.Creation == (windows.Filetime{}) {
								t.Fatalf("invalid control readiness: error_type=%T", err)
							}
							handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, identity.PID)
							if err != nil {
								t.Fatalf("open ready %s process: error_type=%T", name, err)
							}
							var creation, exit, kernel, user windows.Filetime
							err = windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user)
							if err != nil || creation != identity.Creation {
								closeErr := windows.CloseHandle(handle)
								t.Fatalf("ready %s process identity changed: error_type=%T close_error_type=%T", name, err, closeErr)
							}
							// Only the verified lifetime may enter termination-authorized cleanup.
							handles, pids = append(handles, handle), append(pids, int(identity.PID))
							if state, err := windows.WaitForSingleObject(handle, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
								t.Fatalf("ready %s process was not running: wait=%d error_type=%T", name, state, err)
							}
							break
						}
						select {
						case err := <-done:
							waited = true
							t.Fatalf("helper exited before %s readiness: error_type=%T", name, err)
						case <-readyCtx.Done():
							t.Fatalf("control %s did not become ready", name)
						case <-time.After(10 * time.Millisecond):
						}
					}
				}
				t.Log("stalled child and inherited-pipe descendant ready; process handles anchored")
				signaled = time.Now()
				if n, err := deadlineWriter.Write([]byte{'!'}); err != nil || n != 1 {
					t.Fatalf("signal diagnostic deadline: bytes=%d error_type=%T", n, err)
				}
			}
			err = <-done
			waited = true
			if ctx.Err() != nil {
				t.Fatal("process helper reached the outer deadline")
			}
			code := -1
			if process.ProcessState != nil {
				code = process.ProcessState.ExitCode()
			}
			if code != control.code || (control.code == 0) != (err == nil) {
				t.Fatalf("process helper status mismatch: got=%d want=%d error_type=%T", code, control.code, err)
			}
			for _, stream := range []struct {
				name    string
				done    <-chan error
				drained *bool
			}{{"stdout", stdoutDone, &stdoutDrained}, {"stderr", stderrDone, &stderrDrained}} {
				select {
				case err := <-stream.done:
					*stream.drained = true
					if err != nil {
						t.Fatalf("%s capture failed before EOF: error_type=%T", stream.name, err)
					}
				case <-ctx.Done():
					t.Fatalf("%s capture did not reach natural EOF before the outer deadline", stream.name)
				}
			}
			if ctx.Err() != nil {
				t.Fatal("capture reached EOF after the outer deadline")
			}
			checkpointFile, err := os.Open(checkpointPath)
			if err != nil {
				t.Fatalf("open saved startup checkpoints: error_type=%T", err)
			}
			checkpointData, readErr := io.ReadAll(io.LimitReader(checkpointFile, 2049))
			closeErr := checkpointFile.Close()
			if readErr != nil || closeErr != nil || len(checkpointData) > 2048 {
				t.Fatalf("read bounded startup checkpoints: read_error_type=%T close_error_type=%T bytes=%d", readErr, closeErr, len(checkpointData))
			}
			checkpointPattern := regexp.MustCompile(`\Abefore-shell-start supervisor_pid=([0-9]+) utc=(\S+)\n` +
				`after-shell-start shell_pid=([0-9]+) process_start_ns=([0-9]+) supervisor_pid=([0-9]+) utc=(\S+)\n` +
				`after-shell-exit supervisor_pid=([0-9]+) utc=(\S+)\n\z`)
			checkpointFields := checkpointPattern.FindSubmatch(checkpointData)
			if len(checkpointFields) != 9 {
				t.Fatal("saved startup checkpoints lack the exact phase sequence")
			}
			for _, index := range []int{1, 5, 7} {
				if string(checkpointFields[index]) != strconv.Itoa(process.Process.Pid) {
					t.Fatal("saved startup checkpoint identifies the wrong supervisor")
				}
			}
			childPID, err := strconv.ParseUint(string(checkpointFields[3]), 10, 32)
			if err != nil || childPID == 0 || childPID == uint64(process.Process.Pid) || control.name == "timeout" && childPID != uint64(pids[0]) {
				t.Fatal("saved startup checkpoint identifies an invalid child")
			}
			if startNS, err := strconv.ParseInt(string(checkpointFields[4]), 10, 64); err != nil || startNS < 0 {
				t.Fatal("saved startup checkpoint contains an invalid Start duration")
			}
			for _, index := range []int{2, 6, 8} {
				stamp := string(checkpointFields[index])
				if parsed, err := time.Parse(time.RFC3339Nano, stamp); err != nil || parsed.IsZero() || !strings.HasSuffix(stamp, "Z") {
					t.Fatal("saved startup checkpoint contains an invalid UTC timestamp")
				}
			}
			t.Logf("saved shell startup checkpoints: %s", checkpointData)
			if !bytes.Equal(stderr.Bytes(), stderrBytes) {
				t.Fatal("process helper changed the separate stderr bytes")
			}
			if control.name != "timeout" {
				if !bytes.Equal(stdout.Bytes(), stdoutBytes) {
					t.Fatal("process helper changed the separate stdout bytes")
				}
				return
			}
			if !bytes.HasPrefix(stdout.Bytes(), stdoutBytes) {
				t.Fatal("timeout lost or changed partial stdout bytes")
			}
			diagnostic := stdout.Bytes()[len(stdoutBytes):]
			for _, private := range []string{helper, work, "synthetic-private-directory", "synthetic-private-argument", privateEnvironment, "synthetic-private-stdout", "synthetic-private-stderr"} {
				if bytes.Contains(diagnostic, []byte(private)) {
					t.Fatal("timeout diagnostic exposed private control data")
				}
			}
			pattern := regexp.MustCompile(`(?m)^\s+shell_file_windows_test\.go:[0-9]+: native shell timed out: context deadline exceeded; timeout=100ms startup=(\S+) elapsed=(\S+) process=true process_state=true pid=([0-9]+) exit=(-?[0-9]+) user_cpu=(\S+) system_cpu=(\S+) error_type=\*exec\.ExitError$`)
			fields := pattern.FindSubmatch(diagnostic)
			if len(diagnostic) > 2048 || len(fields) != 7 || string(fields[3]) != strconv.Itoa(pids[0]) {
				t.Fatal("timeout diagnostic lacks bounded process metadata")
			}
			for _, index := range []int{1, 2, 5, 6} {
				if duration, err := time.ParseDuration(string(fields[index])); err != nil || duration < 0 {
					t.Fatal("timeout diagnostic contains an invalid duration")
				}
			}
			if elapsed, _ := time.ParseDuration(string(fields[2])); elapsed < windowsShellDiagnosticTimeout || elapsed >= 20*time.Second || time.Since(signaled) < windowsShellDiagnosticTimeout {
				t.Fatal("timeout diagnostic does not match the inner deadline")
			}
			t.Logf("process helper failed before outer cancellation; elapsed=%s signal_to_exit=%s", time.Since(started), time.Since(signaled))
		})
	}
}

// Native transport controls expose shell transformations and ready stdin.
func TestShellFileWindowsTransportHelper(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) < 2 || args[0] != "prefilled-input" && args[0] != "prefilled-stdin" && len(args) != 2 {
		t.Fatal("invalid transport helper arguments")
	}
	var err error
	switch args[0] {
	case "prefilled-input", "prefilled-stdin":
		var data []byte
		commandArgs := args[1:]
		if args[0] == "prefilled-input" {
			if len(args) < 3 {
				t.Fatal("missing prefilled-input command")
			}
			data, err = os.ReadFile(args[1])
			commandArgs = args[2:]
		} else {
			data, err = io.ReadAll(os.Stdin)
		}
		if err != nil {
			break
		}
		reader, writer, pipeErr := os.Pipe()
		if pipeErr != nil {
			t.Fatal(pipeErr)
		}
		defer reader.Close()
		defer writer.Close()
		// These small fixtures must be ready before the CLI probes stdin.
		if n, writeErr := writer.Write(data); writeErr != nil || n != len(data) {
			t.Fatalf("prefill stdin: wrote %d of %d bytes: %v", n, len(data), writeErr)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close prefilled stdin writer: %v", err)
		}
		child := exec.Command(commandArgs[0], commandArgs[1:]...)
		child.Stdin, child.Stdout, child.Stderr = reader, os.Stdout, os.Stderr
		childErr := child.Run()
		// os.Exit below bypasses deferred cleanup.
		if err := reader.Close(); err != nil {
			t.Fatalf("close prefilled stdin reader: %v", err)
		}
		if exit, ok := childErr.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		if childErr != nil {
			t.Fatal(childErr)
		}
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

type windowsConsoleResult struct {
	Started    bool
	EventSent  bool
	Event      uint32
	ChildExit  *int
	TimedOut   bool
	ForcedKill bool
}

// Publish only a complete receipt. A helper failure must not resemble CLI cancellation.
func writeWindowsConsoleResult(t *testing.T, result windowsConsoleResult) {
	t.Helper()
	path := os.Getenv("OPENAI_CLI_TEST_CONSOLE_RESULT")
	if path == "" {
		return
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("console result path must be fresh: %v", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(temporary)
	written, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil || written != len(data) {
		t.Fatalf("write console result: bytes=%d/%d write=%v close=%v", written, len(data), writeErr, closeErr)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
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
	observation := windowsConsoleResult{Started: true}
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
	helperCode := -1
	select {
	case result = <-done:
	case event := <-events:
		if event > 1 {
			observation.ForcedKill = true
			child.Process.Kill()
			result = <-done
			helperCode = 125
			break
		}
		if err := windows.GenerateConsoleCtrlEvent(uint32(event), 0); err != nil {
			child.Process.Kill()
			<-done
			t.Fatal(err)
		}
		observation.Event, observation.EventSent = uint32(event), true
		result = <-done
	}
	code := 0
	if result != nil {
		if exit, ok := result.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(result)
		}
	}
	observation.ChildExit = &code
	observation.TimedOut = ctx.Err() != nil
	observation.ForcedKill = observation.ForcedKill || observation.TimedOut
	writeWindowsConsoleResult(t, observation)
	if os.Getenv("OPENAI_CLI_TEST_CONSOLE_ACK") == "1" && !waitWindowsConsoleAcknowledgement(ctx) {
		os.Exit(126) // A failed acknowledgement cannot resemble CLI cancellation.
	}
	if helperCode >= 0 {
		os.Exit(helperCode)
	}
	os.Exit(code)
}

// Keep the Job Object open while a caller checks cleanup after the CLI exits.
// Existing callers omit this opt-in and retain their single-byte input protocol.
func waitWindowsConsoleAcknowledgement(ctx context.Context) bool {
	acknowledged := make(chan bool, 1)
	go func() {
		var ack [1]byte
		_, err := io.ReadFull(os.Stdin, ack[:])
		acknowledged <- err == nil && ack[0] == 1
	}()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case accepted := <-acknowledged:
		return accepted && ctx.Err() == nil
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}
