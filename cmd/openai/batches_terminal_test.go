package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Separate real PTYs expose channel pollution hidden by ordinary pipe tests.
// Python supplies only the native PTY plumbing; the child executes production main.
const batchesSeparatePTY = `
import errno, fcntl, json, os, pty, selectors, struct, subprocess, sys, termios, time
masters, slaves = zip(pty.openpty(), pty.openpty())
for slave in slaves:
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
assert all(os.isatty(slave) for slave in slaves)
process = subprocess.Popen(sys.argv[1:], stdin=subprocess.DEVNULL, stdout=slaves[0], stderr=slaves[1])
for slave in slaves:
    os.close(slave)
selector = selectors.DefaultSelector()
for index, master in enumerate(masters):
    selector.register(master, selectors.EVENT_READ, index)
channels = [bytearray(), bytearray()]
deadline = time.monotonic() + 10
try:
    while selector.get_map():
        if time.monotonic() > deadline:
            raise TimeoutError("batch PTY command did not finish")
        for key, _ in selector.select(.1):
            try:
                data = os.read(key.fd, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            if data:
                channels[key.data].extend(data)
            else:
                selector.unregister(key.fd)
    code = process.wait(timeout=2)
    print(json.dumps({"code": code, "stdout": channels[0].decode(), "stderr": channels[1].decode()}))
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    selector.close()
    for master in masters:
        os.close(master)
`

func TestMainBatchesWaitTerminalErrorChannels(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Python POSIX PTYs do not provide native Windows terminal coverage")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		if runtime.GOOS == "darwin" {
			t.Fatal("native macOS batch terminal regression requires python3:", err)
		}
		t.Skip("batch terminal regression requires python3 for native POSIX PTYs")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, dataFormat, errorFormat, transform string
		progress                                 bool
	}{
		{"human", "text", "text", "", true},
		{"json", "text", "json", "", false},
		{"jsonl", "text", "jsonl", "", false},
		{"yaml", "text", "yaml", "", false},
		{"raw", "text", "raw", "", false},
		{"pretty", "text", "pretty", "", false},
		{"json_extraction", "text", "json", "message", false},
		{"text_extraction", "text", "text", "message", false},
		{"machine_data", "json", "text", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if requests.Add(1) == 1 {
					fmt.Fprint(w, batchesWorkflowResponse("in_progress", `{"total":3,"completed":1,"failed":0}`))
					return
				}
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error":{"message":"synthetic denial","type":"permission_error","code":"synthetic_permission"}}`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			args := []string{"-c", batchesSeparatePTY, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai",
				"--format", tc.dataFormat, "--format-error", tc.errorFormat}
			if tc.transform != "" {
				args = append(args, "--transform-error", tc.transform)
			}
			args = append(args, "batches", "retrieve", "batch_synthetic", "--wait", "--poll-interval", "1ms")
			child := exec.CommandContext(ctx, python, args...)
			child.Env = []string{"HOME=" + t.TempDir(), "TERM=xterm-256color", "FORCE_COLOR=0", "NO_COLOR=1", "GOMAXPROCS=2",
				"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-batches-terminal", "OPENAI_BASE_URL=" + server.URL}
			output, err := child.CombinedOutput()
			if err != nil {
				t.Fatalf("PTY helper failed: %v; %s", err, output)
			}
			var got struct {
				Code           int
				Stdout, Stderr string
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("invalid PTY capture: %v; %s", err, output)
			}
			if got.Code != 1 || got.Stdout != "" || requests.Load() != 2 {
				t.Fatalf("result=%+v requests=%d; want exit 1, empty stdout, and two GET requests", got, requests.Load())
			}
			if strings.Contains(got.Stderr, "Processing: 1 of 3 requests finished.") != tc.progress {
				t.Fatalf("progress polluted the selected error channel or disappeared: %q", got.Stderr)
			}
			if tc.transform != "" {
				want := "synthetic denial"
				if tc.errorFormat == "json" {
					want = `"synthetic denial"`
				}
				if strings.TrimSpace(got.Stderr) != want {
					t.Fatalf("error extraction changed: %q; want %q", got.Stderr, want)
				}
			} else if tc.errorFormat == "pretty" {
				if !strings.HasPrefix(strings.TrimSpace(got.Stderr), "Error\r\n╭") || !strings.Contains(got.Stderr, "message: synthetic denial") || !strings.Contains(got.Stderr, "code: synthetic_permission") {
					t.Fatalf("pretty error presentation changed: %q", got.Stderr)
				}
			} else if tc.errorFormat != "text" {
				payload := decodeMainErrorObject(t, tc.errorFormat, got.Stderr)
				if payload["code"] != "synthetic_permission" || payload["message"] != "synthetic denial" {
					t.Fatalf("structured error changed: %v", payload)
				}
			}
		})
	}
}
