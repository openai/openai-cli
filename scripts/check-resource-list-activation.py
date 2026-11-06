#!/usr/bin/env python3
"""Observe public resource list activation using existing private PTY primitives."""
import importlib.util
import json
import os
import pathlib
import re
import signal
import sys
import time
import urllib.request


def main():
    config = json.load(sys.stdin)
    spec = importlib.util.spec_from_file_location(
        "list_navigation", pathlib.Path(__file__).with_name("check-list-navigation.py"))
    navigation = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(navigation)

    def interrupted(_signal, _frame):
        raise TimeoutError("resource list observer interrupted")

    signal.signal(signal.SIGTERM, interrupted)
    env = {
        "PATH": "/usr/bin:/bin", "HOME": config["home"], "XDG_CONFIG_HOME": config["home"],
        "TERM": "xterm-256color", "TERM_PROGRAM": "Apple_Terminal", "LANG": "en_US.UTF-8", "NO_COLOR": "1", "PAGER": "cat",
        "GOMAXPROCS": "2", "OPENAI_CLI_MAIN_DISPATCH_PROCESS": "1",
        "OPENAI_API_KEY": "sk-fake-resource-list", "OPENAI_ADMIN_KEY": "sk-fake-resource-list-admin",
        "OPENAI_BASE_URL": config["endpoint"] + "/v1",
    }
    process = config.get("process", "TestMainDispatchProcess")
    args = [f"-test.run=^{process}$", "--", *config["args"]]
    diagnostic = open(pathlib.Path(config["home"]) / "stderr", "w+b")
    terminal = None

    def state():
        with urllib.request.urlopen(config["endpoint"] + "/_state", timeout=2) as response:
            return json.load(response)

    def wait_state(key, expected):
        deadline = time.monotonic() + 5
        while state()[key] != expected:
            assert time.monotonic() < deadline, (key, expected, state(), terminal.text()[-1200:])
            terminal.read(0.02)

    def check_table():
        text = terminal.text()
        assert all(header in text for header in config["headers"]), text
        assert "ID:" not in text and "Filename:" not in text, text

    try:
        binary = config["binary"]
        if config["action"] == "print-return":
            args = ["--noprofile", "--norc", "-c", '"$@" && echo NEXT_COMMAND', "list-test", binary, *args]
            binary = "/bin/bash"
        terminal = navigation.terminal_module.Terminal(binary, args, env, width=config.get("width", 110), height=24,
                                                       stderr_file=diagnostic)
        action, mode = config["action"], config["mode"]
        if action in ("stdin-value", "stdin-interrupt"):
            terminal.send(b"assistants")
            terminal.wait("assistants")
            navigation.drain(terminal, 0.35)
            assert terminal.child.poll() is None and state()["requests"] == 0
            assert "q: quit" not in terminal.text() and b"\x1b[?1049h" not in terminal.raw
            if action == "stdin-interrupt":
                os.kill(terminal.child.pid, signal.SIGINT)
                status = terminal.child.wait(timeout=4)
                assert status in (-signal.SIGINT, 128 + signal.SIGINT), status
                navigation.finish(terminal, status)
                assert state()["requests"] == 0
            else:
                # The first Ctrl+D delivers the pending bytes. The second ends the read.
                terminal.send(b"\x04")
                navigation.drain(terminal, 0.07)
                terminal.send(b"\x04")
                navigation.finish(terminal, 0)
                assert "item_001" in terminal.text() and state()["requests"] == 1, terminal.text()
            return
        if action == "print-return":
            # No input is sent. The second shell command must run on its own.
            terminal.wait("NEXT_COMMAND", timeout=5)
            status = terminal.child.wait(timeout=5)
            assert status == 0, status
            output = terminal.text()
            assert "Space: browse" not in output and b"\x1b[?1049h" not in terminal.raw, output
            print(output)
            return
        if action in ("discover", "inspect", "watch", "watch-without-flag", "live"):
            if action in ("discover", "inspect"):
                deadline = time.monotonic() + 5
                while terminal.child.poll() is None and not any(marker in terminal.text() for marker in
                                                                ("↑↓: row", "Space: browse", "↑↓ Space b q")):
                    terminal.read(0.03)
                    assert time.monotonic() < deadline, terminal.text()
                assert b"\x1b[?1049h" not in terminal.raw, "list cleared the shell on startup"
                if terminal.child.poll() is None:
                    if action == "inspect" and config["width"] < 40:
                        terminal.send(b" ")
                        terminal.wait("BQQD", timeout=5)
                    terminal.send(b"q")
            navigation.finish(terminal, config["expected"])
            if action in ("discover", "inspect"):
                text = terminal.text()
                for marker in config.get("markers") or []:
                    assert marker in text, (marker, text)
                for marker in config.get("omit") or []:
                    assert marker not in text, (marker, text)
                print(text)
            diagnostic.seek(0)
            stderr = diagnostic.read().decode()
            if action not in ("discover", "inspect"):
                assert b"\x1b[?1049h" not in terminal.raw, "watch entered the resource pager"
            if action == "live":
                for marker in config.get("markers") or []:
                    assert marker in terminal.text(), (marker, terminal.text())
            if action == "watch":
                assert terminal.text().count("Batch snapshot ") == 2, terminal.text()
            if action == "watch-without-flag":
                assert "Use --watch" in stderr, stderr
            else:
                assert not stderr, stderr
            return
        if action in ("finish", "one", "error"):
            if mode == "unfamiliar":
                terminal.wait("item_001")
                terminal.send(b" ")
                terminal.wait("item_002")
                terminal.send(b"q")
            navigation.finish(terminal, config["expected"])
            if mode != "unfamiliar":
                assert b"\x1b[?1049h" not in terminal.raw, "short list or initial error cleared the shell"
            text = terminal.text()
            if mode == "empty":
                assert "No results." in text, text
            elif mode == "unfamiliar":
                assert "retain-synthetic-detail" in text and "ID:" in text, text
                assert "FILENAME" not in text, "unfamiliar fields were hidden by the table"
            elif action == "error":
                if mode in ("missing-cursor", "missing-legacy-cursor"):
                    restored = bytes(terminal.raw).rsplit(b"\x1b[?2004l", 1)[-1]
                    if "skills" not in config["args"]:
                        assert re.findall(rb"item_\d{3}", restored) == [b"item_001", b"item_002"], restored
                    else:
                        assert restored.count(b"Synthetic skill") == 2, restored
                else:
                    assert "item_001" not in text, text
            else:
                check_table()
                assert bytes(terminal.raw).count(b"item_001") == 1, "short results were printed twice"
                assert ("item_002" in text) == (action != "one"), text
        else:
            terminal.wait("q: quit", timeout=5)
            wait_state("requests", 1)
            if mode != "blocked-first":
                terminal.wait("item_001", timeout=5)
                check_table()
                navigation.drain(terminal, 0.2)
                assert state()["requests"] == 1, "idle command fetched another page"
                if action == "paging-legacy-keys":
                    before = bytes(terminal.raw)
                    terminal.send(b"pi")
                    navigation.drain(terminal, 0.1)
                    assert terminal.child.poll() is None, "removed keys quit the command"
                    assert state()["requests"] == 1, "removed keys fetched another page"
                    assert bytes(terminal.raw) == before, "removed keys redrew output"
                if action != "paging":
                    terminal.resize(100, 24)
                    navigation.drain(terminal, 0.08)
                    assert state()["requests"] == 1, "resize fetched another page"
                    terminal.resize(110, 24)
                    navigation.drain(terminal, 0.08)
                    assert state()["requests"] == 1, "resize fetched another page"
                terminal.send(b" ")
                wait_state("requests", 2)
            if action in ("paging", "paging-legacy-keys"):
                terminal.wait("End", timeout=5)
                assert terminal.child.poll() is None, "final page did not remain open"
                terminal.send(b"b")
                navigation.drain(terminal, 0.1)
                terminal.send(b" ")
                navigation.drain(terminal, 0.1)
                assert state()["requests"] == 2, "cached navigation requested another page"
                terminal.send(b"q")
            elif action == "error-next":
                pass
            else:
                if mode == "blocked-next":
                    terminal.send(b" " * 40)
                    navigation.drain(terminal, 0.15)
                    assert state()["requests"] == 2 and state()["maximum"] == 1, "duplicate next-page request"
                if action in ("SIGINT", "SIGTERM"):
                    os.kill(terminal.child.pid, getattr(signal, action))
                else:
                    terminal.send({"q": b"q", "ctrl-c": b"\x03"}[action])
            navigation.finish(terminal, config["expected"])
            if action in ("paging", "paging-legacy-keys"):
                assert state()["requests"] == 2, "final completion fetched another page"
            if action == "paging":
                # Restore the primary screen around the alternate-screen segment.
                # A shell command following q must start in column zero, with no
                # live controls or duplicate table left above its prompt.
                raw = bytes(terminal.raw)
                prefix, entered, rest = raw.partition(b"\x1b[?1049h")
                _, restored, suffix = rest.partition(b"\x1b[?1049l")
                assert entered and restored, "Space must open and restore the full-screen browser"
                primary = b"prior shell output\r\n$ openai list\r\n" + prefix + suffix + b"$ next command"
                lines = navigation.terminal_module.screen(primary, 110, 24).decode().splitlines()
                assert "prior shell output" in lines and "$ next command" in lines, lines
                assert not any("q: quit" in line or "item_001" in line for line in lines), lines
            if mode.startswith("blocked-"):
                wait_state("disconnected", 1)
            if action == "error-next":
                # A transient row in the alternate screen disappears on exit.
                # Verify the permanent output after restoration, once per received row.
                restored = bytes(terminal.raw).rsplit(b"\x1b[?2004l", 1)[-1]
                count = 4 if mode == "repeat-cursor" else 2
                assert re.findall(rb"item_\d{3}", restored) == [
                    f"item_{i:03d}".encode() for i in range(1, count + 1)
                ], restored
        diagnostic.seek(0)
        stderr = diagnostic.read().decode("utf-8")
        if config["expected"] == 0:
            assert not stderr, stderr
        else:
            error = json.loads(stderr)
            expected = "synthetic list failure" if action in ("error", "error-next") else "Request canceled."
            if mode in ("repeat-cursor", "empty-continuation", "empty-next-continuation", "missing-cursor", "missing-legacy-cursor"):
                expected = "List pagination stopped because the API returned a missing or repeated cursor. Output may be incomplete."
            assert error["message"] == expected, error
            assert expected not in terminal.text(), "diagnostics leaked into stdout"
        print(json.dumps({"mode": mode, "action": action, "status": terminal.child.returncode,
                          "restored": True, **state()}))
    finally:
        navigation.terminal_module.cleanup_fixture_resources(
            *([terminal.close] if terminal is not None else []), diagnostic.close)


if __name__ == "__main__":
    main()
