#!/usr/bin/env python3
"""Drive the real workspace with deterministic synthetic input and retain raw bytes."""

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import termios
import time


spec = importlib.util.spec_from_file_location("limits_demo_terminal", Path(__file__).resolve().parents[2] / "image_picker_harness.py")
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)

# Trapped signals retain their default disposition in the foreground CLI child.
BASH_WORKSPACE = r'''trap ':' HUP INT TERM
"$@"
limits_scene_status=$?
printf '%s\n' "$limits_scene_status" > "$DEMO_CLI_EXIT" || exit 74
"$DEMO_TERMIOS_PYTHON" -I -B -c '
import json, os, sys, termios
attributes = termios.tcgetattr(0)
attributes[6] = [value.hex() if isinstance(value, bytes) else value for value in attributes[6]]
with open(sys.argv[1], "x", encoding="utf-8") as destination:
    json.dump({"parent_pid": os.getppid(), "attributes": attributes}, destination)
' "$DEMO_TERMIOS_SNAPSHOT" || exit 74
exit "$limits_scene_status"
'''


def write_text(path, value):
    with Path(path).open("x", encoding="utf-8") as destination:
        if destination.write(value) != len(value):
            raise OSError("incomplete scene evidence write")


class RelayTerminal(harness.Terminal):
    def __init__(self, *args, terminal_snapshot, **kwargs):
        self.terminal_snapshot = terminal_snapshot
        super().__init__(*args, **kwargs)

    def read(self, duration=0.05):
        offset = len(self.raw)
        super().read(duration)
        chunk = self.raw[offset:]
        if sys.stdout.buffer.write(chunk) != len(chunk):
            raise OSError("short terminal relay write")
        sys.stdout.buffer.flush()

    def send(self, value):
        if os.write(self.master, value) != len(value):
            raise OSError("short workspace key write")

    def finish(self, expected):
        deadline = time.monotonic() + 8
        while self.child.poll() is None:
            self.read()
            if time.monotonic() >= deadline:
                raise AssertionError("workspace did not exit")
        drain(self, 0.15)
        if self.child.returncode != expected:
            raise AssertionError(f"workspace exit={self.child.returncode}, expected={expected}; tail={self.text()[-900:]}")
        # Bash snapshots the modes before exiting its controlling PTY session.
        snapshot = json.loads(self.terminal_snapshot.read_text())
        if snapshot["parent_pid"] != self.child.pid:
            raise AssertionError("terminal modes were not captured under the live Bash parent")
        restored, initial = snapshot["attributes"].copy(), self.initial.copy()
        initial[6] = [value.hex() if isinstance(value, bytes) else value for value in initial[6]]
        self.terminal_modes = {"initial": initial.copy(), "restored": restored.copy(),
                               "snapshot_parent_pid": snapshot["parent_pid"], "bash_pid": self.child.pid}
        if sys.platform == "darwin":
            restored[3] &= ~termios.PENDIN
            initial[3] &= ~termios.PENDIN
        if restored != initial:
            raise AssertionError("workspace did not restore terminal input")
        if self.raw.rfind(b"\x1b[?1049l") <= self.raw.rfind(b"\x1b[?1049h"):
            raise AssertionError("workspace did not leave its alternate screen")
        if self.raw.rfind(b"\x1b[?25h") <= self.raw.rfind(b"\x1b[?25l"):
            raise AssertionError("workspace did not restore cursor visibility")


def drain(terminal, seconds):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        terminal.read(min(0.03, max(0, deadline - time.monotonic())))


def screen_text(terminal):
    with tempfile.TemporaryDirectory(prefix="limits-demo-screen-") as temporary:
        recording, transcript = Path(temporary) / "screen.cast", Path(temporary) / "screen.txt"
        header = {"version": 2, "width": terminal.initial_size[0], "height": terminal.initial_size[1]}
        write_text(recording, "\n".join(json.dumps(item) for item in [header, *terminal.events]) + "\n")
        subprocess.run([os.environ["DEMO_ASCIINEMA"], "convert", "-f", "txt", str(recording), str(transcript)],
                       capture_output=True, check=True, timeout=10,
                       env={"PATH": "/usr/bin:/bin", "HOME": temporary,
                            "ASCIINEMA_STATE_HOME": temporary + "/state", "ASCIINEMA_CONFIG_HOME": temporary + "/config"})
        return transcript.read_text()


def wait_screen(terminal, required, forbidden=(), timeout=10):
    deadline, screen = time.monotonic() + timeout, ""
    while time.monotonic() < deadline:
        drain(terminal, 0.15)
        screen = screen_text(terminal)
        if all(text in screen for text in required) and not any(text in screen for text in forbidden):
            return screen
        if terminal.child.poll() is not None:
            break
    raise AssertionError(f"workspace state missing {required!r}; screen={screen!r}")


def requests(scene):
    entries = [json.loads(line) for line in Path(os.environ["DEMO_REQUEST_LOG"]).read_text().splitlines() if line]
    return [entry for entry in entries if entry["scene"] == scene]


def mutations(scene):
    return [item for item in requests(scene) if item["method"] in {"POST", "DELETE"}]


def checkpoint(terminal, evidence, output, name, required=()):
    screen = screen_text(terminal)
    if not all(text in screen for text in required):
        raise AssertionError(f"checkpoint {name} omitted its required preview: {required!r}")
    write_text(Path(str(output) + f".{name}.txt"), screen)
    evidence["checkpoints"].append({"name": name, "seconds": time.monotonic() - terminal.started})
    drain(terminal, 0.6)


def key(terminal, evidence, label, value):
    if terminal.child.poll() is not None:
        raise AssertionError("workspace exited before its scripted action")
    evidence["keys"].append({"key": label, "seconds": time.monotonic() - terminal.started})
    terminal.send(value)
    drain(terminal, 0.08)


def replace(terminal, evidence, label, value):
    key(terminal, evidence, label, b"\x01\x0b\x1b[200~" + value.encode() + b"\x1b[201~")


def review(terminal, evidence, scene, output):
    wait_screen(terminal, ["> Back", "Enter: select"])
    checkpoint(terminal, evidence, output, "review-top")
    preview_seen = False
    for _ in range(40):
        screen = screen_text(terminal)
        if "Command printing needs" in screen or "Command preview omitted" in screen:
            raise AssertionError("review omitted the real command preview")
        if not preview_seen and "Command preview (inspection only)" in screen:
            checkpoint(terminal, evidence, output, "review-command",
                       ("Command preview (inspection only)",))
            preview_seen = True
        if "Apply change" in screen and "Read to the end" not in screen:
            break
        if not preview_seen and "Command preview" in screen:
            key(terminal, evidence, "Show command preview", b"\x1b[B")
        else:
            key(terminal, evidence, "PageDown", b"\x1b[6~")
    else:
        raise AssertionError("review never exposed its final action")
    if not preview_seen:
        raise AssertionError("review reached Apply without a command preview checkpoint")
    if mutations(scene):
        raise AssertionError("fixture received a mutation before explicit confirmation")
    if scene == "alert-print":
        wait_screen(terminal, ["Ctrl+P"])
        checkpoint(terminal, evidence, output, "review-print", ("Ctrl+P",))
        key(terminal, evidence, "Ctrl+P print and exit", b"\x10")
        return
    key(terminal, evidence, "Tab to Apply", b"\t")
    wait_screen(terminal, ["> Apply change"])
    checkpoint(terminal, evidence, output, "review-apply")
    key(terminal, evidence, "Apply", b"\r")


def run_scene(terminal, scene, evidence, output):
    if scene == "slow-cancel":
        gate = Path(os.environ["DEMO_GATE_DIRECTORY"]) / "slow-cancel.requested"
        deadline = time.monotonic() + 8
        while not gate.exists():
            terminal.read()
            if time.monotonic() >= deadline:
                raise AssertionError("slow request did not reach fixture")
        wait_screen(terminal, ["Loading", "Ctrl+C"])
        checkpoint(terminal, evidence, output, "loading")
        key(terminal, evidence, "Ctrl+C", b"\x03")
        terminal.finish(130)
        write_text(Path(os.environ["DEMO_GATE_DIRECTORY"]) / "slow-cancel.release", "child canceled and exited\n")
        if mutations(scene):
            raise AssertionError("canceling a load caused a mutation")
        return
    wait_screen(terminal, ["Set hard spend limit", "Ctrl+C"], ["Loading"])
    checkpoint(terminal, evidence, output, "overview")
    if scene.startswith("alert-"):
        key(terminal, evidence, "Alerts", b"\x1b[C")
        wait_screen(terminal, ["Create spend alert"])
        creating = scene in {"alert-create", "alert-print"}
        if not creating:
            key(terminal, evidence, "Select alert", b"\x1b[B")
        if scene == "alert-delete":
            key(terminal, evidence, "Delete selected alert", b"d")
        else:
            key(terminal, evidence, "Edit alert", b"\r")
            wait_screen(terminal, ["Monthly USD", "Review change"])
            replace(terminal, evidence, "Monthly USD", "75.50" if creating else "150.25")
            key(terminal, evidence, "Recipients", b"\t")
            if creating:
                replace(terminal, evidence, "Synthetic recipient", "finance@example.test")
            key(terminal, evidence, "Subject prefix", b"\t")
            if creating:
                prefix = "Demo team's budget $HOME; keep 'quoted' text" if scene == "alert-print" else "Demo"
                replace(terminal, evidence, "Subject prefix", prefix)
            key(terminal, evidence, "Prefix mode", b"\t")
            if scene == "alert-edit":
                key(terminal, evidence, "Replace prefix mode", b"\r")
                key(terminal, evidence, "Clear prefix to null", b"\r")
            key(terminal, evidence, "Review action", b"\t")
            checkpoint(terminal, evidence, output, "form")
            key(terminal, evidence, "Review", b"\r")
    elif scene == "org-limit-delete":
        key(terminal, evidence, "Select removal", b"\x1b[B")
        wait_screen(terminal, ["Remove hard spend limit"])
        key(terminal, evidence, "Review removal", b"\r")
    elif scene == "rate-update":
        key(terminal, evidence, "Alerts", b"\x1b[C")
        key(terminal, evidence, "Rates", b"\x1b[C")
        wait_screen(terminal, ["rl_demo"])
        key(terminal, evidence, "Edit rate", b"\r")
        wait_screen(terminal, ["Requests / minute", "Review change"])
        replace(terminal, evidence, "Requests per minute", "250")
        for _ in range(6):
            key(terminal, evidence, "Next rate field", b"\t")
        checkpoint(terminal, evidence, output, "form")
        key(terminal, evidence, "Review", b"\r")
    else:
        key(terminal, evidence, "Edit hard limit", b"\r")
        wait_screen(terminal, ["Monthly USD", "Review change"])
        replace(terminal, evidence, "Monthly USD", "125.25")
        key(terminal, evidence, "Review action", b"\t")
        checkpoint(terminal, evidence, output, "form")
        key(terminal, evidence, "Review", b"\r")
    review(terminal, evidence, scene, output)
    if scene == "alert-print":
        terminal.finish(0)
        scene_requests = requests(scene)
        if mutations(scene) or not scene_requests or any(item["method"] != "GET" for item in scene_requests):
            raise AssertionError("printing a reviewed command must perform no mutation")
        channel = json.dumps({"recipients": ["finance@example.test"],
                              "subject_prefix": "Demo team's budget $HOME; keep 'quoted' text",
                              "type": "email"}, sort_keys=True, separators=(",", ":"))
        command = (evidence["staged_binary"]["path"] + " '--base-url=" + evidence["request_base_url"]
                   + "' admin organization spend-alerts create"
                   " --currency USD --interval month --threshold-amount 7550 --notification-channel '"
                   + channel.replace("'", "'\\''") + "'")
        output_flags = terminal.terminal_modes["initial"][1]
        newline = "\r\n" if output_flags & termios.OPOST and output_flags & termios.ONLCR else "\n"
        expected = (command + newline).encode()
        restored = terminal.raw.rfind(b"\x1b[?1049l") + len(b"\x1b[?1049l")
        start = len(terminal.raw) - len(expected)
        if (start < restored or terminal.raw[start:] != expected
                or terminal.raw[restored:].count(expected) != 1
                or terminal.raw.rfind(b"\x1b[?25h") > start):
            raise AssertionError("printed command bytes differ or precede terminal restoration")
        with Path(str(output) + ".printed-command.raw.txt").open("xb") as destination:
            if destination.write(terminal.raw[start:]) != len(expected):
                raise OSError("incomplete printed command evidence write")
        evidence["printed_command"] = {"byte_offset": start, "byte_length": len(expected),
                                       "sha256": hashlib.sha256(expected).hexdigest()}
        checkpoint(terminal, evidence, output, "printed-command")
    elif scene == "preflight-400":
        wait_screen(terminal, ["HTTP 400", "125.25", "Review change"], ["Saving"])
        checkpoint(terminal, evidence, output, "preflight", ("HTTP 400", "125.25"))
        key(terminal, evidence, "Blocked review before refresh", b"\r\r")
        wait_screen(terminal, ["Refresh with r", "125.25", "Review change"])
        checkpoint(terminal, evidence, output, "refresh-required", ("Refresh with r", "125.25"))
        key(terminal, evidence, "F1 recovery", b"\x1bOP")
        help_screen = wait_screen(terminal, ["HTTP 400", "Recheck"])
        compact_help = "".join(help_screen.split())
        for message in ("No write sent.", "request fields and scope", "press r to refresh"):
            if "".join(message.split()) not in compact_help:
                raise AssertionError("preflight help omitted status or recovery guidance")
        checkpoint(terminal, evidence, output, "preflight-help", ("HTTP 400",))
        key(terminal, evidence, "Back", b"\x1b")
        wait_screen(terminal, ["Set hard spend limit"])
        key(terminal, evidence, "Quit", b"q")
        terminal.finish(1)
        if mutations(scene):
            raise AssertionError("failed preflight sent a mutation")
        if b"synthetic-private-preflight" in terminal.raw:
            raise AssertionError("preflight recovery exposed private response data")
        restored = terminal.raw.rfind(b"\x1b[?1049l") + len(b"\x1b[?1049l")
        diagnostic = terminal.raw[restored:]
        for message in (b"HTTP 400", b"No write sent.", b"request fields and scope",
                        b"Reopen the limits workspace", b"review current settings",
                        b"help admin organization spend-limit retrieve",
                        b"original connection, authentication, and scope options"):
            if message not in diagnostic:
                raise AssertionError("normal exit lost the preflight diagnostic")
        if b"may have succeeded" in diagnostic:
            raise AssertionError("preflight exit incorrectly claimed an uncertain mutation")
    elif scene == "uncertain":
        wait_screen(terminal, ["Save not confirmed", "Review change"])
        checkpoint(terminal, evidence, output, "uncertain")
        key(terminal, evidence, "Blocked repeat Enter", b"\r\r")
        if len(mutations(scene)) != 1:
            raise AssertionError("uncertain write repeated automatically")
        key(terminal, evidence, "Back", b"\x1b")
        wait_screen(terminal, ["Writes locked"])
        key(terminal, evidence, "Quit", b"q")
        terminal.finish(1)
    else:
        wait_screen(terminal, ["Removal confirmed" if scene == "org-limit-delete" else "Change confirmed."], ["Loading", "Saving"])
        checkpoint(terminal, evidence, output, "saved")
        key(terminal, evidence, "Quit", b"q")
        terminal.finish(0)
        if len(mutations(scene)) != 1:
            raise AssertionError("scene did not produce exactly one confirmed mutation")


def main():
    scene, output = os.environ["DEMO_SCENE_NAME"], Path(os.environ["DEMO_EVIDENCE_BASE"])
    width, height = int(os.environ["DEMO_WIDTH"]), int(os.environ["DEMO_HEIGHT"])
    if (width, height) not in {(80, 24), (40, 12)} or not all(os.isatty(fd) for fd in (0, 1, 2)):
        raise RuntimeError("scene requires a supported terminal size")
    binary = shutil.which("openai")
    if binary is None:
        raise RuntimeError("immutable CLI binary is unavailable")
    binary = Path(binary).resolve(strict=True)
    runtime = Path(os.environ["DEMO_RUNTIME_DIRECTORY"]).resolve(strict=True)
    if runtime.parent != Path("/private/tmp") or not runtime.name.startswith("cli-demo."):
        raise AssertionError("the recorder runtime must use an owned synthetic /private/tmp directory")
    project = scene in {"project-limit", "alert-edit", "rate-update"}
    args = ["projects", "limits", "--project-id", "proj_demo"] if project else ["admin", "limits"]
    if scene == "alert-print":
        args.insert(0, "--base-url=" + os.environ["OPENAI_BASE_URL"])
    evidence = {"scene": scene, "width": width, "height": height, "command": ["openai", *args],
                "keys": [], "checkpoints": [], "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                "parent_shell": "/bin/bash", "recorder_runtime": str(runtime),
                "request_base_url": os.environ["OPENAI_BASE_URL"]}
    print("$ openai " + " ".join(args), flush=True)
    original = termios.tcgetattr(1)
    relay = termios.tcgetattr(1)
    relay[1] &= ~termios.OPOST
    try:
        termios.tcsetattr(1, termios.TCSANOW, relay)
        with tempfile.TemporaryDirectory(prefix="openai-limits-demo-", dir=runtime) as temporary:
            staged = Path(temporary) / "openai"
            shutil.copy2(binary, staged)
            staged_hash = hashlib.sha256(staged.read_bytes()).hexdigest()
            if staged_hash != evidence["binary_sha256"]:
                raise AssertionError("staged CLI differs from the supplied binary")
            evidence["staged_binary"] = {"path": str(staged), "sha256_before": staged_hash}
            home = Path(temporary) / "home"
            home.mkdir(mode=0o700)
            exit_receipt = Path(temporary) / "cli-exit-status"
            terminal_snapshot = Path(temporary) / "terminal-modes.json"
            env = {"PATH": temporary + ":/usr/bin:/bin", "HOME": str(home), "TERM": "xterm-256color", "LANG": "en_US.UTF-8",
                   "GOMAXPROCS": "2", "PAGER": "cat", "OPENAI_API_KEY": "synthetic-demo-key",
                   "OPENAI_ADMIN_KEY": "synthetic-demo-admin-key", "OPENAI_BASE_URL": os.environ["OPENAI_BASE_URL"],
                   "DEMO_CLI_EXIT": str(exit_receipt), "DEMO_TERMIOS_PYTHON": sys.executable,
                   "DEMO_TERMIOS_SNAPSHOT": str(terminal_snapshot)}
            if os.environ.get("DEMO_NO_COLOR", "1") == "1":
                env.update(NO_COLOR="1", FORCE_COLOR="0")
            terminal = None
            try:
                # Post-command status handling keeps Bash as the CLI's real parent.
                # Both processes inherit the existing PTY session and process group.
                shell_args = ["--noprofile", "--norc", "-c", BASH_WORKSPACE, "bash", str(staged), *args]
                terminal = RelayTerminal("/bin/bash", shell_args, env, width=width, height=height,
                                         controlling_terminal=True, terminal_snapshot=terminal_snapshot)
                run_scene(terminal, scene, evidence, output)
                status_text = exit_receipt.read_text()
                if status_text != str(terminal.child.returncode) + "\n":
                    raise AssertionError("Bash did not preserve the actual CLI exit status")
                evidence["cli_exit_status"] = int(status_text)
                evidence["terminal_modes"] = terminal.terminal_modes
                evidence["staged_binary"]["sha256_after"] = hashlib.sha256(staged.read_bytes()).hexdigest()
                if (evidence["staged_binary"]["sha256_after"] != evidence["binary_sha256"]
                        or hashlib.sha256(binary.read_bytes()).hexdigest() != evidence["binary_sha256"]):
                    raise AssertionError("supplied or staged CLI changed during capture")
                if str(binary).encode() in terminal.raw:
                    raise AssertionError("capture exposed the private binary source path")
                if terminal.child.returncode != 0:
                    restored = terminal.raw.rfind(b"\x1b[?1049l")
                    if restored < 0:
                        raise AssertionError("final diagnostic lacks terminal restoration")
                    diagnostic = b" ".join(terminal.raw[restored + len(b"\x1b[?1049l"):].lower().split())
                    for action in (b"press r", b"use r to", b"refresh with r", b"press ?", b"press f1", b"use f1",
                                   b"f1 for", b"f1:", b"ctrl+p", b"tab to apply"):
                        if action in diagnostic:
                            raise AssertionError("final diagnostic names an action from the closed workspace")
                evidence.update(exit_status=terminal.child.returncode, relayed_bytes=len(terminal.raw),
                                relayed_sha256=hashlib.sha256(terminal.raw).hexdigest())
                with Path(str(output) + ".tty").open("xb") as destination:
                    if destination.write(terminal.raw) != len(terminal.raw):
                        raise OSError("incomplete raw terminal write")
                header = {"version": 2, "width": width, "height": height, "title": "Limits workspace; synthetic terminal replay"}
                write_text(Path(str(output) + ".child.cast"), "\n".join(json.dumps(item) for item in [header, *terminal.events]) + "\n")
                write_text(Path(str(output) + ".exit.txt"), screen_text(terminal))
            finally:
                if terminal is not None:
                    terminal.close()
        evidence["staged_binary"]["removed"] = not Path(temporary).exists()
        if not evidence["staged_binary"]["removed"]:
            raise AssertionError("owned staged CLI directory remains after cleanup")
    finally:
        termios.tcsetattr(1, termios.TCSANOW, original)
    write_text(Path(str(output) + ".json"), json.dumps(evidence, indent=2) + "\n")
    print("\n$ ", end="", flush=True)
    time.sleep(1)


if __name__ == "__main__":
    main()
