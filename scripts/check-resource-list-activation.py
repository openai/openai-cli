#!/usr/bin/env python3
"""Observe public resource list activation using existing private PTY primitives."""
import importlib.util
import json
import os
import pathlib
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
        "TERM": "xterm-256color", "LANG": "en_US.UTF-8", "NO_COLOR": "1", "PAGER": "cat",
        "GOMAXPROCS": "2", "OPENAI_CLI_MAIN_DISPATCH_PROCESS": "1",
        "OPENAI_API_KEY": "sk-fake-resource-list", "OPENAI_ADMIN_KEY": "sk-fake-resource-list-admin",
        "OPENAI_BASE_URL": config["endpoint"] + "/v1",
    }
    args = ["-test.run=^TestMainDispatchProcess$", "--", *config["args"]]
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
        terminal = navigation.terminal_module.Terminal(config["binary"], args, env, width=110, height=24,
                                                       stderr_file=diagnostic)
        action, mode = config["action"], config["mode"]
        if action in ("finish", "one", "error"):
            navigation.finish(terminal, config["expected"])
            text = terminal.text()
            if mode == "empty":
                assert "No results." in text, text
            elif mode == "unfamiliar":
                assert "retain-synthetic-detail" in text and "ID:" in text, text
                assert "FILENAME" not in text, "unfamiliar fields were hidden by the table"
            elif action == "error":
                assert "item_001" not in text, text
            else:
                check_table()
                assert bytes(terminal.raw).count(b"item_001") == 1, "short results were printed twice"
                assert ("item_002" in text) == (action != "one"), text
            assert "q: quit" not in text, "complete first results waited for input"
        else:
            terminal.wait("q: quit", timeout=5)
            wait_state("requests", 1)
            if mode != "blocked-first":
                terminal.wait("item_001", timeout=5)
                check_table()
                navigation.drain(terminal, 0.2)
                assert state()["requests"] == 1, "idle viewer fetched another page"
                terminal.resize(100, 24)
                navigation.drain(terminal, 0.08)
                assert state()["requests"] == 1, "resize fetched another page"
                terminal.resize(110, 24)
                navigation.drain(terminal, 0.08)
                terminal.send(b" ")
                wait_state("requests", 2)
            if action in ("paging", "paging-back"):
                terminal.wait("End of results", timeout=5)
                terminal.send(b"b")
                navigation.drain(terminal, 0.08)
                if action == "paging":
                    terminal.send(b" ")
                    navigation.drain(terminal, 0.08)
                assert state()["requests"] == 2, "cached navigation fetched another page"
                terminal.send(b"p")
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
            if action in ("paging", "paging-back"):
                printed = bytes(terminal.raw).rsplit(b"\x1b[?2004l", 1)[-1]
                expected, absent = ((1, 2), (3, 4)) if action == "paging-back" else ((3, 4), (1, 2))
                for number in expected:
                    assert printed.count(f"ID: item_{number:03d}\r\n".encode()) == 1, printed
                for number in absent:
                    assert f"item_{number:03d}".encode() not in printed, printed
            if mode.startswith("blocked-"):
                wait_state("disconnected", 1)
            if action == "error-next":
                assert "item_001" in terminal.text(), "later failure discarded earlier output"
        diagnostic.seek(0)
        stderr = diagnostic.read().decode("utf-8")
        if config["expected"] == 0:
            assert not stderr, stderr
        else:
            error = json.loads(stderr)
            expected = "synthetic list failure" if action in ("error", "error-next") else "Request canceled."
            assert error["message"] == expected, error
            assert expected not in terminal.text(), "diagnostics leaked into stdout"
        print(json.dumps({"mode": mode, "action": action, "status": terminal.child.returncode,
                          "restored": True, **state()}))
    finally:
        navigation.terminal_module.cleanup_fixture_resources(
            *([terminal.close] if terminal is not None else []), diagnostic.close)


if __name__ == "__main__":
    main()
