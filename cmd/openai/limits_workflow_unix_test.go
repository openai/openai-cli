//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-cli/internal/terminal"
)

type limitsBorrowedOutputProbe struct {
	fail   bool
	writes atomic.Int32
	ready  atomic.Bool
	failed atomic.Bool
}

func (w *limitsBorrowedOutputProbe) Write(data []byte) (int, error) {
	w.writes.Add(1)
	if w.fail && w.ready.Load() && !w.failed.Swap(true) {
		return 0, errors.New("synthetic-private-borrowed-output")
	}
	n, err := os.Stdout.Write(data)
	if err == nil && n == len(data) && bytes.Contains(data, []byte("Website controls and source errors: ?")) {
		w.ready.Store(true)
	}
	return n, err
}

// Exercise production startup with the supported embedding writer installed.
func TestMainLimitsBorrowedWriterProcess(t *testing.T) {
	mode := os.Getenv("OPENAI_CLI_LIMITS_BORROWED_PROCESS")
	if mode == "" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing subprocess argument separator")
	}
	sink := &limitsBorrowedOutputProbe{fail: mode == "write-failure"}
	borrowed := terminal.BorrowedWriter{Writer: sink, File: os.Stdout}
	commands.Command.Writer, commands.Command.Reader = borrowed, os.Stdin
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	code := app.Run(ctx, os.Args[separator+1:])
	if err := ctx.Err(); err != nil {
		t.Fatal("workspace required fallback cancellation", err)
	}
	if sink.writes.Load() == 0 || sink.fail && !sink.failed.Load() {
		t.Fatal("workspace bypassed the borrowed output sink")
	}
	if _, err := os.Stdout.Stat(); err != nil {
		t.Fatal("workspace closed the caller's terminal", err)
	}
	if _, err := fmt.Fprintln(os.Stderr, "borrowed-terminal-verified"); err != nil {
		t.Fatal(err)
	}
	os.Exit(code)
}

func TestMainLimitsWorkflowBorrowedTerminalWriter(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for native Unix borrowed-terminal checks")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, mode string
		project    bool
		exit       int
	}{
		{"organization", "quit", false, 0},
		{"project", "quit", true, 0},
		{"interrupt", "interrupt", false, 130},
		{"output failure", "write-failure", false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
				return limitsWorkflowSuccess(t, test.project, r)
			})
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			args := []string{"-I", "-B", "-c", limitsBorrowedTerminalPTY, binary,
				"-test.run=^TestMainLimitsBorrowedWriterProcess$", "--", "openai"}
			args = append(args, limitsWorkflowArgs(test.project, false)...)
			child := exec.CommandContext(ctx, python, args...)
			child.Env = spendRecoverySetEnvironment(spendRecoveryEnvironment(t, server.URL), "CI", "")
			child.Env = append(child.Env, "PATH=/usr/bin:/bin", "TERM=xterm-256color", "GOMAXPROCS=2",
				"OPENAI_CLI_LIMITS_BORROWED_PROCESS="+test.mode)
			child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
			child.WaitDelay = 2 * time.Second
			output, err := child.CombinedOutput()
			if err != nil || ctx.Err() != nil {
				t.Fatalf("borrowed terminal harness failed: error=%v context=%v output=%s", err, ctx.Err(), output)
			}
			var got struct {
				Code           int
				Stdout, Stderr string
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("invalid borrowed terminal capture: %v; %s", err, output)
			}
			if got.Code != test.exit || !strings.Contains(got.Stderr, "borrowed-terminal-verified\n") {
				t.Fatalf("borrowed terminal changed exit or ownership: %+v", got)
			}
			ready := strings.Index(got.Stdout, "Website controls and source errors: ?")
			restored := strings.LastIndex(got.Stdout, "\x1b[?25h")
			if ready < 0 || restored <= ready || strings.Contains(got.Stdout, "\x1b[?1049h") || strings.Contains(got.Stdout, "\x1b[?1049l") ||
				strings.Contains(got.Stdout+got.Stderr, "panic:") || strings.Contains(got.Stderr, "synthetic-private") {
				t.Fatalf("workspace did not restore safe terminal output: %+v", got)
			}
			if test.exit == 0 && (!strings.Contains(got.Stdout[restored:], "Limits workspace closed.") || got.Stderr != "borrowed-terminal-verified\n") {
				t.Fatalf("workspace did not close normally after restoration: %+v", got)
			}
			if test.mode == "write-failure" && got.Stderr != "Could not write command output. Check your terminal, output file, or receiving command.\nborrowed-terminal-verified\n" {
				t.Fatalf("workspace output failure omitted safe recovery: %q", got.Stderr)
			}
			for _, path := range []string{spendThresholdPath(test.project, "spend-limit"), spendThresholdPath(test.project, "spend-alerts"), "/organization/costs"} {
				if requests.count(path) != 1 {
					t.Errorf("workspace did not load %s exactly once", path)
				}
			}
		})
	}
}

// The private PTY follows the existing process-test cleanup pattern. Only the
// ready workspace receives a key; output failure ends after a redraw signal.
const limitsBorrowedTerminalPTY = `
import errno, fcntl, json, os, pty, selectors, signal, struct, subprocess, sys, termios, time
def interrupted(signum, frame):
    raise TimeoutError("borrowed terminal harness interrupted")
signal.signal(signal.SIGTERM, interrupted)
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
def session():
    os.setsid()
    fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
selector = selectors.DefaultSelector()
process = None
completed = False
output = [bytearray(), bytearray()]
try:
    process = subprocess.Popen(sys.argv[1:], stdin=slave, stdout=slave, stderr=subprocess.PIPE, preexec_fn=session)
    os.close(slave)
    slave = -1
    for descriptor, index in [(master, 0), (process.stderr.fileno(), 1)]:
        os.set_blocking(descriptor, False)
        selector.register(descriptor, selectors.EVENT_READ, index)
    sent = False
    mode = os.environ["OPENAI_CLI_LIMITS_BORROWED_PROCESS"]
    deadline = time.monotonic() + 10
    while selector.get_map() or process.poll() is None:
        if time.monotonic() >= deadline:
            raise TimeoutError("borrowed terminal command exceeded ten seconds")
        for key, _ in selector.select(.1):
            try:
                data = os.read(key.fd, 65536)
            except OSError as error:
                if error.errno == errno.EAGAIN:
                    continue
                if error.errno != errno.EIO:
                    raise
                data = b""
            if not data:
                selector.unregister(key.fd)
                continue
            output[key.data].extend(data)
            if len(output[key.data]) > 2 << 20:
                raise RuntimeError("borrowed terminal capture exceeded its allowance")
            if not sent and mode in ("quit", "interrupt") and b"Website controls and source errors: ?" in output[0]:
                key = b"\x03" if mode == "interrupt" else b"q"
                os.write(master, key)
                sent = True
            elif not sent and mode == "write-failure" and b"Website controls and source errors: ?" in output[0]:
                os.kill(process.pid, signal.SIGWINCH)
                sent = True
    code = process.wait(timeout=2)
    completed = True
finally:
    if process is not None and not completed:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=2)
    if process is not None:
        process.stderr.close()
    selector.close()
    os.close(master)
    if slave >= 0:
        os.close(slave)
print(json.dumps({"Code": code, "Stdout": output[0].decode(), "Stderr": output[1].decode()}))
`

func TestMainLimitsWorkflowPartialFailureSurvivesClosedPipe(t *testing.T) {
	for _, test := range []struct {
		name          string
		sourceFailure bool
	}{{"complete sources", false}, {"partial source failure", true}} {
		t.Run(test.name, func(t *testing.T) {
			server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
				if test.sourceFailure && r.URL.Path == "/organization/spend_alerts" {
					return http.StatusForbidden, `{"error":{"message":"private synthetic source failure"}}`
				}
				status, body := limitsWorkflowSuccess(t, false, r)
				if r.URL.Path == "/organization/spend_limit" {
					body = strings.TrimSuffix(body, "}") + `,"future":"` + strings.Repeat("synthetic", 2<<20) + `"}`
				}
				return status, body
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			child := batchesWorkflowProcess(t, ctx, server, "admin", "limits", "show", "--format", "json")
			child.Env = append(child.Env, "OPENAI_ADMIN_KEY=sk-fake-spend-admin")
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
			defer func() {
				if child.ProcessState == nil {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			if _, err := io.ReadFull(stdout, make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			if err := stdout.Close(); err != nil {
				t.Fatal(err)
			}
			err = child.Wait()
			if ctx.Err() != nil || requests.count("/organization/spend_alerts") != 1 {
				t.Fatalf("closed-pipe command timed out or repeated a source: context=%v stderr=%q", ctx.Err(), stderr.String())
			}
			if !test.sourceFailure {
				if err != nil || stderr.Len() != 0 {
					t.Fatalf("lone output EPIPE changed success: error=%v stderr=%q", err, stderr.String())
				}
				return
			}
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("source failure disappeared after EPIPE: error=%v stderr=%q", err, stderr.String())
			}
			if !json.Valid(stderr.Bytes()) || !strings.Contains(stderr.String(), "overview is incomplete") || strings.Contains(stderr.String(), "private synthetic") {
				t.Fatalf("safe structured recovery missing: %q", stderr.String())
			}
		})
	}
}
