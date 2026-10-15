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
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Keep one owner in the test's group while its controller owns a separate session.
// The launch gate prevents the CLI from leaving that session's group before registration.
const batchesTerminalCleanupGuardian = `
import json, os, pathlib, select, signal, socket, sys, time
expected_parent = int(sys.argv.pop(3))
receipt_path = os.environ.pop("OPENAI_CLI_BATCHES_PTY_RECEIPT", "")
stopping = None
def stop_guardian(number, frame):
    global stopping
    stopping = number
for number in [signal.SIGINT, signal.SIGTERM]:
    signal.signal(number, stop_guardian)
parent_channel, controller_channel = socket.socketpair()
controller_pid = os.fork()
if controller_pid:
    controller_channel.close()
    cli_pid, status, pending = None, None, b""
    foreground_ready = False
    started = time.monotonic()
    reason, cleanup_ok = None, True
    def receipt(state):
        if receipt_path:
            path = pathlib.Path(receipt_path)
            temporary = path.with_suffix(".tmp")
            temporary.write_text(json.dumps({"state": state, "guardian_pid": os.getpid(),
                "controller_pid": controller_pid, "cli_pid": cli_pid,
                "reason": reason, "cleanup_ok": cleanup_ok}))
            temporary.replace(path)
    def send(pid, number, group=False):
        try:
            (os.killpg if group else os.kill)(pid, number)
        except ProcessLookupError:
            pass
    def group_exists(pid):
        try:
            os.killpg(pid, 0)
            return True
        except ProcessLookupError:
            return False
    try:
        receipt("started")
        while status is None:
            if stopping is not None:
                reason = "signal-" + str(stopping)
                break
            if os.getppid() != expected_parent:
                reason = "parent-exited"
                break
            if time.monotonic() - started >= 8:
                reason = "guardian-deadline"
                break
            ready, _, _ = select.select([parent_channel], [], [], .01)
            if ready:
                data = parent_channel.recv(128)
                pending += data
                while b"\n" in pending:
                    message, pending = pending.split(b"\n", 1)
                    if message.startswith(b"P "):
                        assert cli_pid is None, "duplicate CLI registration"
                        cli_pid = int(message[2:])
                        assert os.getsid(cli_pid) == controller_pid
                        assert os.getpgid(cli_pid) == controller_pid
                        receipt("registered")
                        parent_channel.sendall(b"F")
                    else:
                        assert message == b"R" and cli_pid is not None
                        foreground_ready = True
                        receipt("foreground-ready")
                if not data:
                    time.sleep(.01)
            child, candidate = os.waitpid(controller_pid, os.WNOHANG)
            if child:
                status = candidate
    except BaseException as error:
        reason = type(error).__name__ + ": " + str(error)
    finally:
        # Finish before the outer supervisor's two-second SIGKILL grace expires.
        cleanup_started = time.monotonic()
        if cli_pid is not None and (status is None or group_exists(cli_pid)):
            send(cli_pid, signal.SIGKILL, group=True)
            if not foreground_ready:
                send(cli_pid, signal.SIGKILL)
        if status is None:
            send(controller_pid, signal.SIGTERM)
            send(controller_pid, signal.SIGCONT)
        while status is None and time.monotonic() - cleanup_started < .75:
            child, candidate = os.waitpid(controller_pid, os.WNOHANG)
            if child:
                status = candidate
                break
            time.sleep(.01)
        if status is None or group_exists(controller_pid):
            send(controller_pid, signal.SIGKILL, group=True)
            if status is None:
                send(controller_pid, signal.SIGKILL)
        groups_gone = False
        while time.monotonic() - cleanup_started < 1.5:
            if status is None:
                child, candidate = os.waitpid(controller_pid, os.WNOHANG)
                if child:
                    status = candidate
            groups_gone = not group_exists(controller_pid) and (cli_pid is None or not group_exists(cli_pid))
            if status is not None and groups_gone:
                break
            time.sleep(.01)
        cleanup_ok = status is not None and groups_gone
        parent_channel.close()
        receipt("finished")
    if not cleanup_ok:
        sys.stderr.write("fixture guardian could not remove every owned process group\n")
    if reason:
        sys.stderr.write("fixture guardian stopped: " + reason + "\n")
    code = os.waitstatus_to_exitcode(status) if status is not None else 1
    sys.exit((128 + stopping) if stopping is not None else (1 if reason or not cleanup_ok else (128 - code if code < 0 else code)))
parent_channel.close()
def stop_controller(number, frame):
    raise SystemExit(128 + number)
for number in [signal.SIGINT, signal.SIGTERM]:
    signal.signal(number, stop_controller)
`

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
    signal.signal(signal.SIGINT, signal.SIG_DFL)
    signal.signal(signal.SIGTERM, signal.SIG_DFL)
    controller_channel.close()
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
controller_channel.sendall(("P " + str(pid) + "\n").encode())
assert controller_channel.recv(1) == b"F"
os.setpgid(pid, pid)
os.tcsetpgrp(slave, pid)
assert os.getsid(pid) == os.getpid()
assert os.getpgid(pid) == pid and os.tcgetpgrp(slave) == pid
os.write(gate_write, b"F")
os.close(gate_write)
output, status, sent, filled, stop_drain = bytearray(), None, False, 0, False
saved_before_signal = None
started = time.monotonic()
phases = []
try:
    controller_channel.sendall(b"R\n")
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
            elif (mode.startswith("result_interrupt") or mode == "result_terminate_wait") and hidden and frame:
                if mode == "result_interrupt_download":
                    saved_before_signal = entered.with_name("output.jsonl").read_bytes().hex()
                number = signal.SIGTERM if mode == "result_terminate_wait" else signal.SIGINT
                os.kill(pid, number)
                phases.append(["signal_sent", int(number), time.monotonic() - started])
                sent = True
            elif mode == "error_interrupt" and entered.exists():
                os.kill(pid, signal.SIGINT)
                phases.append(["signal_sent", int(signal.SIGINT), time.monotonic() - started])
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
        os.killpg(pid, signal.SIGKILL)
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
        "output": output.decode(errors="replace"), "filled": filled, "stop_drain": stop_drain,
        "sent": sent, "phases": phases, "saved_before_signal": saved_before_signal}))
finally:
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    permission_error, cleanup_error = None, None
    cleanup_limit = time.monotonic() + .5
    reaped = status is not None
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    except PermissionError as error:
        permission_error = error
    except OSError as error:
        cleanup_error = error
    if not reaped:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        except PermissionError as error:
            permission_error = permission_error or error
        except OSError as error:
            cleanup_error = cleanup_error or error
        while not reaped and time.monotonic() < cleanup_limit:
            try:
                child, candidate = os.waitpid(pid, os.WNOHANG)
            except ChildProcessError:
                # A signal can arrive after waitpid reaps, before Python assigns its result.
                reaped = True
                break
            except OSError as error:
                cleanup_error = cleanup_error or error
                break
            if child:
                status = candidate
                reaped = True
                break
            try:
                ready, _, _ = select.select([master], [], [], .01)
                if ready:
                    os.read(master, 65536)
            except OSError:
                pass
    # Always close the owned terminal before deciding whether a signal error was benign.
    for descriptor in [filler, slave, master]:
        if descriptor is not None:
            try:
                os.close(descriptor)
            except OSError as error:
                cleanup_error = cleanup_error or error
    try:
        controller_channel.close()
    except OSError as error:
        cleanup_error = cleanup_error or error
    group_gone = False
    while True:
        try:
            os.killpg(pid, 0)
        except ProcessLookupError:
            group_gone = True
            break
        except PermissionError as error:
            permission_error = permission_error or error
        except OSError as error:
            cleanup_error = cleanup_error or error
            break
        if time.monotonic() >= cleanup_limit:
            break
        time.sleep(.01)
    if not reaped or not group_gone:
        raise AssertionError("fixture cleanup retained the CLI or its group") from (permission_error or cleanup_error)
    if cleanup_error is not None:
        raise cleanup_error
    # A raced PermissionError is harmless only after reaping and confirmed group disappearance.
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
	const downloadBytes = "{\"custom_id\":\"synthetic\",\"value\":\"café\"}\r\n"
	for _, mode := range []string{"result_quit", "result_deadline", "result_interrupt", "result_interrupt_wait", "result_interrupt_ordinary", "result_interrupt_download", "result_terminate_wait", "result_blocked", "error_deadline", "error_interrupt", "error_deadline_transform", "error_api_quit", "human_api_denial"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			entered := filepath.Join(directory, "entered")
			downloadPath := filepath.Join(directory, "output.jsonl")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request := requests.Add(1)
				if mode == "result_interrupt_download" && request == 2 && r.Method == http.MethodGet && r.URL.Path == "/files/file_output/content" {
					w.Header().Set("Content-Type", "application/jsonl")
					fmt.Fprint(w, downloadBytes)
					return
				}
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
			switch mode {
			case "result_interrupt_wait", "result_terminate_wait":
				args = []string{"batches", "wait", "batch_synthetic"}
			case "result_interrupt_ordinary":
				args = []string{"batches", "retrieve", "batch_synthetic"}
			case "result_interrupt_download":
				args = []string{"batches", "download", "batch_synthetic", "--output", downloadPath}
			}
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
			probe := exec.CommandContext(ctx, python, append([]string{"-c", batchesTerminalCleanupGuardian + batchesTerminalCleanupPTY, mode, entered, strconv.Itoa(os.Getpid())}, process.Args...)...)
			probe.Env = append(process.Env, "TERM=xterm-256color")
			probe.Cancel = func() error { return probe.Process.Signal(syscall.SIGTERM) }
			probe.WaitDelay = 2 * time.Second
			output, err := probe.CombinedOutput()
			if err != nil {
				t.Fatalf("explorer cleanup probe failed: %v %s", err, output)
			}
			var got struct {
				Code              int     `json:"code"`
				Exceeded          bool    `json:"exceeded"`
				Seconds           float64 `json:"seconds"`
				CursorHidden      bool    `json:"cursor_hidden"`
				CursorVisible     bool    `json:"cursor_visible"`
				ModesRestored     bool    `json:"modes_restored"`
				Output            string  `json:"output"`
				Filled            int     `json:"filled"`
				StopDrain         bool    `json:"stop_drain"`
				Sent              bool    `json:"sent"`
				Phases            [][]any `json:"phases"`
				SavedBeforeSignal string  `json:"saved_before_signal"`
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("invalid cleanup probe response: %v %s", err, output)
			}
			if (strings.Contains(mode, "interrupt") || strings.Contains(mode, "terminate")) && !got.Sent {
				t.Fatalf("fixture did not send its signal: %+v", got)
			}
			wantCode, wantRequests := 124, int32(1)
			if mode == "result_quit" {
				wantCode = 0
			} else if mode == "result_interrupt_ordinary" {
				// Ordinary explorer retains Bubble Tea's error and main's default exit code.
				wantCode = 1
			} else if mode == "result_terminate_wait" {
				wantCode = 143
			} else if strings.Contains(mode, "interrupt") {
				wantCode = 130
			} else if mode == "error_api_quit" || mode == "human_api_denial" {
				wantCode = 1
			}
			if mode == "human_api_denial" || mode == "result_interrupt_download" {
				wantRequests = 2
			}
			if mode == "result_interrupt_download" {
				data, readErr := os.ReadFile(downloadPath)
				if readErr != nil || string(data) != downloadBytes || got.SavedBeforeSignal != fmt.Sprintf("%x", []byte(downloadBytes)) {
					t.Fatalf("receipt interruption changed the saved bytes: before=%q after=%q err=%v", got.SavedBeforeSignal, data, readErr)
				}
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
