package main

import (
	"context"
	"encoding/json"
	"fmt"
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

// Python supplies only PTY plumbing. Every child invokes production main.
// The controller outlives the CLI; macOS session teardown can wait on unread output.
// A separate open of the owned slave fills output without changing child fd flags.
const batchesTerminalCleanupPTY = `
import errno, fcntl, json, os, pathlib, pty, re, select, signal, struct, sys, termios, time
mode, entered, argv = sys.argv[1], pathlib.Path(sys.argv[2]), sys.argv[3:]
if os.getsid(0) != os.getpid():
    os.setsid()
signal.signal(signal.SIGTTOU, signal.SIG_IGN)
signal.signal(signal.SIGHUP, signal.SIG_IGN)
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
before = termios.tcgetattr(slave)
filler = None
if mode == "result_blocked":
    filler = os.open(os.ttyname(slave), os.O_WRONLY | os.O_NONBLOCK | os.O_NOCTTY)
    assert os.path.sameopenfile(slave, filler)
    assert not fcntl.fcntl(slave, fcntl.F_GETFL) & os.O_NONBLOCK
gate_read, gate_write = os.pipe()
pid = os.fork()
if pid == 0:
    os.close(master)
    os.close(gate_write)
    if filler is not None:
        os.close(filler)
    os.setpgid(0, 0)
    assert os.read(gate_read, 1) == b"F"
    os.close(gate_read)
    signal.signal(signal.SIGTTOU, signal.SIG_DFL)
    signal.signal(signal.SIGHUP, signal.SIG_DFL)
    for fd in [0, 1, 2]:
        os.dup2(slave, fd)
    if slave > 2:
        os.close(slave)
    os.execve(argv[0], argv, os.environ)
os.close(gate_read)
os.setpgid(pid, pid)
os.tcsetpgrp(slave, pid)
assert os.getsid(pid) == os.getpid()
assert os.getpgid(pid) == pid and os.tcgetpgrp(slave) == pid
os.write(gate_write, b"F")
os.close(gate_write)
output, status, sent, filled, stop_drain = bytearray(), None, False, 0, False
started = time.monotonic()
phases = []
try:
    while time.monotonic() - started < 2.5:
        if not stop_drain:
            ready, _, _ = select.select([master], [], [], .01)
            if ready:
                try:
                    output.extend(os.read(master, 65536))
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
        else:
            time.sleep(.01)
        hidden = b"\x1b[?25l" in output
        frame = b"synthetic denial" in output if mode == "error_api_quit" else b"batch_synthetic" in output
        if not sent:
            if mode in ["result_quit", "error_api_quit"] and hidden and frame:
                os.write(master, b"q")
                sent = True
            elif mode == "result_interrupt" and hidden and frame:
                os.kill(pid, signal.SIGINT)
                sent = True
            elif mode == "error_interrupt" and entered.exists():
                os.kill(pid, signal.SIGINT)
                sent = True
            elif mode == "result_blocked" and hidden and frame:
                # The explorer has initialized before output becomes blocked.
                stop_drain = True
                phases.append(["fill_start", time.monotonic() - started])
                for _ in range(3):
                    for value in [b"x" * 1024, b"x"]:
                        while filled < 1 << 20:
                            try:
                                count = os.write(filler, value)
                            except BlockingIOError:
                                break
                            if count == 0:
                                break
                            filled += count
                    time.sleep(.01)
                phases.append(["fill_end", time.monotonic() - started])
                assert 0 < filled < 1 << 20, "owned PTY did not become saturated"
                sent = True
        child, candidate = os.waitpid(pid, os.WNOHANG)
        if child:
            status = candidate
            break
    exceeded = status is None
    if exceeded:
        phases.append(["watchdog", time.monotonic() - started])
        os.kill(pid, signal.SIGKILL)
        cleanup_limit = time.monotonic() + 2
        while status is None and time.monotonic() < cleanup_limit:
            child, candidate = os.waitpid(pid, os.WNOHANG)
            if child:
                status = candidate
                break
            ready, _, _ = select.select([master], [], [], .01)
            if ready:
                try:
                    output.extend(os.read(master, 65536))
                except OSError:
                    pass
        assert status is not None, "killed fixture could not be reaped"
    elapsed = time.monotonic() - started
    after = termios.tcgetattr(slave)
    # BSD may mark pending canonical input after restoring raw mode.
    adjusted = list(after)
    pending = getattr(termios, "PENDIN", 0)
    if pending and not before[3] & pending and after[3] & pending:
        adjusted[3] &= ~pending
    restored = before == after or before == adjusted
    os.set_blocking(master, False)
    while True:
        try:
            data = os.read(master, 65536)
        except (BlockingIOError, OSError):
            break
        if not data:
            break
        output.extend(data)
    cursor = re.findall(rb"\x1b\[\?25([hl])", output)
    print(json.dumps({"code": os.waitstatus_to_exitcode(status), "exceeded": exceeded,
        "seconds": elapsed, "cursor_hidden": b"l" in cursor,
        "cursor_visible": not cursor or cursor[-1] == b"h", "modes_restored": restored, "kernel_pendin": before != after and restored,
        "controller_pid": os.getpid(), "cli_pid": pid, "foreground_child": True,
        "output": output.decode(errors="replace"), "filled": filled, "stop_drain": stop_drain, "phases": phases}))
finally:
    if status is None:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    if filler is not None:
        os.close(filler)
    os.close(slave)
    os.close(master)
`

func TestMainBatchesExplorerTerminalCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PTYs do not establish native Windows explorer cleanup")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		if runtime.GOOS == "darwin" {
			t.Fatal("native macOS explorer cleanup requires python3:", err)
		}
		t.Skip("explorer cleanup requires python3 for POSIX PTY plumbing")
	}
	for _, mode := range []string{"result_quit", "result_deadline", "result_interrupt", "result_blocked", "error_deadline", "error_interrupt", "error_deadline_transform", "error_api_quit", "human_api_denial"} {
		t.Run(mode, func(t *testing.T) {
			entered := filepath.Join(t.TempDir(), "entered")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request := requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" {
					t.Errorf("unexpected explorer request: %s %s", r.Method, r.URL)
				}
				if err := os.WriteFile(entered, nil, 0600); err != nil {
					t.Error(err)
					return
				}
				if strings.HasPrefix(mode, "error_") && mode != "error_api_quit" {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "error_api_quit" || mode == "human_api_denial" && request > 1 {
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"error":{"message":"synthetic denial","type":"permission_error","code":"synthetic_permission"}}`)
					return
				}
				status := "completed"
				if mode == "human_api_denial" {
					status = "in_progress"
				}
				fmt.Fprint(w, batchesWorkflowResponse(status, batchesWorkflowCounts))
			}))
			defer server.Close()
			args := []string{"batches", "retrieve", "batch_synthetic", "--wait"}
			switch {
			case strings.HasPrefix(mode, "error_"):
				args = append(args, "--format-error", "explore")
			case mode == "human_api_denial":
				args = append(args, "--poll-interval", "1ms")
			default:
				args = append(args, "--format", "explore")
			}
			if strings.Contains(mode, "deadline") {
				args = append(args, "--wait-timeout", "500ms")
			} else if mode == "result_blocked" {
				args = append(args, "--wait-timeout", "1s")
			}
			if mode == "error_deadline_transform" {
				args = append(args, "--transform-error", "message")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			process := batchesWorkflowProcess(t, ctx, server, args...)
			probe := exec.CommandContext(ctx, python, append([]string{"-c", batchesTerminalCleanupPTY, mode, entered}, process.Args...)...)
			probe.Env = append(process.Env, "TERM=xterm-256color")
			output, err := probe.CombinedOutput()
			if err != nil {
				t.Fatalf("explorer cleanup probe failed: %v %s", err, output)
			}
			var got struct {
				Code          int     `json:"code"`
				Exceeded      bool    `json:"exceeded"`
				Seconds       float64 `json:"seconds"`
				CursorHidden  bool    `json:"cursor_hidden"`
				CursorVisible bool    `json:"cursor_visible"`
				ModesRestored bool    `json:"modes_restored"`
				Output        string  `json:"output"`
				Filled        int     `json:"filled"`
				StopDrain     bool    `json:"stop_drain"`
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("invalid cleanup probe response: %v %s", err, output)
			}
			wantCode, wantRequests := 124, int32(1)
			if mode == "result_quit" {
				wantCode = 0
			} else if strings.Contains(mode, "interrupt") {
				wantCode = 130
			} else if mode == "error_api_quit" || mode == "human_api_denial" {
				wantCode = 1
			}
			if mode == "human_api_denial" {
				wantRequests = 2
			}
			if got.Exceeded || got.Code != wantCode || requests.Load() != wantRequests || !got.ModesRestored {
				t.Fatalf("explorer lifecycle failed: %+v requests=%d; want exit %d requests %d", got, requests.Load(), wantCode, wantRequests)
			}
			switch {
			case mode == "result_blocked":
				if !got.CursorHidden || !got.StopDrain || got.Filled == 0 {
					t.Fatalf("explorer never reached blocked cleanup: %+v", got)
				}
			case strings.HasPrefix(mode, "result_") || mode == "error_api_quit":
				if !got.CursorHidden || !got.CursorVisible {
					t.Fatalf("explorer left the cursor hidden: %+v", got)
				}
				for _, protocol := range []struct {
					name, enabled string
					reset         []string
				}{
					{"bracketed paste", "\x1b[?2004h", []string{"\x1b[?2004l"}},
					{"modified keys", "\x1b[>4;2m", []string{"\x1b[>4m", "\x1b[>4;0m"}},
					{"keyboard enhancement", "\x1b[>1u", []string{"\x1b[<1u", "\x1b[<u"}},
				} {
					if enabled := strings.LastIndex(got.Output, protocol.enabled); enabled >= 0 {
						restored := false
						for _, reset := range protocol.reset {
							restored = restored || strings.LastIndex(got.Output, reset) > enabled
						}
						if !restored {
							t.Errorf("explorer left %s enabled", protocol.name)
						}
					}
				}
			case mode == "human_api_denial":
				if !strings.Contains(got.Output, "403") || strings.Contains(got.Output, "Request canceled.") {
					t.Fatalf("worker shutdown replaced the API error: %+v", got)
				}
			default:
				if got.CursorHidden || !json.Valid([]byte(got.Output)) {
					t.Fatalf("canceled wait started another explorer instead of a static error: %+v", got)
				}
				if mode == "error_deadline_transform" {
					var message string
					if err := json.Unmarshal([]byte(got.Output), &message); err != nil || !strings.Contains(message, "timed out") {
						t.Fatalf("error extraction changed after cancellation: %q (%v)", got.Output, err)
					}
				}
			}
		})
	}
}
