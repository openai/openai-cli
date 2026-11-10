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

    if config["action"] in ("page-boundaries", "pager-boundaries"):
        spec = importlib.util.spec_from_file_location(
            "list_fixture", pathlib.Path(__file__).parent / "demos/list-pager/fixture.py")
        fixture = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(fixture)
        rows = 25 if config["action"] == "pager-boundaries" else 8
        with fixture.serve(pages=3, rows=rows, block_page=2, numbered_files=True) as server:
            config["endpoint"] = f"http://127.0.0.1:{server.server_port}"
            observe(config, navigation, server)
        return
    observe(config, navigation)


def observe(config, navigation, server=None):
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
        if config["action"] in ("print-return", "preview-scrollback"):
            command = '"$@" && echo NEXT_COMMAND'
            if config["action"] == "preview-scrollback":
                command = "printf 'history-%02d\\n' {1..20}; echo PREVIOUS_COMMAND; " + command
            args = ["--noprofile", "--norc", "-c", command, "list-test", binary, *args]
            binary = "/bin/bash"
        terminal = navigation.terminal_module.Terminal(binary, args, env, width=config.get("width", 110), height=config.get("height", 24),
                                                       stderr_file=diagnostic)
        action, mode = config["action"], config["mode"]
        if action == "preview-scrollback":
            terminal.wait("Files 1–10")
            assert state()["requests"] == 1
            size = terminal.size()
            terminal.send(b"q")
            terminal.wait("NEXT_COMMAND", timeout=5)
            assert terminal.child.wait(timeout=5) == 0
            history = []
            display = navigation.terminal_module.screen(terminal.raw, *size, history=history).decode()
            transcript = "\n".join([*history, display]).rstrip()
            assert "history-20\nPREVIOUS_COMMAND\nNEXT_COMMAND" in transcript, transcript
            assert b"\x1b[?1049h" not in terminal.raw and "file_001" not in transcript
            diagnostic.seek(0)
            assert not diagnostic.read()
            return
        if action == "files-stalled-resize":
            terminal.wait("file_008")
            terminal.resize(101, 20)
            navigation.drain(terminal, 0.12)
            assert state()["requests"] == 1
            terminal.send(b" ")
            wait_state("requests", 2)
            navigation.drain(terminal, 0.12)
            if terminal.child.poll() is None:
                terminal.send(b" ")  # A missing guard would now send the starting cursor again.
            navigation.finish(terminal, 1)
            restored = bytes(terminal.raw).rsplit(b"\x1b[?1049l", 1)[-1]
            assert all(row in restored for row in (b"file_001", b"file_009", b"file_stalled"))
            diagnostic.seek(0)
            assert "missing or repeated cursor" in json.load(diagnostic)["message"]
            return
        if action in ("list-range", "list-resize-retry", "files-range", "files-resize", "files-first-request"):
            def check_span(span, calls):
                start, last = map(int, re.search(r"(\d+)–(\d+)", span).groups())
                deadline = time.monotonic() + 5
                offset = config.get("offset", 0)
                prefix = config.get("rowPrefix", "file_")
                while True:
                    terminal.read(0.03)
                    frame = navigation.terminal_module.screen(terminal.raw, *terminal.size()).decode()
                    if (re.search(r"(?m)^" + re.escape(span) + r"\s", frame)
                            and f"{prefix}{start + offset:03d}" in frame and f"{prefix}{last + offset:03d}" in frame):
                        break
                    assert terminal.child.poll() is None and time.monotonic() < deadline, (span, frame)
                navigation.drain(terminal, 0.12)
                assert state()["requests"] == calls, (span, state())

            if action == "files-first-request":
                wait_state("requests", 1)
            elif action == "list-resize-retry":
                wait_state("requests", 1)
                terminal.resize(101, 20)
                with urllib.request.urlopen(config["endpoint"] + "/_release", timeout=2):
                    pass
                check_span("1–8", 2)
                terminal.send(b" ")
                check_span("9–26", 3)
            elif action == "files-resize":
                check_span("1–8", 1)
                terminal.send(b" ")
                check_span("9–16", 2)
                terminal.resize(101, 20)
                check_span("9–16", 2)
                terminal.send(b" ")
                check_span("17–34", 3)
                terminal.resize(101, 9)
                check_span("17–23", 3)
                for span, calls in [("24–30", 3), ("31–34", 3), ("35–41", 4)]:
                    terminal.send(b" ")
                    check_span(span, calls)
            else:
                previous = calls = 0
                for span in config["spans"]:
                    start = int(re.search(r"\d+", span).group())
                    first_view = not previous
                    if previous:
                        terminal.send(b"b" if start < previous else b" ")
                    if start > previous:
                        calls += 1
                    previous = start
                    check_span(span, calls)
                    direct = "--pager" in config["args"]
                    assert (b"\x1b[?1049h" in terminal.raw) == (direct or not first_view)
            terminal.send(b"q")
            navigation.finish(terminal, 0)
            diagnostic.seek(0)
            assert not diagnostic.read()
            return
        if action == "pager-collapsed":
            wait_state("requests", 1)
            terminal.wait("Resize terminal")
            navigation.drain(terminal, 0.1)
            assert b"item_001" not in navigation.terminal_module.screen(terminal.raw, 110, 1)
            terminal.send(b"  \x1b[B")  # Neither Space nor Down can reveal a row in this pane.
            navigation.drain(terminal, 0.2)
            assert state()["requests"] == 1, "advanced past a file that was never visible"
            terminal.resize(110, 24)
            terminal.wait("item_001")
            terminal.wait("item_002")
            assert state()["requests"] == 1
            terminal.send(b" ")
            wait_state("requests", 2)
            terminal.wait("End")
            terminal.send(b"q")
            navigation.finish(terminal, 0)
            return
        if action == "pager-boundaries":
            terminal.wait("file-demo-025")
            terminal.wait("i: inline")
            navigation.drain(terminal, 0.1)
            assert len(server.requests) == 1
            terminal.send(b" ")
            assert server.blocked.wait(3), "the first advance at the last visible file must fetch page two"
            terminal.send(b" " * 40)
            navigation.drain(terminal, 0.1)
            assert len(server.requests) == 2 and server.maximum_inflight == 1
            server.release.set()
            # Tea can repaint only the digits that changed. Read the reconstructed screen.
            deadline = time.monotonic() + 4
            while b"file-demo-050" not in navigation.terminal_module.screen(terminal.raw, 101, 27):
                terminal.read(0.03)
                assert time.monotonic() < deadline, terminal.text()[-1000:]
            terminal.send(b"b")
            navigation.drain(terminal, 0.1)
            terminal.send(b"i")
            wait_inline_rows(terminal, "file-demo-050")
            assert len(server.requests) == 2
            terminal.send(b" ")
            navigation.finish(terminal, 0)
            output = inline_output(terminal)
            assert re.findall(r"file-demo-\d{3}", output) == [f"file-demo-{n:03d}" for n in range(1, 76)], output
            assert output.count("FILENAME") == 1 and len(server.requests) == 3, output
            diagnostic.seek(0)
            assert not diagnostic.read()
            return
        if action == "pager-inline":
            terminal.wait("item_002")
            terminal.wait("i: inline")
            navigation.drain(terminal, 0.1)
            assert state()["requests"] == 1 and b"\x1b[?1049h" in terminal.raw
            terminal.send(b"i")
            wait_inline_rows(terminal, "item_002")
            assert state()["requests"] == 1
            terminal.send(b" ")
            navigation.finish(terminal, 0)
            output = inline_output(terminal)
            assert output.count(config["headers"][-1]) == 1, output
            assert re.findall(r"item_\d{3}", output) == ["item_001", "item_002", "item_003", "item_004"], output
            assert state()["requests"] == 2
            diagnostic.seek(0)
            assert not diagnostic.read()
            return
        if action == "page-boundaries":
            def check_screen(first, last, calls):
                marker = f"File {first}" if first == last else f"Files {first}–{last}"
                wanted_ids = [f"file-demo-{i:03d}" for i in range(first, last + 1)]
                deadline = time.monotonic() + 5
                while True:
                    terminal.read(0.03)
                    # Tea redraws only the changed cells, including numeric ranges.
                    frame = navigation.terminal_module.screen(terminal.raw, *terminal.size()).decode()
                    if marker in frame and re.findall(r"file-demo-\d{3}", frame) == wanted_ids:
                        break
                    assert terminal.child.poll() is None and time.monotonic() < deadline, (marker, frame)
                navigation.drain(terminal, 0.15)
                with server.lock:
                    assert [req["page"] for req in server.requests] == list(range(1, calls + 1)), server.requests
                    assert server.maximum_inflight == 1

            check_screen(1, 7, 1)
            terminal.resize(110, 14)
            check_screen(1, 8, 1)
            terminal.resize(110, 9)
            check_screen(1, 7, 1)
            assert b"\x1b[?1049h" not in terminal.raw
            terminal.send(b" ")
            check_screen(8, 8, 1)
            assert b"\x1b[?1049h" in terminal.raw
            terminal.send(b" ")
            assert server.blocked.wait(3), "Space at the loaded boundary did not fetch page two"
            terminal.send(b" " * 40)
            navigation.drain(terminal, 0.15)
            server.release.set()
            check_screen(9, 15, 2)
            terminal.send(b"b")
            check_screen(8, 8, 2)
            terminal.send(b" ")
            check_screen(9, 15, 2)
            terminal.send(b" ")
            check_screen(16, 16, 2)
            terminal.send(b" ")
            check_screen(17, 23, 3)
            terminal.send(b" ")
            check_screen(24, 24, 3)
            terminal.send(b"q")
            navigation.finish(terminal, 0)
            diagnostic.seek(0)
            assert not diagnostic.read(), "unexpected diagnostics"
            return
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
                direct = "--pager" in config["args"]
                controls = ("i: inline",) if direct else ("↑↓: row", "Space: browse", "↑↓ Space b q")
                deadline = time.monotonic() + 5
                while terminal.child.poll() is None and not any(marker in terminal.text() for marker in controls):
                    terminal.read(0.03)
                    assert time.monotonic() < deadline, terminal.text()
                if config.get("requirePaused"):
                    navigation.drain(terminal, 0.12)
                    assert terminal.child.poll() is None, "list returned before keyboard input"
                    assert state()["requests"] == 1, "list fetched before keyboard input"
                if terminal.child.poll() is None:
                    assert (b"\x1b[?1049h" in terminal.raw) == direct, "unexpected initial screen mode"
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
            navigation.finish(terminal, config["expected"])
            assert b"\x1b[?1049h" not in terminal.raw, "short list or initial error cleared the shell"
            text = terminal.text()
            if mode == "empty":
                assert "No results." in text, text
            elif mode == "unfamiliar":
                assert all(marker in text for marker in ("item_001", "item_002", "retain-synthetic-detail", "ID:")), text
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


def inline_output(terminal):
    _, restored, primary = bytes(terminal.raw).partition(b"\x1b[?1049l")
    return primary.decode("utf-8", errors="replace") if restored else ""


def wait_inline_rows(terminal, marker):
    deadline = time.monotonic() + 5
    while marker not in inline_output(terminal) or "q: quit" not in inline_output(terminal):
        terminal.read(0.03)
        assert terminal.child.poll() is None and time.monotonic() < deadline, inline_output(terminal)[-1400:]


if __name__ == "__main__":
    main()
