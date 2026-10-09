#!/usr/bin/env python3
"""Compare built public entrypoints in isolated PTYs. No third-party modules."""

import argparse
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import struct
import subprocess
import tempfile
import termios
import time


def run(binary, args, width=80, streams=(True, True, True), extra=None):
    with tempfile.TemporaryDirectory(prefix="welcome-check-") as directory:
        root = Path(directory)
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 250, width, 0, 0))
        env = {
            "PATH": "/usr/bin:/bin", "HOME": directory, "USERPROFILE": directory,
            "XDG_CONFIG_HOME": directory, "XDG_CACHE_HOME": directory,
            "TERM": "xterm-256color", "LANG": "en_US.UTF-8",
            "OPENAI_BASE_URL": "not%url", "OPENAI_API_KEY": "", "OPENAI_ADMIN_KEY": "",
            "OPENAI_MTLS_CLIENT_CERT_FILE": "/synthetic/missing-cert.pem",
            "OPENAI_MTLS_CLIENT_KEY_FILE": "/synthetic/missing-key.pem",
        }
        env.update(extra or {})
        start = time.monotonic()
        child = subprocess.Popen(
            ["openai", *args], executable=str(binary), cwd=directory, env=env,
            stdin=slave if streams[0] else subprocess.PIPE,
            stdout=slave if streams[1] else subprocess.PIPE,
            stderr=slave if streams[2] else subprocess.PIPE,
        )
        if child.stdin is not None:
            child.stdin.close()
        os.close(slave)
        descriptors = {master: "terminal"}
        for name in ("stdout", "stderr"):
            stream = getattr(child, name)
            if stream is not None:
                descriptors[stream.fileno()] = name
        output = {"terminal": bytearray(), "stdout": bytearray(), "stderr": bytearray()}
        try:
            while descriptors:
                if time.monotonic() - start > 15:
                    raise TimeoutError(f"welcome timed out: {args!r}")
                ready, _, _ = select.select(list(descriptors), [], [], 0.1)
                for fd in ready:
                    try:
                        chunk = os.read(fd, 65536)
                    except OSError as error:
                        if fd != master or error.errno != errno.EIO:
                            raise
                        chunk = b""
                    if chunk:
                        output[descriptors[fd]].extend(chunk)
                    else:
                        del descriptors[fd]
            code = child.wait(timeout=5)
        finally:
            if child.poll() is None:
                child.kill()
                child.wait()
            os.close(master)
            for stream in (child.stdout, child.stderr):
                if stream is not None:
                    stream.close()
        state = sorted(str(path.relative_to(root)) for path in root.rglob("*"))
        return {"code": code, "state": state, **{
            name: bytes(data).decode("utf-8").replace("\r\n", "\n")
            for name, data in output.items()
        }}


def plain(text):
    return re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", text)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    parser.add_argument("output", type=Path)
    options = parser.parse_args()
    before, after = options.before.resolve(), options.after.resolve()
    options.output.mkdir(parents=True, exist_ok=False)
    cases = []
    for width in (80, 40, 29, 28, 20):
        cases.append((f"bare-{width}", [], width, (True, True, True), {}, width >= 29))
    for name, extra, banner in (
        ("no-color", {"NO_COLOR": "anything", "CLICOLOR_FORCE": "1"}, True),
        ("force-color-zero", {"FORCE_COLOR": "0"}, True),
        ("dumb", {"TERM": "dumb"}, False), ("unset-term", {"TERM": ""}, False),
        ("ci", {"CI": "true"}, False), ("github-ci", {"GITHUB_ACTIONS": "true"}, False),
        ("ci-false", {"CI": "false"}, True),
    ):
        cases.append((name, [], 80, (True, True, True), extra, banner))
    for index, args in enumerate((
        ["--help"], ["-h"], ["--h"], ["help"], ["--"], [""], ["--help=false"],
        ["--version"], ["--format", "json"], ["--format", "jsonl"], ["--debug"],
        ["--format-error", "json"], ["--raw-output"], ["--transform", "id"],
        ["models"], ["models", "list", "--help"], ["help", "models"],
        ["--format", "json", "--help"], ["--invalid-welcome-probe"],
        ["__complete", "--", "mo"],
    )):
        cases.append((f"explicit-{index}", args, 80, (True, True, True), {}, False))
    for name, streams in (
        ("stdin-pipe", (False, True, True)), ("stdout-pipe", (True, False, True)),
        ("stderr-pipe", (True, True, False)), ("all-pipes", (False, False, False)),
    ):
        cases.append((name, [], 80, streams, {}, False))
    version = run(after, ["--version"], streams=(False, False, False))["stdout"].strip().split()[-1]
    results = []
    for name, args, width, streams, extra, banner in cases:
        old = run(before, args, width, streams, extra)
        new = run(after, args, width, streams, extra)
        (options.output / f"{name}.json").write_text(json.dumps({"before": old, "after": new}, indent=2))
        assert old["code"] == new["code"], (name, "exit status changed")
        assert old["state"] == new["state"] == [], (name, "unexpected state")
        comparable = dict(new)
        if banner:
            header, body = new["terminal"].split("\n\n", 1)
            header = plain(header)
            assert "What are we making today?" in header, (name, "missing greeting")
            assert version in header, (name, "missing runtime version")
            assert len(header.splitlines()) == 4, (name, "header is too tall")
            assert all(len(line) <= width for line in header.splitlines()), (name, "header overflows")
            comparable["terminal"] = body
            if name in ("no-color", "force-color-zero"):
                assert "\x1b" not in new["terminal"], (name, "color was not disabled")
        else:
            assert "What are we making today?" not in str(new), (name, "unexpected banner")
        assert comparable == old, (name, "existing help/output changed")
        results.append({"case": name, "args": args, "width": width, "banner": banner, "status": "pass"})
    (options.output / "results.json").write_text(json.dumps(results, indent=2) + "\n")
    print(f"PASS: {len(results)} before/after public-entrypoint comparisons; runtime version {version}")


if __name__ == "__main__":
    main()
