#!/usr/bin/env python3
"""Observe Models signal cleanup for TestMainModelsListSignals using a shared PTY."""
import importlib.util
import json
import os
import pathlib
import signal
import sys
import time


def main():
    config = json.load(sys.stdin)
    spec = importlib.util.spec_from_file_location(
        "models_terminal", pathlib.Path(__file__).with_name("image_picker_harness.py"))
    harness = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(harness)

    def interrupted(signum, frame):
        raise TimeoutError("Models signal observer interrupted")

    signal.signal(signal.SIGTERM, interrupted)
    env = {
        "PATH": "/usr/bin:/bin", "HOME": config["home"], "XDG_CONFIG_HOME": config["home"],
        "TERM": "xterm-256color", "LANG": "en_US.UTF-8", "NO_COLOR": "1", "PAGER": "cat",
        "GOMAXPROCS": "2", "OPENAI_CLI_MAIN_DISPATCH_PROCESS": "1",
        "OPENAI_API_KEY": "sk-fake-models-signal-test", "OPENAI_BASE_URL": config["endpoint"],
    }
    args = ["-test.run=^TestMainDispatchProcess$", "--", "openai", "models", "list"]
    args.extend(config["flags"] or [])
    # Models receives stdin explicitly. A noncontrolling PTY keeps termios
    # observable after the child exits, including on Darwin.
    terminal = harness.Terminal(config["binary"], args, env, width=80, height=12)
    try:
        terminal.wait("q: quit", timeout=5)
        if config["stage"] in ("loaded", "printing"):
            terminal.wait("synthetic-model-00", timeout=5)
            terminal.wait("p:", timeout=5)
        assert terminal.child.poll() is None, "viewer exited before its input action"
        printed_before_signal = None
        if config["stage"] == "printing":
            mark = len(terminal.raw)
            terminal.send(b"p")
            deadline = time.monotonic() + 3
            while True:
                tail = bytes(terminal.raw[mark:])
                reset = tail.rfind(b"\x1b[?2004l")
                printed = tail[reset + len(b"\x1b[?2004l"):] if reset >= 0 else b""
                if b"synthetic-model-00" in printed and b"Created: 1700000000" in printed:
                    break
                assert terminal.child.poll() is None, "printing finished before the signal boundary"
                assert time.monotonic() < deadline, "post-viewer printing was not observed"
                terminal.read(0.02)
            # Stop draining while the remaining metadata exceeds the PTY buffer.
            # The reset and complete-record fields prove Run returned to finish.
            assert len(printed) < config["print_bytes"] // 2, "too much output drained before the signal"
            assert b"synthetic-model-39" not in printed, "printing already reached its final record"
            assert terminal.child.poll() is None, "printing exited before the signal"
            printed_before_signal = len(printed)
        deadline = time.monotonic() + config["delay"]
        while time.monotonic() < deadline:
            terminal.read(max(0, min(0.001, deadline - time.monotonic())))
        # The first footer can arrive with an already loaded frame. Record the
        # observed state instead of claiming every startup case precedes loading.
        loaded_at_signal = "synthetic-model-00" in terminal.text() and "p:" in terminal.text()
        started = time.monotonic()
        action = config["action"]
        if action in ("SIGINT", "SIGTERM"):
            os.kill(terminal.child.pid, getattr(signal, action))
        else:
            terminal.send({"ctrl-c": b"\x03", "q": b"q", "escape": b"\x1b", "p": b"p"}[action])
        terminal.finish(config["expected"], timeout=3)
        if action == "p":
            # Printing must retain complete records, including those outside
            # the initial viewport, after the terminal leaves raw mode.
            after = bytes(terminal.raw).rsplit(b"\x1b[?2004l", 1)[-1]
            for index in range(40):
                assert f"synthetic-model-{index:02d}".encode() in after, "printed models were incomplete"
            assert b"Created: 1700000000" in after, "printing discarded model metadata"
        result = {"stage": config["stage"], "action": action,
                  "loaded_at_signal": loaded_at_signal,
                  "status": terminal.child.returncode, "restored": True,
                  "seconds": round(time.monotonic() - started, 4)}
        if printed_before_signal is not None:
            result["printed_bytes_before_signal"] = printed_before_signal
        print(json.dumps(result))
    finally:
        terminal.close()


if __name__ == "__main__":
    main()
