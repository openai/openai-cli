"""Native terminal scene, reusing the reviewed live private-PTY lifecycle.

Adapts the retained ctrlc-fix/native/ctrlc_scene.py private-PTY lifecycle.
No screenshot implementation, shell, API calls or prerecorded graphics replay.
The separate platform driver owns native screenshot export at checkpoints.
The CLI's actual PTY bytes are relayed unchanged to the existing terminal.
"""
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import sys
import termios
import time

ROOT = Path(__file__).resolve().parent
EVIDENCE = ROOT
MANIFEST = ROOT / "manifest.json"
LABELS = ("before", "after")
LARGE = ROOT / "fixtures" / "synthetic-large.png"
DETAIL = ROOT / "fixtures" / "synthetic-detail.png"
FIXTURES = {
    LARGE: "bb162ad291fd507e859a43788d033fcc23ccd79adbab0d250da55c4a9b562574",
    DETAIL: "2ab84c40dc9c99a3a5ff8c95a3bf19a7765c12f70ae8000d9d622ff8835d3eea",
}


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def selection(label):
    payload = MANIFEST.read_bytes()
    selected = json.loads(payload)["binaries"][label]
    check(selected["frozen"] is True, "AFTER is unavailable: the replacement-compatible candidate has not been frozen")
    check(isinstance(selected["path"], str) and isinstance(selected["sha256"], str), "manifest path/hash is missing")
    check(len(selected["sha256"]) == 64 and all(c in "0123456789abcdef" for c in selected["sha256"]), "manifest SHA256 is invalid")
    binary = (EVIDENCE / selected["path"]).resolve()
    check(EVIDENCE in binary.parents, "binary must be a retained file inside this evidence tree")
    check(digest(binary) == selected["sha256"], "binary changed; freeze and revalidate the manifest first")
    return selected, binary, hashlib.sha256(payload).hexdigest()


def emit(data):
    """Write every byte, preserving partial-write correctness."""
    view = memoryview(data)
    while view:
        count = os.write(1, view)
        check(count > 0, "stdout stopped accepting bytes")
        view = view[count:]


def line(message):
    emit((message + "\r\n").encode())


def stop(_signum, _frame):
    raise TimeoutError("scene interrupted or exceeded its 60-second work bound")


def group_exists(group):
    try:
        os.killpg(group, 0)
        return True
    except ProcessLookupError:
        return False


def signal_group(group, sig):
    try:
        os.killpg(group, sig)
    except ProcessLookupError:
        pass


def drain_group(group, seconds):
    deadline = time.monotonic() + seconds
    while group_exists(group) and time.monotonic() < deadline:
        time.sleep(0.02)
    return not group_exists(group)


def finish_phase(process):
    """Signal only the unreaped owned group; report any surviving descendants."""
    forced = process.poll() is None
    if forced:
        signal_group(process.pid, signal.SIGTERM)
    try:
        process.wait(timeout=3)
    except subprocess.TimeoutExpired:
        signal_group(process.pid, signal.SIGKILL)
        process.wait(timeout=2)
    # wait/poll can reap the group leader, releasing its PID for reuse. From
    # here, inspect only: a remaining group fails the run and is left to the
    # disposable runner's teardown, never signaled through a stale numeric ID.
    gone = drain_group(process.pid, 1)
    return {"harness_termination_needed": forced, "process_group_gone": gone}


def worker(label, hold_seconds, preview_only, directory):
    term_program = os.environ.get("TERM_PROGRAM")
    term = os.environ.get("TERM", "")
    accepted = ("kitty", "ghostty", "iTerm.app", "WezTerm") if preview_only else ("kitty", "ghostty")
    terminal = term_program if term_program in accepted else None
    if not term_program and term in ("xterm-kitty", "xterm-ghostty"):
        terminal = term[len("xterm-"):]
    check(terminal is not None, "Requires a supported terminal identity; Ctrl-C comparison supports Kitty/Ghostty")
    check(term != "dumb" and not any(os.environ.get(key) for key in ("TMUX", "STY", "ZELLIJ"))
          and not term.startswith(("screen", "tmux")), "Requires a direct supported terminal, outside a multiplexer")
    check(os.isatty(1), "stdout must be your existing terminal, not a pipe")
    selected, binary, manifest_hash = selection(label)
    expected = selected["sha256"]
    check(all(digest(path) == value for path, value in FIXTURES.items()), "fixture changed")
    for subdir in ("home", "config", "cache"):
        (directory / subdir).mkdir()
    summary = {
        "binary": str(binary), "binary_sha256": expected, "label": label,
        "source_commit": selected.get("source_commit"), "manifest_sha256": manifest_hash,
        "terminal_identity": terminal, "fixture_sha256": {str(p): h for p, h in FIXTURES.items()},
        "terminal_environment": {"TERM": term, "TERM_PROGRAM": term_program},
        "terminal_version_environment": os.environ.get("TERM_PROGRAM_VERSION"),
        "host": {"os": os.uname().sysname, "release": os.uname().release, "machine": os.uname().machine},
        "transport": "live unmodified CLI PTY bytes", "native_appearance": "native exports require pixel inspection",
        "candidate_status": selected["status"],
        "vintr_offset_after_first_frame": 0, "preview_only": preview_only,
        "script_sha256": digest(Path(__file__).resolve()),
    }
    master = slave = None
    started = time.monotonic()
    signal.signal(signal.SIGALRM, stop)
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    signal.alarm(60)
    try:
        # Same controlling-terminal/process-group setup as ../typed_ctrlc.py.
        master, slave = pty.openpty()
        os.setsid()
        fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
        signal.signal(signal.SIGTTOU, signal.SIG_IGN)
        signal.signal(signal.SIGHUP, signal.SIG_IGN)
        try:
            size = fcntl.ioctl(1, termios.TIOCGWINSZ, b"\0" * 8)
        except OSError:
            size = struct.pack("HHHH", 40, 120, 1200, 800)
        fcntl.ioctl(slave, termios.TIOCSWINSZ, size)
        settings = termios.tcgetattr(slave)
        settings[3] |= termios.ISIG | termios.ICANON | termios.ECHO
        settings[3] &= ~termios.NOFLSH
        settings[6][termios.VINTR] = b"\x03"
        termios.tcsetattr(slave, termios.TCSANOW, settings)
        summary["initial_lflag"] = settings[3]
        summary["private_pty"] = os.ttyname(slave)
        summary["outer_termios_before"] = repr(termios.tcgetattr(1))
        env = {
            "PATH": "/usr/bin:/bin", "HOME": str(directory / "home"),
            "XDG_CONFIG_HOME": str(directory / "config"), "XDG_CACHE_HOME": str(directory / "cache"),
            "TERM": term,
            "CI": "", "NO_COLOR": "", "CLICOLOR": "", "OPENAI_API_KEY": "", "OPENAI_ADMIN_KEY": "",
            "OPENAI_BASE_URL": "http://127.0.0.1:1",
        }
        if term_program is not None:
            env["TERM_PROGRAM"] = term_program

        def control_terminal():
            os.setpgid(0, 0)
            os.tcsetpgrp(0, os.getpgrp())
            check(os.tcgetpgrp(0) == os.getpgrp(), "CLI group is not foreground TTY group")
            signal.signal(signal.SIGTTOU, signal.SIG_DFL)
            signal.signal(signal.SIGHUP, signal.SIG_DFL)
            os.write(2, b"HARNESS: foreground controlling TTY verified\n")

        line("OpenAI CLI | " + terminal + " | " + label.upper() + ": " + selected["display_name"])
        line("Live local synthetic PNG. No API. " + ("Normal preview." if preview_only else "Automatic typed Ctrl-C after first frame, then retry."))
        if label == "after":
            line("DRAFT: native appearance needs inspection; this run cannot establish readiness by itself.")
        line("RUN " + directory.parent.name + " CASE " + directory.name)
        phases = (("preview", DETAIL),) if preview_only else (("prior", DETAIL), ("cancel", LARGE), ("retry", DETAIL))
        for phase, fixture in phases:
            line("Starting " + phase + " phase")
            data = bytearray()
            phase_start = time.monotonic()
            target = header_end = sent_at = None
            barrier = False
            with (directory / (phase + ".stderr")).open("wb") as error:
                process = subprocess.Popen(
                    [str(binary), "images", "preview", "--inline", "on", str(fixture)],
                    stdin=slave, stdout=slave, stderr=error, env=env, preexec_fn=control_terminal,
                )
                try:
                    (directory / "active-group").write_text(str(process.pid) + "\n")
                    summary["active_process_group"] = process.pid
                    while True:
                        check_cancel(directory)
                        check(time.monotonic() - phase_start < 12, phase + " exceeded 12 seconds")
                        ready, _, _ = select.select([master], [], [], 0.05)
                        if not ready:
                            if process.poll() is not None:
                                break
                            continue
                        limit = 65536
                        if phase == "cancel" and not barrier:
                            limit = 1 if header_end is None else target - len(data)
                        try:
                            part = os.read(master, limit)
                        except OSError as error:
                            if error.errno == errno.EIO and process.poll() is not None:
                                break
                            raise
                        if not part:
                            break
                        data.extend(part)
                        emit(part)  # Actual CLI bytes forwarded now, without modification/replay.
                        if phase == "cancel" and not barrier:
                            if header_end is None:
                                start = data.find(b"\x1b_G")
                                end = data.find(b";", start) if start >= 0 else -1
                                if end >= 0:
                                    check(b"m=1" in data[start:end], "first frame must be nonfinal")
                                    header_end = end + 1
                                    target = header_end + 4096 + 2
                            if target is not None and len(data) == target:
                                check(data[header_end + 4096:header_end + 4098] == b"\x1b\\", "first frame end invalid")
                                check(process.poll() is None, "process exited before VINTR")
                                summary["during_upload_lflag"] = termios.tcgetattr(slave)[3]
                                os.write(master, b"\x03")
                                barrier = True
                                sent_at = time.monotonic()
                    status = process.wait(timeout=2)
                    os.tcsetpgrp(slave, os.getpgrp())
                finally:
                    cleanup = finish_phase(process)
                    summary[phase + "_group_cleanup"] = cleanup
                    (directory / (phase + ".bin")).write_bytes(data)
                    if cleanup["process_group_gone"]:
                        (directory / "active-group").unlink(missing_ok=True)
                        summary.pop("active_process_group", None)
            (directory / (phase + ".bin")).write_bytes(data)
            error_text = (directory / (phase + ".stderr")).read_text()
            check("foreground controlling TTY verified" in error_text, "foreground setup marker absent")
            restored = termios.tcgetattr(slave) == settings
            summary[phase] = {
                "exit": status, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(),
                "restored_settings": restored, "vintr_typed": barrier,
                "seconds": round(time.monotonic() - phase_start, 5),
                "vintr_to_exit_seconds": round(time.monotonic() - sent_at, 5) if sent_at else None,
                "stderr": error_text,
            }
            check(restored, "private terminal settings were not fully restored")
            check(cleanup["process_group_gone"], "a helper process group survived cleanup")
            check(not cleanup["harness_termination_needed"], "CLI/helper needed harness termination")
            if phase == "cancel":
                check(barrier and status == 1 and "Request canceled." in error_text, "cancellation contract failed")
            else:
                check(status == 0, "retry failed")
            line("\n" + phase + " exit=" + str(status) + " | private terminal settings restored=" + str(restored))
            checkpoint(directory, phase, summary)
        summary["originals_unchanged"] = all(digest(path) == value for path, value in FIXTURES.items())
        summary["binary_unchanged"] = digest(binary) == expected
        summary["manifest_unchanged"] = digest(MANIFEST) == manifest_hash
        summary["outer_termios_unchanged"] = repr(termios.tcgetattr(1)) == summary["outer_termios_before"]
        check(summary["originals_unchanged"], "fixture changed")
        check(summary["binary_unchanged"], "binary changed during scene")
        check(summary["manifest_unchanged"], "binary manifest changed during scene")
        check(summary["outer_termios_unchanged"], "outer terminal settings changed")
        summary["behavior_checks"] = "pass; native appearance still requires human inspection"
        line("\nDONE | " + label.upper() + " | native image and following text need visual inspection")
        if label == "after":
            line("DRAFT candidate. A successful picture does not establish full readiness.")
        line("Evidence: " + str(directory / "result.json"))
        line("Scene ends in " + str(hold_seconds) + " seconds. It does not read your keyboard.")
        summary["seconds_before_hold"] = round(time.monotonic() - started, 5)
        (directory / "result.json").write_text(json.dumps(summary, indent=2) + "\n")
        time.sleep(hold_seconds)
    except BaseException as error:
        summary["failure"] = str(error)
        (directory / "result.json").write_text(json.dumps(summary, indent=2) + "\n")
        raise
    finally:
        signal.alarm(0)
        if master is not None:
            os.close(master)
        if slave is not None:
            os.close(slave)


def check_cancel(directory):
    check(not (directory / "cancel-requested").exists(), "native capture driver requested cancellation")


def checkpoint(directory, phase, summary):
    """Pause at an observed live frame until the isolated driver exports pixels."""
    marker = "CHECKPOINT " + directory.parent.name + " " + directory.name + " " + phase
    line(marker)
    ready = {"phase": phase, "marker": marker, "monotonic": time.monotonic(),
             "scene_pid": os.getpid(), "phase_result": summary[phase]}
    temporary = directory / (phase + ".ready.tmp")
    temporary.write_text(json.dumps(ready, indent=2) + "\n")
    temporary.rename(directory / (phase + ".ready.json"))
    ack = directory / (phase + ".captured.json")
    deadline = time.monotonic() + 12
    while not ack.exists():
        check_cancel(directory)
        check(time.monotonic() < deadline, "native screenshot checkpoint timed out: " + phase)
        time.sleep(0.03)
    captured = json.loads(ack.read_text())
    check(captured["marker"] == marker and captured["phase"] == phase, "wrong screenshot acknowledgement")
    summary.setdefault("native_screenshots", {})[phase] = captured


def main():
    check(sys.platform in ("linux", "darwin"), "This scene requires Linux or macOS")
    check(len(sys.argv) == 5 and sys.argv[1] in ("--managed", "--worker"),
          "usage: scene.py --managed before|after preview|ctrlc CASE_DIRECTORY")
    label, mode = sys.argv[2:4]
    check(label in LABELS and mode in ("preview", "ctrlc"), "invalid scene")
    directory = Path(sys.argv[4]).resolve()
    check(directory.parents[1] == ROOT / "runs" and directory.parent.name.startswith("run-")
          and directory.name == mode + "-" + label and directory.is_dir(), "invalid isolated case directory")
    selection(label)
    check_cancel(directory)
    if sys.argv[1] == "--worker":
        worker(label, 0, mode == "preview", directory)
        return
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGHUP, stop)
    process = subprocess.Popen([sys.executable, "-I", str(Path(__file__).resolve()),
                                "--worker", label, mode, str(directory)])
    try:
        (directory / "worker-pid").write_text(str(process.pid) + "\n")
        status = process.wait(timeout=65)
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=6)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=2)
        active = directory / "active-group"
        if active.exists():
            group = int(active.read_text())
            check(group > 1, "invalid tracked child group")
            (directory / "wrapper-cleanup.json").write_text(json.dumps({
                "group": group, "group_signaled": False,
                "failure": "worker left a tracked group; disposable runner teardown required",
            }) + "\n")
            check(False, "worker left a tracked group; disposable runner teardown required")
    raise SystemExit(status)


if __name__ == "__main__":
    main()
