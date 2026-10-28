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
        "models_navigation", pathlib.Path(__file__).with_name("check-list-navigation.py"))
    navigation = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(navigation)
    harness = navigation.terminal_module

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
        if config["stage"] == "loaded":
            terminal.wait("synthetic-model-00", timeout=5)
            first_model = terminal.raw.index(b"synthetic-model-00")
            terminal.wait("Space: more", timeout=5, after=first_model)
        assert terminal.child.poll() is None, "pager exited before its input action"
        deadline = time.monotonic() + config["delay"]
        while time.monotonic() < deadline:
            terminal.read(max(0, min(0.001, deadline - time.monotonic())))
        # The first footer can arrive with an already loaded frame. Record the
        # observed state instead of claiming every startup case precedes loading.
        loaded_at_signal = "synthetic-model-00" in terminal.text() and "Space: more" in terminal.text()
        started = time.monotonic()
        action = config["action"]
        if action in ("SIGINT", "SIGTERM"):
            os.kill(terminal.child.pid, getattr(signal, action))
        else:
            terminal.send({"ctrl-c": b"\x03", "q": b"q", "escape": b"\x1b"}[action])
        navigation.finish(terminal, config["expected"], timeout=3)
        result = {"stage": config["stage"], "action": action,
                  "loaded_at_signal": loaded_at_signal,
                  "status": terminal.child.returncode, "restored": True,
                  "seconds": round(time.monotonic() - started, 4)}
        print(json.dumps(result))
    finally:
        terminal.close()


if __name__ == "__main__":
    main()
