//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestMainAdminSetupTerminal(t *testing.T) {
	if adminSetupUnavailable(t) {
		t.Skip("interactive input is unavailable on this platform")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for the Unix Admin setup PTY cases")
	}
	const key = "sk-admin-SYNTHETIC-quiet-test"
	for _, test := range []struct {
		name         string
		flags        []string
		status, exit int
		cancel       bool
		progress     bool
		queued       string
	}{
		{"default success", nil, http.StatusOK, 0, false, true, ""},
		{"quiet success", []string{"--quiet"}, http.StatusOK, 0, false, false, ""},
		{"quiet overrides verbose", []string{"--quiet", "--verbose"}, http.StatusOK, 0, false, false, ""},
		{"quiet forbidden", []string{"--quiet"}, http.StatusForbidden, 1, false, false, ""},
		{"quiet cancellation", []string{"--quiet"}, http.StatusOK, 130, true, false, ""},
		{"prequeued complete line", nil, http.StatusOK, 0, false, true, "line"},
		{"prequeued partial prefix", nil, http.StatusOK, 0, false, true, "prefix"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/organization/projects" ||
					r.URL.RawQuery != "limit=1" || r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("verification changed its request or synthetic credential")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				body := `{"object":"list","data":[],"has_more":true}`
				if test.status == http.StatusForbidden {
					body = `{"error":{"message":"synthetic-private-quiet-body ` + key + `"}}`
				}
				if _, err := io.WriteString(w, body); err != nil {
					t.Error("synthetic response write failed")
				}
			}))
			defer server.Close()
			work := t.TempDir()
			args := append([]string{"openai"}, test.flags...)
			args = append(args, "setup", "admin")
			got := runAdminSetupTerminalPTY(t, python, work, server.URL+"/v1", args, test.cancel, test.queued)
			wantRequests := int32(1)
			if test.cancel {
				wantRequests = 0
			}
			require.Equal(t, wantRequests, requests.Load())
			require.Equal(t, test.exit, got.code)
			for _, output := range []string{got.stdout, got.stderr, ansi.Strip(got.stdout), ansi.Strip(got.stderr)} {
				// Boolean assertions cannot include a reflected credential in failure output.
				require.False(t, strings.Contains(output, "sk-admin-SYNTHETIC"), "entered key appeared in output")
				require.False(t, strings.Contains(output, "synthetic-private-quiet-body"), "backend body appeared in output")
			}
			stdout, stderr := ansi.Strip(got.stdout), ansi.Strip(got.stderr)
			require.Equal(t, 1, strings.Count(stderr, "Admin API key (hidden): "))
			require.Contains(t, stderr, "Destination:")
			require.Contains(t, stderr, "The key stays in this process.")
			require.Equal(t, test.progress, strings.Contains(stderr, "Verifying admin access..."))
			require.NotContains(t, stdout, "Verifying admin access...")
			require.NotContains(t, stderr, "Command result:", "quiet must override verbose")
			if test.exit == 0 {
				require.Equal(t, "Admin access verified. The project-list request succeeded.\nNo key was saved. Later commands still need authentication.\n", stdout)
			} else {
				require.Empty(t, stdout)
				if test.cancel {
					require.Contains(t, stderr, "Admin setup canceled. No key was saved.")
				} else {
					require.Contains(t, stderr, "The server denied admin access (403). Ask an organization owner to check your access.")
				}
			}
			entries, err := os.ReadDir(work)
			require.NoError(t, err)
			require.Empty(t, entries, "interactive verification must not persist credentials or configuration")
		})
	}
}

// Drain subprocess pipes even after the small control-output allowance is exceeded.
type adminSetupTerminalCapture struct {
	buffer   bytes.Buffer
	overflow bool
}

func (capture *adminSetupTerminalCapture) Write(data []byte) (int, error) {
	const limit = 512 << 10
	original := len(data)
	if remaining := limit - capture.buffer.Len(); len(data) > remaining {
		capture.overflow = true
		data = data[:remaining]
	}
	_, _ = capture.buffer.Write(data)
	return original, nil
}

func runAdminSetupTerminalPTY(t *testing.T, python, work, endpoint string, args []string, cancelInput bool, queued string) mainDispatchResult {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	input, err := json.Marshal(struct {
		Binary, Work, Endpoint string
		Args                   []string
		Cancel                 bool
		Queued                 string
	}{binary, work, endpoint, args, cancelInput, queued})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, python, "-I", "-B", "-c", adminSetupTerminalPTYScript)
	child.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "LANG=en_US.UTF-8"}
	child.Dir = work
	child.Stdin = bytes.NewReader(input)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
	child.WaitDelay = 4 * time.Second
	var stdout, stderr adminSetupTerminalCapture
	child.Stdout, child.Stderr = &stdout, &stderr
	err = child.Run()
	if child.Process != nil && syscall.Kill(-child.Process.Pid, 0) != syscall.ESRCH {
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
		deadline := time.Now().Add(time.Second)
		for syscall.Kill(-child.Process.Pid, 0) != syscall.ESRCH && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("PTY harness required forced process-group cleanup")
		if syscall.Kill(-child.Process.Pid, 0) != syscall.ESRCH {
			t.Error("owned process group remained after bounded cleanup")
		}
	}
	require.NoError(t, ctx.Err(), "Admin setup PTY harness exceeded its deadline")
	require.NoError(t, err, "Admin setup PTY harness failed")
	require.False(t, stdout.overflow || stderr.overflow, "PTY control capture exceeded its allowance")
	require.Zero(t, stderr.buffer.Len(), "PTY harness emitted an unexpected diagnostic")
	var result struct {
		Code                     int
		Stdout, Stderr, Failure  string
		Hidden, Restored, Reaped bool
		QueuedBefore             int
	}
	require.NoError(t, json.Unmarshal(stdout.buffer.Bytes(), &result), "invalid PTY control result")
	require.Empty(t, result.Failure)
	require.True(t, result.Hidden && result.Restored && result.Reaped, "hidden input or owned cleanup was not established")
	if queued != "" {
		require.Positive(t, result.QueuedBefore, "startup input was not queued before the command started")
	}
	return mainDispatchResult{result.Code, result.Stdout, result.Stderr}
}

const adminSetupTerminalPTYScript = `
import errno, fcntl, json, os, pty, re, selectors, signal, struct, subprocess, sys, termios, time

def check(condition, label):
    if not condition:
        raise AssertionError(label)

def interrupted(signum, frame):
    raise TimeoutError("harness interrupted")

def queued_bytes(fd):
    return struct.unpack("i", fcntl.ioctl(fd, termios.FIONREAD, struct.pack("i", 0)))[0]

signal.signal(signal.SIGTERM, interrupted)
config = json.load(sys.stdin)
env = {
    "PATH": "/usr/bin:/bin", "HOME": config["Work"], "XDG_CONFIG_HOME": config["Work"],
    "LANG": "en_US.UTF-8", "TERM": "xterm-256color", "NO_COLOR": "1", "GOMAXPROCS": "2",
    "OPENAI_CLI_MAIN_DISPATCH_PROCESS": "1", "OPENAI_BASE_URL": config["Endpoint"],
    "HTTP_PROXY": "http://127.0.0.1:1", "HTTPS_PROXY": "http://127.0.0.1:1",
    "NO_PROXY": "127.0.0.1,localhost,::1",
}
key = b"sk-admin-SYNTHETIC-quiet-test"
ansi = re.compile(rb"\x1b\][^\x07]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]")
selector = selectors.DefaultSelector()
masters, slaves, output = [], [], [bytearray(), bytearray(), bytearray()]
process = None
result = {"Code": -1, "Stdout": "", "Stderr": "", "Failure": "",
          "Hidden": False, "Restored": False, "Reaped": False, "QueuedBefore": 0}

def pump(timeout):
    ready = selector.select(timeout)
    for selected, _ in ready:
        try:
            data = os.read(selected.fd, 65536)
        except OSError as error:
            if error.errno == errno.EAGAIN:
                continue
            if error.errno != errno.EIO:
                raise
            data = b""
        if not data:
            selector.unregister(selected.fd)
        output[selected.data].extend(data)
        check(sum(map(len, output)) <= 65536, "terminal capture exceeded allowance")
    return bool(ready)

try:
    for stream in range(3):
        master, slave = pty.openpty()
        masters.append(master)
        slaves.append(slave)
        os.set_blocking(master, False)
        selector.register(master, selectors.EVENT_READ, stream)
    if config["Queued"]:
        mode = termios.tcgetattr(slaves[0])
        check(mode[3] & termios.ICANON, "startup fixture did not start canonical")
        mode[3] &= ~(termios.ECHO | termios.ECHONL)
        data = b"sk-admin-SYNTHETIC-queued-stale\n"
        if config["Queued"] == "prefix":
            mode[3] &= ~termios.ICANON
            data = b"sk-admin-SYNTHETIC-queued-prefix-"
        else:
            check(config["Queued"] == "line", "unknown startup queue fixture")
        termios.tcsetattr(slaves[0], termios.TCSANOW, mode)
        check(os.write(masters[0], data) == len(data), "startup queue write was incomplete")
        queue_deadline = time.monotonic() + 1
        while queued_bytes(slaves[0]) != len(data):
            check(time.monotonic() < queue_deadline, "startup bytes did not reach the input queue")
            time.sleep(0.005)
        result["QueuedBefore"] = queued_bytes(slaves[0])
    original = [termios.tcgetattr(slave) for slave in slaves]
    if not config["Queued"]:
        check(original[0][3] & termios.ECHO, "input did not start with echo")
    command = [config["Binary"], "-test.run=^TestMainDispatchProcess$", "--", *config["Args"]]
    process = subprocess.Popen(command, cwd=config["Work"], env=env,
                               stdin=slaves[0], stdout=slaves[1], stderr=slaves[2])
    deadline = time.monotonic() + 10
    while process.poll() is None:
        check(time.monotonic() < deadline, "command exceeded deadline")
        pump(0.02)
        if not result["Hidden"] and b"Admin API key (hidden): " in output[2]:
            check(process.poll() is None, "command completed before fresh prompt input")
            mode = termios.tcgetattr(slaves[0])
            check(not mode[3] & (termios.ECHO | termios.ICANON), "prompt preceded hidden input")
            result["Hidden"] = True
            data = b"\x03" if config["Cancel"] else key + b"\r"
            check(os.write(masters[0], data) == len(data), "incomplete input write")
    result["Code"] = process.wait(timeout=1)
    result["Reaped"] = True
    drain_deadline = time.monotonic() + 1
    while pump(0.05):
        check(time.monotonic() < drain_deadline, "terminal output did not drain")
    check(queued_bytes(slaves[0]) == 0, "input remains after command cleanup")
    restored = [termios.tcgetattr(slave) for slave in slaves]
    if sys.platform == "darwin":
        for modes in original + restored:
            modes[3] &= ~termios.PENDIN
    check(original == restored, "terminal settings were not restored")
    result["Restored"] = True
    for capture in output:
        for view in [bytes(capture), ansi.sub(b"", capture)]:
            check(b"sk-admin-SYNTHETIC" not in view, "input privacy failed")
            check(b"synthetic-private-quiet-body" not in view, "response privacy failed")
    check(not output[0], "input unexpectedly echoed")
    result["Stdout"] = output[1].decode("utf-8").replace("\r\n", "\n")
    result["Stderr"] = output[2].decode("utf-8").replace("\r\n", "\n")
except Exception as error:
    result["Failure"] = str(error) if isinstance(error, AssertionError) else type(error).__name__
    result["Stdout"], result["Stderr"] = "", ""
finally:
    if process is not None:
        if process.poll() is None:
            process.kill()
            result["Failure"] = "command required forced cleanup"
        process.wait(timeout=2)
        result["Reaped"] = True
    selector.close()
    for descriptor in masters + slaves:
        os.close(descriptor)
print(json.dumps(result))
`
