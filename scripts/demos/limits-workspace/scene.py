#!/usr/bin/env python3
"""Drive the real workspace with deterministic synthetic input and retain raw bytes."""

import codecs
import errno
import hashlib
import importlib.util
import json
import os
import re
import select
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
BASH_WORKSPACE = r'''for ((limits_seed=1; limits_seed<=DEMO_SCROLLBACK_ROWS; limits_seed++)); do
  printf "Prior shell output %03d\n" "$limits_seed"
done
printf "F40 workspace start\n"
trap ':' HUP INT TERM
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


validator_spec = importlib.util.spec_from_file_location("limits_demo_validator", Path(__file__).with_name("validate.py"))
validator = importlib.util.module_from_spec(validator_spec)
validator_spec.loader.exec_module(validator)
restored_offset = validator.restored_offset

observer_path = Path(__file__).parents[1] / "audio-workbench" / "observer.py"
observer_spec = importlib.util.spec_from_file_location("limits_verified_observer_sources", observer_path)
observer_sources = importlib.util.module_from_spec(observer_spec)
observer_spec.loader.exec_module(observer_sources)
pyte = observer_sources._verified_packages(os.environ.get("DEMO_OBSERVER_SOURCES"))


class TerminalObserver:
    """Observe synthetic output and reply at the actual terminal query boundary."""
    def __init__(self, width, height, reply):
        class Screen(pyte.HistoryScreen):
            def write_process_input(self, data):
                reply(data.encode())

            def report_device_status(self, mode, **kwargs):
                if not kwargs.get("private"):
                    column = self.cursor.x
                    self.cursor.x = min(column, self.columns - 1)
                    try:
                        super().report_device_status(mode)
                    finally:
                        self.cursor.x = column

            def report_device_attributes(self, mode=0, **kwargs):
                if mode == 0 and not kwargs.get("private"):
                    self.write_process_input("\x1b[?1;2c")

        self.screen = Screen(width, height, history=observer_sources.HISTORY_ROWS)
        self.stream = pyte.ByteStream(self.screen)
        self.pending = bytearray()
        self.bytes_observed = 0

    def feed(self, data):
        self.bytes_observed += len(data)
        self.pending.extend(data)
        while self.pending:
            if self.pending[0] != 27:
                end = self.pending.find(b"\x1b")
                end = len(self.pending) if end < 0 else end
            elif len(self.pending) < 2:
                break
            elif self.pending[1] == ord("["):
                end = next((i + 1 for i in range(2, len(self.pending))
                            if 0x40 <= self.pending[i] <= 0x7e), None)
                if end is None:
                    break
            elif self.pending[1] in b"]PX^_":
                terminators = ((b"\x07", 1), (b"\x1b\\", 2)) if self.pending[1] == ord("]") else ((b"\x1b\\", 2),)
                endings = [position + size for marker, size in terminators
                           if (position := self.pending.find(marker, 2)) >= 0]
                if not endings:
                    break
                end = min(endings)
            else:
                end = 1
                while end < len(self.pending) and 0x20 <= self.pending[end] <= 0x2f:
                    end += 1
                if end == len(self.pending):
                    break
                assert 0x30 <= self.pending[end] <= 0x7e, "malformed terminal escape sequence"
                end += 1
            token = bytes(self.pending[:end])
            del self.pending[:end]
            if token in (b"\x1b]11;?\x07", b"\x1b]11;?\x1b\\"):
                self.screen.write_process_input("\x1b]11;rgb:1818/1818/1818\x07")
            elif token[:2] in (b"\x1bP", b"\x1bX", b"\x1b^", b"\x1b_") or re.fullmatch(rb"\x1b\[>[0-9;]*c", token):
                pass  # Ignore unsupported string controls and secondary device queries.
            elif not (re.fullmatch(rb"\x1b\[[<>=][0-9;]*u", token)
                      or re.fullmatch(rb"\x1b\[>4(?:;[0-2])?m", token)):
                self.stream.feed(token)
        assert len(self.screen.history.top) < observer_sources.HISTORY_ROWS, "observer history reached its bound"

    def resize(self, width, height):
        self.screen.resize(lines=height, columns=width)

    def text(self):
        return "\n".join(self.screen.display)

    def history(self):
        return ["".join(line.get(column, self.screen.default_char).data
                        for column in range(self.screen.columns)).rstrip()
                for line in self.screen.history.top]


def probe_observer():
    payload = (b"before\r\n\x1b[6n\x1b]11;?\x07\x1b[c"
               b"\x1bP+q5443\x1b\\\x1b_hidden\x1b\\\x1b[>0c"
               b"\x1b[>4;2m\x1b[>1u\x1b[<1uafter\x1b[6n")
    expected = [b"\x1b[2;1R", b"\x1b]11;rgb:1818/1818/1818\x07", b"\x1b[?1;2c", b"\x1b[2;6R"]
    for boundary in range(len(payload) + 1):
        replies = []
        observer = TerminalObserver(40, 12, replies.append)
        observer.feed(payload[:boundary])
        observer.feed(payload[boundary:])
        assert replies == expected and not observer.pending, "split query replies differ"
        assert observer.screen.display[:2] == ["before".ljust(40), "after".ljust(40)]
    replies = []
    observer = TerminalObserver(40, 12, replies.append)
    for byte in payload:
        observer.feed(bytes([byte]))
    assert replies == expected, "single-byte query replies differ"
    observer = TerminalObserver(40, 12, replies.append)
    observer.feed(b"x" * 40 + b"\x1b[6n")
    assert replies[-1] == b"\x1b[1;40R", "cursor report exceeded the right margin"
    observer = TerminalObserver(40, 12, lambda _: None)
    observer.feed(b"".join(f"seed {number:02d}\r\n".encode() for number in range(30)))
    assert observer.history()[:3] == ["seed 00", "seed 01", "seed 02"]
    for prefix, final in ((b"\x1b(", b"B"), (b"\x1b#", b"8"), (b"\x1bPq", b"\x1b\\")):
        observer = TerminalObserver(40, 12, lambda _: None)
        observer.feed(prefix)
        assert observer.pending, "incomplete escape was accepted"
        observer.feed(final)
        assert not observer.pending, "complete escape remained pending"
    restored = (b"\x1b[?25l\x1b[?2004h\x1b[>4;2m\x1b[>1u\x1b[?u"
                b"Website controls and source errors: ?\x1b[?2004l\x1b[>4m\x1b[<1u\x1b[?25h")
    assert restored_offset(restored) == len(restored)
    for late in (b"\x1b[>5u", b"\x1b[>4;1m", b"\x1b[>04;1m", b"\x1b[?25;2004h",
                 b"\x1b[?1049;25h", b"\x1b[2;0J", b"\x1b[?25l", b"\x1b[<2u"):
        try:
            restored_offset(restored + late)
        except AssertionError:
            pass
        else:
            raise AssertionError("late terminal mode change was accepted")
    print("PASS: observer split/coalesced queries; keyboard controls; cursor bounds; retained history.")


def write_text(path, value):
    with Path(path).open("x", encoding="utf-8") as destination:
        if destination.write(value) != len(value):
            raise OSError("incomplete scene evidence write")


class RelayTerminal(harness.Terminal):
    def __init__(self, *args, terminal_snapshot, **kwargs):
        self.terminal_snapshot = terminal_snapshot
        self.protocol_replies = []
        self.decoder = codecs.getincrementaldecoder("utf-8")("strict")
        self.observer = TerminalObserver(kwargs["width"], kwargs["height"], self.reply)
        super().__init__(*args, **kwargs)

    def read(self, duration=0.05):
        if not select.select([self.master], [], [], duration)[0]:
            return
        try:
            chunk = os.read(self.master, 65536)
        except OSError as error:
            if error.errno == errno.EIO:
                return
            raise
        self.raw.extend(chunk)
        decoded = self.decoder.decode(chunk)
        if decoded:
            self.events.append([time.monotonic() - self.started, "o", decoded])
        self.observer.feed(chunk)
        if sys.stdout.buffer.write(chunk) != len(chunk):
            raise OSError("short terminal relay write")
        sys.stdout.buffer.flush()

    def resize(self, width, height, notify=True):
        self.observer.resize(width, height)
        super().resize(width, height, notify=notify)

    def reply(self, value):
        self.protocol_replies.append(value.decode("ascii"))
        self.send(value)

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
        self.decoder.decode(b"", final=True)
        restored_offset(self.raw, loading=expected == 130)
        if self.raw.rfind(b"\x1b[?25h") <= self.raw.rfind(b"\x1b[?25l"):
            raise AssertionError("workspace did not restore cursor visibility")


def drain(terminal, seconds):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        terminal.read(min(0.03, max(0, deadline - time.monotonic())))


def screen_text(terminal):
    return terminal.observer.text()


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


def read_review(terminal, evidence, scene, output, name):
    wait_screen(terminal, ["> Back", "Enter: select"])
    checkpoint(terminal, evidence, output, name + "-top")
    preview_seen = False
    for _ in range(40):
        screen = screen_text(terminal)
        if "Command printing needs" in screen or "Command preview omitted" in screen:
            raise AssertionError("review omitted the real command preview")
        if not preview_seen and "Command preview (inspection only)" in screen:
            checkpoint(terminal, evidence, output, name + "-command",
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


def review(terminal, evidence, scene, output, name="review"):
    read_review(terminal, evidence, scene, output, name)
    if scene == "alert-print":
        wait_screen(terminal, ["Ctrl+P"])
        checkpoint(terminal, evidence, output, name + "-print", ("Ctrl+P",))
        key(terminal, evidence, "Ctrl+P print and exit", b"\x10")
        return
    key(terminal, evidence, "Tab to Apply", b"\t")
    wait_screen(terminal, ["> Apply change"])
    if scene == "alert-create":
        checkpoint(terminal, evidence, output, "review-before-help", ("> Apply change",))
        key(terminal, evidence, "Review help", b"\x1bOP")
        wait_screen(terminal, ["Spend manages hard monthly"])
        checkpoint(terminal, evidence, output, "review-help")
        terminal.resize(39, 11)
        wait_screen(terminal, ["Resize to at least 40 x 12."])
        checkpoint(terminal, evidence, output, "review-help-small")
        target = (40, 12) if terminal.initial_size == (80, 24) else (80, 24)
        terminal.resize(*target)
        wait_screen(terminal, ["Spend manages hard monthly"], ["Resize to at least"])
        checkpoint(terminal, evidence, output, "review-help-resized")
        key(terminal, evidence, "Return to review", b"\x1b")
        wait_screen(terminal, ["> Back", "Read to the end"], ["> Apply change"])
        checkpoint(terminal, evidence, output, "review-return", ("> Back", "Read to the end"))
        key(terminal, evidence, "Apply blocked until reread", b"\t")
        wait_screen(terminal, ["> Back", "Read to the end"], ["> Apply change"])
        terminal.resize(*terminal.initial_size)
        wait_screen(terminal, ["> Back", "Read to the end"], ["> Apply change"])
        checkpoint(terminal, evidence, output, "review-resize-return")
        if mutations(scene):
            raise AssertionError("resizing or returning from help applied the draft")
        key(terminal, evidence, "Inspect draft after resize", b"\x1b")
        for _ in range(4):
            key(terminal, evidence, "Previous resized alert field", b"\x1b[Z")
        wait_screen(terminal, ["Monthly USD", "75.50", "Review change"])
        checkpoint(terminal, evidence, output, "review-resize-draft", ("Monthly USD", "75.50"))
        for _ in range(4):
            key(terminal, evidence, "Next resized alert field", b"\t")
        key(terminal, evidence, "Fresh review after resize", b"\r")
        read_review(terminal, evidence, scene, output, "review-resized")
        key(terminal, evidence, "Select Apply again", b"\t")
        wait_screen(terminal, ["> Apply change"])
    checkpoint(terminal, evidence, output, name + "-apply")
    key(terminal, evidence, "Apply" if name == "review" else "Apply refreshed", b"\r")


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
            amount = "75.505" if scene == "alert-create" else "75.50" if creating else "150.25"
            replace(terminal, evidence, "Monthly USD", amount)
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
            if scene == "alert-create":
                wait_screen(terminal, ["Enter a USD amount", "Review change"])
                checkpoint(terminal, evidence, output, "validation", ("Enter a USD amount",))
                if mutations(scene):
                    raise AssertionError("an invalid amount sent a mutation")
                key(terminal, evidence, "Validation help", b"\x1bOP")
                wait_screen(terminal, ["Draft details", "two decimal places."])
                checkpoint(terminal, evidence, output, "validation-help", ("two decimal places.",))
                key(terminal, evidence, "Repeated help", b"\x1bOP")
                key(terminal, evidence, "Return to draft", b"\x1b")
                wait_screen(terminal, ["> Review change", "Demo", "Enter a USD amount"])
                checkpoint(terminal, evidence, output, "draft-return", ("> Review change", "Demo"))
                for _ in range(4):
                    key(terminal, evidence, "Previous alert field", b"\x1b[Z")
                wait_screen(terminal, ["Monthly USD", "75.505"])
                replace(terminal, evidence, "Correct Monthly USD", "75.50")
                for _ in range(4):
                    key(terminal, evidence, "Next alert field", b"\t")
                key(terminal, evidence, "Review corrected draft", b"\r")
                wait_screen(terminal, ["> Back", "Enter: select"])
                key(terminal, evidence, "Escape from review", b"\x1b")
                wait_screen(terminal, ["> Review change", "Demo"])
                for _ in range(4):
                    key(terminal, evidence, "Inspect retained alert amount", b"\x1b[Z")
                wait_screen(terminal, ["Monthly USD", "75.50", "Review change"])
                checkpoint(terminal, evidence, output, "review-back", ("Monthly USD", "75.50"))
                for _ in range(4):
                    key(terminal, evidence, "Return to review action", b"\t")
                key(terminal, evidence, "Review retained draft", b"\r")
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
        wait_screen(terminal, ["Tokens / day", "Review change"])
        replace(terminal, evidence, "Tokens per day", "9007199254740993")
        key(terminal, evidence, "Review rate action", b"\t")
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
        restored = restored_offset(terminal.raw, loading=scene == "slow-cancel")
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
    elif scene == "preflight-recover":
        wait_screen(terminal, ["HTTP 400", "125.25", "Review change"], ["Saving"])
        checkpoint(terminal, evidence, output, "recover-failed", ("HTTP 400", "125.25"))
        if mutations(scene):
            raise AssertionError("failed preflight sent a mutation")
        key(terminal, evidence, "Refresh retained draft", b"r")
        wait_screen(terminal, ["Baseline refreshed.", "125.25", "Review change"], ["Loading"])
        checkpoint(terminal, evidence, output, "recover-refreshed", ("125.25", "Baseline refreshed."))
        if mutations(scene):
            raise AssertionError("refresh applied the retained draft")
        key(terminal, evidence, "Review recovered draft", b"\r")
        review(terminal, evidence, scene, output, "refreshed-review")
        wait_screen(terminal, ["Change confirmed."], ["Loading", "Saving"])
        checkpoint(terminal, evidence, output, "saved")
        key(terminal, evidence, "Quit", b"q")
        terminal.finish(0)
        if len(mutations(scene)) != 1:
            raise AssertionError("recovered preflight did not produce exactly one mutation")
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
        key(terminal, evidence, "Return to retained draft", b"\x1b")
        wait_screen(terminal, ["125.25", "Review change"])
        checkpoint(terminal, evidence, output, "preflight-draft", ("125.25", "Review change"))
        key(terminal, evidence, "Back to overview", b"\x1b")
        wait_screen(terminal, ["Set hard spend limit"])
        key(terminal, evidence, "Quit", b"q")
        terminal.finish(1)
        if mutations(scene):
            raise AssertionError("failed preflight sent a mutation")
        if b"synthetic-private-preflight" in terminal.raw:
            raise AssertionError("preflight recovery exposed private response data")
        restored = restored_offset(terminal.raw, loading=scene == "slow-cancel")
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
        key(terminal, evidence, "Refresh uncertain outcome", b"r")
        wait_screen(terminal, ["Writes locked", "125.25"], ["Loading"])
        checkpoint(terminal, evidence, output, "uncertain-refreshed", ("Writes locked", "125.25"))
        key(terminal, evidence, "Blocked change after refresh", b"\r")
        if len(mutations(scene)) != 1:
            raise AssertionError("refresh unlocked an uncertain write")
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
                   "DEMO_TERMIOS_SNAPSHOT": str(terminal_snapshot),
                   "DEMO_SCROLLBACK_ROWS": str(height * 2 + 4 if scene == "project-limit" else 0)}
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
                newline = b"\r\n" if terminal.initial[1] & termios.OPOST and terminal.initial[1] & termios.ONLCR else b"\n"
                marker = b"F40 workspace start" + newline
                assert terminal.raw.count(marker) == 1, "missing or repeated prelude boundary"
                evidence["prelude_boundary"] = terminal.raw.index(marker) + len(marker)
                assert not terminal.observer.pending, "terminal ended inside a control sequence"
                evidence["observer"] = {"bytes_observed": terminal.observer.bytes_observed,
                                        "source_manifest_sha256": observer_sources.MANIFEST_SHA256,
                                        "package_commits": dict(observer_sources.COMMITS),
                                        "limitation": "Offline viewport model; resize clips without terminal reflow."}
                history = terminal.observer.history()
                evidence["prior_scrollback"] = {"seeded": scene == "project-limit", "retained": history}
                if scene == "project-limit":
                    expected = [f"Prior shell output {number:03d}" for number in range(1, 4)]
                    positions = [history.index(line) for line in expected]
                    assert positions == sorted(positions), "prior shell scrollback changed order"
                evidence["protocol_replies"] = terminal.protocol_replies
                evidence["resize_events"] = [event for event in terminal.events if event[1] == "r"]
                evidence["staged_binary"]["sha256_after"] = hashlib.sha256(staged.read_bytes()).hexdigest()
                if (evidence["staged_binary"]["sha256_after"] != evidence["binary_sha256"]
                        or hashlib.sha256(binary.read_bytes()).hexdigest() != evidence["binary_sha256"]):
                    raise AssertionError("supplied or staged CLI changed during capture")
                if str(binary).encode() in terminal.raw:
                    raise AssertionError("capture exposed the private binary source path")
                if terminal.child.returncode != 0:
                    restored = restored_offset(terminal.raw, loading=scene == "slow-cancel")
                    diagnostic = b" ".join(terminal.raw[restored:].lower().split())
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
    if sys.argv[1:] == ["--check-observer"]:
        probe_observer()
    elif len(sys.argv) == 1:
        main()
    else:
        raise SystemExit("usage: scene.py [--check-observer]")
