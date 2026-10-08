package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainBatchesWaitRejectsMalformedJSON(t *testing.T) {
	valid := batchesWorkflowResponse("completed", batchesWorkflowCounts)
	for name, response := range map[string]string{
		"truncated": valid[:len(valid)-1],
		"trailing":  valid + " TRAILING",
		"invalid":   "{broken",
	} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, response)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "batches", "retrieve", "batch_synthetic", "--wait", "--format", "json")
			if got.code != 1 || got.stdout != "" || !json.Valid([]byte(got.stderr)) || requests.Load() != 1 {
				t.Fatalf("malformed response was accepted: result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainBatchesBlockedStdoutCancellation(t *testing.T) {
	for _, operation := range []string{"wait_interrupt", "wait_timeout", "download_interrupt", "receipt_interrupt"} {
		t.Run(operation, func(t *testing.T) {
			interrupt := operation != "wait_timeout"
			if interrupt && runtime.GOOS == "windows" {
				t.Skip("os.Process.Signal cannot send os.Interrupt on Windows; timeout remains tested")
			}
			const payloadSize = 4 << 20
			large := strings.Repeat("x", payloadSize)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("local cancellation mutated remote work: %s %s", r.Method, r.URL)
				}
				if r.URL.Path == "/files/file_output/content" {
					w.Header().Set("Content-Type", "application/jsonl")
					if operation == "receipt_interrupt" {
						io.WriteString(w, batchesWorkflowContent)
					} else {
						io.WriteString(w, large)
					}
					return
				}
				if r.URL.Path != "/batches/batch_synthetic" {
					t.Errorf("unexpected path: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				response := batchesWorkflowResponse("completed", batchesWorkflowCounts)
				if operation == "receipt_interrupt" {
					response = strings.Replace(response, `"id":"batch_synthetic"`, `"id":"`+large+`"`, 1)
				} else if strings.HasPrefix(operation, "wait_") {
					response = strings.TrimSuffix(response, "}") + `,"padding":"` + large + `"}`
				}
				fmt.Fprint(w, response)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			args := []string{"batches", "retrieve", "batch_synthetic", "--wait", "--format", "json"}
			wantCode, wantRequests := 130, int32(1)
			output := ""
			if operation == "wait_timeout" {
				args = append(args, "--wait-timeout", "500ms")
				wantCode = 124
			} else if operation == "download_interrupt" || operation == "receipt_interrupt" {
				output = "-"
				if operation == "receipt_interrupt" {
					output = filepath.Join(t.TempDir(), "results.jsonl")
				}
				args = []string{"batches", "download", "batch_synthetic", "--output", output, "--format", "json"}
				wantRequests = 2
			}
			child := batchesWorkflowProcess(t, ctx, server, args...)
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdout.Close()
			var stderr bytes.Buffer
			child.Stderr = &stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			// Reading a prefix proves that response parsing and writer startup finished.
			// Leave the rest undrained: cancellation must not depend on the consumer.
			var prefix [32]byte
			if _, err := io.ReadFull(stdout, prefix[:]); err != nil {
				_ = child.Process.Kill()
				_ = child.Wait()
				t.Fatalf("output never started: %v stderr=%q", err, stderr.String())
			}
			if interrupt {
				if err := child.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}
			completed := make(chan error, 1)
			go func() { completed <- child.Wait() }()
			select {
			case err := <-completed:
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != wantCode || !json.Valid(stderr.Bytes()) {
					t.Fatalf("blocked output exit: err=%v stderr=%q; want %d", err, stderr.String(), wantCode)
				}
			case <-time.After(2 * time.Second):
				_ = child.Process.Kill()
				<-completed
				t.Fatal("local cancellation remained blocked on undrained stdout")
			}
			if requests.Load() != wantRequests {
				t.Fatalf("unexpected remote requests: %d; want %d", requests.Load(), wantRequests)
			}
			if operation == "receipt_interrupt" {
				content, err := os.ReadFile(output)
				if err != nil || string(content) != batchesWorkflowContent {
					t.Fatalf("cancellation damaged the published file: bytes=%q error=%v", content, err)
				}
				pending, err := filepath.Glob(filepath.Join(filepath.Dir(output), ".openai-batch-*"))
				if err != nil || len(pending) != 0 {
					t.Fatalf("download staging files remain: %v (%v)", pending, err)
				}
			}
		})
	}
}

// Keep stdout undrained after a small prefix while draining stderr independently.
// The VINTR case keeps a controlling terminal while draining diagnostics separately.
const batchesBlockedTerminal = `
import errno, fcntl, json, os, pty, select, signal, struct, subprocess, sys, termios, threading, time
mode, argv = sys.argv[1], sys.argv[2:]
if mode == "vintr":
    error_read, error_write = os.pipe()
    pid, master = pty.fork()
    if pid == 0:
        os.close(error_read)
        os.dup2(error_write, 2)
        os.close(error_write)
        os.execve(argv[0], argv, os.environ)
    os.close(error_write)
    errors = []
    def drain_errors():
        with os.fdopen(error_read, "rb") as source:
            errors.append(source.read())
    reader = threading.Thread(target=drain_errors, daemon=True)
    reader.start()
    settings = termios.tcgetattr(master)
    assert settings[3] & termios.ISIG
    assert settings[6][termios.VINTR] == b"\x03"
    ready, _, _ = select.select([master], [], [], 5)
    assert ready, "terminal output never started"
    prefix = os.read(master, 32)
    os.write(master, b"\x03")
    deadline, status = time.monotonic() + 2, None
    while time.monotonic() < deadline:
        child, candidate = os.waitpid(pid, os.WNOHANG)
        if child:
            status = candidate
            break
        time.sleep(.01)
    blocked = status is None
    if blocked:
        os.kill(pid, signal.SIGKILL)
        _, status = os.waitpid(pid, 0)
    os.close(master)
    reader.join(2)
    print(json.dumps({"code": os.waitstatus_to_exitcode(status), "blocked": blocked, "prefix": prefix.decode(), "stderr": b"".join(errors).decode()}))
else:
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
    child = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=slave, stderr=subprocess.PIPE)
    os.close(slave)
    errors = []
    reader = threading.Thread(target=lambda: errors.append(child.stderr.read()), daemon=True)
    reader.start()
    try:
        ready, _, _ = select.select([master], [], [], 5)
        assert ready, "terminal output never started"
        prefix = os.read(master, 32)
        if mode != "timeout":
            child.send_signal(signal.SIGINT)
        try:
            child.wait(timeout=2)
            blocked = False
        except subprocess.TimeoutExpired:
            blocked = True
            child.kill()
            child.wait(timeout=2)
        reader.join(2)
        print(json.dumps({"code": child.returncode, "blocked": blocked, "prefix": prefix.decode(), "stderr": b"".join(errors).decode()}))
    finally:
        if child.poll() is None:
            child.kill()
            child.wait()
        os.close(master)
`

func TestMainBatchesBlockedTerminalCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PTYs do not establish native Windows terminal behavior")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		if runtime.GOOS == "darwin" {
			t.Fatal("native macOS batch cancellation requires python3:", err)
		}
		t.Skip("native terminal cancellation requires python3 for POSIX PTYs")
	}
	for _, operation := range []string{"wait_json", "wait_raw", "wait_raw_string", "wait_timeout", "download", "vintr"} {
		t.Run(operation, func(t *testing.T) {
			large := "\x1b[2J" + strings.Repeat("x", 4<<20)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("terminal cancellation mutated remote work: %s %s", r.Method, r.URL)
				}
				if r.URL.Path == "/files/file_output/content" {
					io.WriteString(w, large)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				padding, _ := json.Marshal(large)
				fmt.Fprint(w, strings.TrimSuffix(batchesWorkflowResponse("completed", batchesWorkflowCounts), "}")+`,"padding":`+string(padding)+`}`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			args := []string{"batches", "retrieve", "batch_synthetic", "--wait", "--format", "json"}
			mode, wantCode, wantRequests := "signal", 130, int32(1)
			switch operation {
			case "wait_raw":
				args = []string{"batches", "retrieve", "batch_synthetic", "--wait", "--format", "raw"}
			case "wait_raw_string":
				args = []string{"batches", "retrieve", "batch_synthetic", "--wait", "--transform", "padding", "--raw-output", "--format-error", "json"}
			case "wait_timeout":
				args = append(args, "--wait-timeout", "500ms")
				mode, wantCode = "timeout", 124
			case "download":
				args = []string{"batches", "download", "batch_synthetic", "--output", "-", "--format", "json"}
				wantRequests = 2
			case "vintr":
				mode = "vintr"
			}
			process := batchesWorkflowProcess(t, ctx, server, args...)
			probe := exec.CommandContext(ctx, python, append([]string{"-c", batchesBlockedTerminal, mode}, process.Args...)...)
			probe.Env = process.Env
			output, err := probe.CombinedOutput()
			if err != nil {
				t.Fatalf("PTY probe failed: %v %s", err, output)
			}
			var got struct {
				Code    int    `json:"code"`
				Blocked bool   `json:"blocked"`
				Prefix  string `json:"prefix"`
				Stderr  string `json:"stderr"`
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("invalid PTY result: %s (%v)", output, err)
			}
			if got.Blocked || got.Code != wantCode || requests.Load() != wantRequests {
				t.Fatalf("terminal cancellation failed: %+v requests=%d; want exit %d requests %d", got, requests.Load(), wantCode, wantRequests)
			}
			if !json.Valid([]byte(got.Stderr)) {
				t.Fatalf("terminal cancellation diagnostic changed: %q", got.Stderr)
			}
			if operation == "wait_raw_string" && strings.Contains(got.Prefix, "\x1b") {
				t.Fatalf("terminal raw-string output lost escaping: %q", got.Prefix)
			}
		})
	}
}

const batchesExplorerDeadlinePTY = `
import errno, fcntl, json, os, pty, select, signal, struct, sys, termios, time
mode, argv = sys.argv[1], sys.argv[2:]
pid, master = pty.fork()
if pid == 0:
    os.execve(argv[0], argv, os.environ)
fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
status, sent, output_bytes = None, False, 0
deadline = time.monotonic() + 3
try:
    while time.monotonic() < deadline:
        ready, _, _ = select.select([master], [], [], .02)
        if ready:
            try:
                data = os.read(master, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            output_bytes += len(data)
            if data and mode == "quit" and not sent:
                os.write(master, b"q")
                sent = True
        child, candidate = os.waitpid(pid, os.WNOHANG)
        if child:
            status = candidate
            break
    blocked = status is None
    if blocked:
        os.kill(pid, signal.SIGKILL)
        _, status = os.waitpid(pid, 0)
    print(json.dumps({"code": os.waitstatus_to_exitcode(status), "blocked": blocked, "output_bytes": output_bytes}))
finally:
    os.close(master)
`

func TestMainBatchesExplorerWaitDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PTYs do not establish native Windows explorer behavior")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		if runtime.GOOS == "darwin" {
			t.Fatal("native macOS batch explorer requires python3:", err)
		}
		t.Skip("native explorer requires python3 for POSIX PTYs")
	}
	for _, mode := range []string{"quit", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" {
					t.Errorf("unexpected explorer request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			args := []string{"batches", "retrieve", "batch_synthetic", "--wait", "--format", "explore"}
			wantCode := 0
			if mode == "timeout" {
				args = append(args, "--wait-timeout", "500ms")
				wantCode = 124
			}
			process := batchesWorkflowProcess(t, ctx, server, args...)
			probe := exec.CommandContext(ctx, python, append([]string{"-c", batchesExplorerDeadlinePTY, mode}, process.Args...)...)
			probe.Env = append(process.Env, "TERM=xterm-256color")
			output, err := probe.CombinedOutput()
			if err != nil {
				t.Fatalf("explorer PTY probe failed: %v %s", err, output)
			}
			var got struct {
				Code        int  `json:"code"`
				Blocked     bool `json:"blocked"`
				OutputBytes int  `json:"output_bytes"`
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("invalid explorer result: %s (%v)", output, err)
			}
			if got.Blocked || got.Code != wantCode || got.OutputBytes == 0 || requests.Load() != 1 {
				t.Fatalf("explorer deadline or quit failed: %+v requests=%d; want exit %d", got, requests.Load(), wantCode)
			}
		})
	}
}

// Saturate one terminal shared by stdout and stderr before the helper starts.
// A replaced executable supplies a deterministic no-acknowledgement startup phase.
const batchesInterruptedStartupPTY = `
import json, os, pathlib, pty, shlex, shutil, signal, subprocess, sys, time
mode, directory, argv = sys.argv[1], pathlib.Path(sys.argv[2]), sys.argv[3:]
if mode != "immediate_deadline":
    executable = directory / "openai-copy"
    shutil.copyfile(argv[0], executable)
    executable.chmod(0o700)
    argv[0] = str(executable)
master, slave = pty.openpty()
os.set_blocking(slave, False)
filled = 0
for _ in range(3):
    for value in [b"x" * 1024, b"x"]:
        while True:
            try:
                filled += os.write(slave, value)
            except BlockingIOError:
                break
    time.sleep(.02)
os.set_blocking(slave, True)
child = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=slave, stderr=slave)
worker = None
try:
    if mode != "immediate_deadline":
        limit = time.monotonic() + 3
        while not (directory / "requested").exists() and time.monotonic() < limit:
            time.sleep(.005)
        assert (directory / "requested").exists(), "batch request never started"
        executable.unlink()
        pidfile = directory / "worker.pid"
        executable.write_text('#!/bin/sh\nprintf "%s" "$$" > ' + shlex.quote(str(pidfile)) + '\nexec /bin/sleep 20\n')
        executable.chmod(0o700)
        (directory / "release").touch()
        limit = time.monotonic() + 3
        while time.monotonic() < limit and child.poll() is None:
            if pidfile.exists() and pidfile.read_text().isdigit():
                worker = int(pidfile.read_text())
                break
            time.sleep(.005)
        assert worker is not None, "replacement helper never started"
        if mode == "startup_interrupt":
            child.send_signal(signal.SIGINT)
    try:
        child.wait(timeout=2.5)
        blocked = False
    except subprocess.TimeoutExpired:
        blocked = True
        child.kill()
        child.wait(timeout=2)
    worker_exists = False
    if worker is not None:
        try:
            os.kill(worker, 0)
            worker_exists = True
        except ProcessLookupError:
            pass
    os.set_blocking(master, False)
    remaining = 0
    while True:
        try:
            data = os.read(master, 65536)
        except (BlockingIOError, OSError):
            break
        if not data:
            break
        remaining += len(data)
    print(json.dumps({"code": child.returncode, "blocked": blocked, "filled": filled, "remaining": remaining, "worker_exists": worker_exists}))
finally:
    if child.poll() is None:
        child.kill()
        child.wait()
    os.close(slave)
    os.close(master)
`

func TestMainBatchesInterruptedOutputStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PTYs and executable replacement do not establish native Windows startup behavior")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		if runtime.GOOS == "darwin" {
			t.Fatal("native macOS batch startup requires python3:", err)
		}
		t.Skip("native startup regression requires python3 for POSIX PTYs")
	}
	for _, mode := range []string{"immediate_deadline", "startup_interrupt", "startup_deadline"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" {
					t.Errorf("unexpected startup request: %s %s", r.Method, r.URL)
				}
				if err := os.WriteFile(filepath.Join(directory, "requested"), nil, 0600); err != nil {
					t.Error(err)
					return
				}
				ticker := time.NewTicker(5 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(filepath.Join(directory, "release")); err == nil {
						break
					}
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			args := []string{"batches", "retrieve", "batch_synthetic", "--wait"}
			wantCode, wantRequests := 124, int32(1)
			switch mode {
			case "immediate_deadline":
				args = append(args, "--wait-timeout", "1ns")
				wantRequests = 0
			case "startup_interrupt":
				args = append(args, "--format", "json")
				wantCode = 130
			case "startup_deadline":
				args = append(args, "--format", "json", "--wait-timeout", "1s")
			}
			process := batchesWorkflowProcess(t, ctx, server, args...)
			probe := exec.CommandContext(ctx, python, append([]string{"-c", batchesInterruptedStartupPTY, mode, directory}, process.Args...)...)
			probe.Env = process.Env
			output, err := probe.CombinedOutput()
			if err != nil {
				t.Fatalf("startup PTY probe failed: %v %s", err, output)
			}
			var got struct {
				Code         int  `json:"code"`
				Blocked      bool `json:"blocked"`
				Filled       int  `json:"filled"`
				Remaining    int  `json:"remaining"`
				WorkerExists bool `json:"worker_exists"`
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("invalid startup result: %s (%v)", output, err)
			}
			if got.Blocked || got.Code != wantCode || got.Filled == 0 || got.Remaining != got.Filled || got.WorkerExists || requests.Load() != wantRequests {
				t.Fatalf("startup cancellation lost destination identity: %+v requests=%d; want exit %d requests %d", got, requests.Load(), wantCode, wantRequests)
			}
		})
	}
}

const batchesStoppedWorkerPTY = `
import errno, json, os, pathlib, pty, selectors, signal, subprocess, sys, time
directory, pgrep, argv = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3:]
masters, slaves = zip(pty.openpty(), pty.openpty())
child = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=slaves[0], stderr=slaves[1], start_new_session=True)
for slave in slaves:
    os.close(slave)
try:
    limit = time.monotonic() + 5
    while not (directory / "requested").exists() and time.monotonic() < limit:
        time.sleep(.005)
    assert (directory / "requested").exists(), "second batch request never started"
    found = subprocess.run([pgrep, "-P", str(child.pid)], capture_output=True, text=True)
    assert found.returncode == 0, found.stderr
    workers = [int(value) for value in found.stdout.split()]
    assert len(workers) == 1, workers
    worker = workers[0]
    os.kill(worker, signal.SIGSTOP)
    (directory / "release").touch()
    selector = selectors.DefaultSelector()
    buffers = [bytearray(), bytearray()]
    for index, master in enumerate(masters):
        selector.register(master, selectors.EVENT_READ, index)
    limit = time.monotonic() + 5
    while selector.get_map():
        assert time.monotonic() < limit, "API error did not finish"
        for key, _ in selector.select(.05):
            try:
                data = os.read(key.fd, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            if data:
                buffers[key.data].extend(data)
            else:
                selector.unregister(key.fd)
    child.wait(timeout=2)
    selector.close()
    try:
        os.kill(worker, 0)
        worker_exists = True
    except ProcessLookupError:
        worker_exists = False
    print(json.dumps({"code": child.returncode, "stdout": buffers[0].decode(), "stderr": buffers[1].decode(), "worker_exists": worker_exists}))
finally:
    if child.poll() is None:
        os.killpg(child.pid, signal.SIGKILL)
        child.wait()
    for master in masters:
        os.close(master)
`

func TestMainBatchesProgressWorkerPreservesAPIExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX worker suspension does not establish native Windows error behavior")
	}
	python, pythonErr := exec.LookPath("python3")
	pgrep, pgrepErr := exec.LookPath("pgrep")
	if pythonErr != nil || pgrepErr != nil {
		if runtime.GOOS == "darwin" {
			t.Fatalf("native macOS worker regression requires python3 and pgrep: %v %v", pythonErr, pgrepErr)
		}
		t.Skip("native worker regression requires python3 and pgrep")
	}
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			directory := t.TempDir()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" {
					t.Errorf("unexpected progress request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if requests.Add(1) == 1 {
					fmt.Fprint(w, batchesWorkflowResponse("in_progress", `{"total":3,"completed":1,"failed":0}`))
					return
				}
				if err := os.WriteFile(filepath.Join(directory, "requested"), nil, 0600); err != nil {
					t.Error(err)
					return
				}
				ticker := time.NewTicker(5 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(filepath.Join(directory, "release")); err == nil {
						break
					}
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
				}
				w.Header().Set("x-should-retry", "false")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"message":"synthetic failure","type":"api_error","code":"synthetic_test"}}`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			process := batchesWorkflowProcess(t, ctx, server, "batches", "retrieve", "batch_synthetic", "--wait", "--poll-interval", "1ms")
			probe := exec.CommandContext(ctx, python, append([]string{"-c", batchesStoppedWorkerPTY, directory, pgrep}, process.Args...)...)
			probe.Env = process.Env
			output, err := probe.CombinedOutput()
			if err != nil {
				t.Fatalf("worker PTY probe failed: %v %s", err, output)
			}
			var got struct {
				Code         int    `json:"code"`
				Stdout       string `json:"stdout"`
				Stderr       string `json:"stderr"`
				WorkerExists bool   `json:"worker_exists"`
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("invalid worker result: %s (%v)", output, err)
			}
			if got.Code != 1 || got.Stdout != "" || !strings.Contains(got.Stderr, fmt.Sprint(status)) || got.WorkerExists || requests.Load() != 2 {
				t.Fatalf("private worker exit replaced the API error: %+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainBatchesDownloadRejectsNullResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" {
			t.Errorf("null batch response triggered an unexpected request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "null")
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "batches", "download", "batch_synthetic", "--output", "-", "--format", "json")
	if got.code != 1 || got.stdout != "" || !json.Valid([]byte(got.stderr)) || requests.Load() != 1 {
		t.Fatalf("null batch response panicked or reached Files content: result=%+v requests=%d", got, requests.Load())
	}
}
