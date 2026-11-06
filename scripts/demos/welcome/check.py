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
import unicodedata


def run(binary, args, width=80, streams=(True, True, True), extra=None):
    with tempfile.TemporaryDirectory(prefix="welcome-check-") as directory:
        root = Path(directory)
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 250, width, 0, 0))
        attributes = termios.tcgetattr(slave)
        attributes[1] &= ~termios.ONLCR
        termios.tcsetattr(slave, termios.TCSANOW, attributes)
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
            name: bytes(data).decode("utf-8")
            for name, data in output.items()
        }}


def plain(text):
    return re.sub(r"\x1b\[[0-9;:]*m", "", text)


def cell_width(text):
    return sum(0 if unicodedata.combining(char) else
               2 if unicodedata.east_asian_width(char) in ("W", "F") else 1
               for char in text)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    parser.add_argument("output", type=Path)
    options = parser.parse_args()
    before, after = options.before.resolve(), options.after.resolve()
    options.output.mkdir(parents=True, exist_ok=False)
    cases = []
    for width in (100, 82, 81, 80, 76, 75, 66, 56, 55, 40, 39, 32, 31, 29, 28, 20):
        cases.append((f"bare-{width}", [], width, (True, True, True), {}, True))
    for name, extra, banner in (
        ("no-color", {"NO_COLOR": "anything", "CLICOLOR_FORCE": "1"}, True),
        ("force-color-zero", {"FORCE_COLOR": "0"}, True),
        ("dumb", {"TERM": "dumb"}, False), ("unset-term", {"TERM": ""}, False),
        ("ci", {"CI": "true"}, False), ("github-ci", {"GITHUB_ACTIONS": "true"}, False),
        ("ci-false", {"CI": "false"}, True),
        ("light-theme", {"COLORFGBG": "0;15", "COLORTERM": "truecolor"}, True),
        ("ansi16", {"TERM": "xterm-color"}, True),
        ("windows-terminal-env", {"TERM": "", "WT_SESSION": "synthetic-session"}, True),
        ("clicolor-zero", {"CLICOLOR": "0"}, True),
        ("ascii-locale", {"LC_ALL": "C"}, True),
        ("unknown-locale", {"LANG": ""}, True),
        ("ctype-ascii", {"LC_CTYPE": "C"}, True),
        ("utf8-precedence", {"LC_ALL": "C.UTF-8", "LC_CTYPE": "C"}, True),
        ("apple-terminal", {"TERM_PROGRAM": "Apple_Terminal"}, True),
        ("vscode", {"TERM_PROGRAM": "vscode"}, True),
        ("kitty", {"TERM_PROGRAM": "kitty"}, True),
        ("iterm", {"TERM_PROGRAM": "iTerm.app"}, True),
        ("ghostty", {"TERM_PROGRAM": "ghostty"}, True),
        ("wezterm", {"TERM_PROGRAM": "WezTerm"}, True),
        ("tmux", {"TMUX": "/synthetic/socket"}, True),
        ("screen", {"TERM": "screen", "STY": "synthetic"}, True),
        ("zellij", {"ZELLIJ": "synthetic"}, True),
    ):
        cases.append((name, [], 80, (True, True, True), extra, banner))
    for index, args in enumerate((
        ["--help"], ["-h"], ["--h"], ["help"], ["--"], [""], ["--help=false"],
        ["--version"], ["--format", "json"], ["--format", "jsonl"], ["--debug"],
        ["--format-error", "json"], ["--raw-output"], ["--transform", "id"],
        ["models"], ["models", "list", "--help"], ["help", "models"],
        ["--format", "json", "--help"], ["--invalid-welcome-probe"],
        ["__complete", "--", "mo"],
        ["--quiet"], ["--verbose"], ["--quiet", "--verbose"], ["--verbose", "--quiet"],
        ["--quiet=false", "--verbose=false"], ["--verbose", "help"],
        ["--quiet", "--format", "json", "--help"],
        ["files", "upload", "--help"], ["files", "get", "--help"], ["files", "download", "--help"],
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
        expect_ascii = name in ("ascii-locale", "unknown-locale", "ctype-ascii")
        displayed_version = "v" + version if version[0].isdigit() else version
        if expect_ascii:
            displayed_version = "".join(char if ord(char) < 128 else
                ("\\u%04x" if ord(char) <= 0xffff else "\\U%08x") % ord(char)
                for char in displayed_version)
        banner = banner and width >= max(29, cell_width("OpenAI CLI  " + displayed_version) + 4)
        old = run(before, args, width, streams, extra)
        new = run(after, args, width, streams, extra)
        (options.output / f"{name}.json").write_text(json.dumps({"before": old, "after": new}, indent=2))
        assert old["code"] == new["code"], (name, "exit status changed")
        assert old["state"] == new["state"] == [], (name, "unexpected state")
        comparable = dict(new)
        if banner:
            raw_header, body = new["terminal"].split("\n\n", 1)
            header = plain(raw_header)
            lines = header.splitlines()
            no_color = name in ("no-color", "force-color-zero", "clicolor-zero")
            version_width = cell_width(displayed_version)
            filled_width = max(76, 66 + version_width)
            filled = not expect_ascii and not no_color and width >= filled_width and version_width <= 16
            detailed = filled and name != "ansi16"
            legacy_wide = (expect_ascii or no_color) and width >= 56 and version_width <= 16
            height = 9 if filled else 6 if width >= 40 else 4
            title_row = 3 if detailed else 2 if filled else 1 if legacy_wide else 2 if width >= 40 else 1
            version_row = title_row + 1 if filled else 2 if legacy_wide else title_row
            assert "What are we making today?" in header, (name, "missing greeting")
            assert len(lines) == height, (name, "unexpected header height")
            assert "OpenAI CLI" in lines[title_row] and displayed_version in lines[version_row], (name, "identity/version wrapped")
            assert len({cell_width(line) for line in lines}) == 1, (name, "unequal frame widths")
            assert all(cell_width(line) <= width for line in lines), (name, "header overflows")
            assert "✦" not in header and (">_" in header) == detailed, (name, "incorrect signature")
            assert "\x1b" not in header, (name, "non-color terminal controls")
            assert "(o u o)" not in header and "~U U U U~" not in header, (name, "unexpected mascot")
            if filled:
                assert all(cell_width(line) == filled_width for line in lines), (name, "incorrect filled-card width")
                assert cell_width(lines[7].split("What are we making today?", 1)[0]) == (20 if detailed else 3), (name, "tagline moved")
                raw_rows = raw_header.splitlines()[1:7]
                fill_rows = raw_rows[1:5] if detailed else raw_rows
                assert all(re.search(r"\x1b\[(?:(?:4[0-7]|10[0-7])(?:[;m])|(?:[0-9]+;)*48[;:](?:2|5)[;:])", line) for line in fill_rows), (name, "missing filled pixel row")
                if detailed:
                    assert cell_width(lines[8].split(">_", 1)[0]) == filled_width - 9, (name, "cursor stamp moved")
                    for row, glyph in ((raw_rows[0], "▇"), (raw_rows[-1], "▂")):
                        assert glyph in row and re.search(r"\x1b\[(?:[0-9]+;)*4(?:;|m)", row), (name, "missing underlined cap")
                        assert re.search(r"\x1b\[(?:[0-9]+;)*38;(?:2|5);", row), (name, "missing cap color")
            elif legacy_wide:
                assert all(cell_width(line) == 56 for line in lines), (name, "legacy fallback width changed")
                if not expect_ascii:
                    assert "█" in header, (name, "monochrome wordmark missing")
            if not no_color and not expect_ascii:
                assert not any(glyph in header for glyph in "█▀▄"), (name, "colored artwork contains block glyphs")
            if expect_ascii:
                assert header.isascii(), (name, "non-ASCII fallback")
            comparable["terminal"] = body
            if no_color:
                assert "\x1b" not in new["terminal"], (name, "color was not disabled")
        else:
            assert "What are we making today?" not in str(new), (name, "unexpected banner")
        assert comparable == old, (name, "existing help/output changed")
        results.append({"case": name, "args": args, "width": width, "banner": banner, "status": "pass"})
    (options.output / "results.json").write_text(json.dumps(results, indent=2) + "\n")
    print(f"PASS: {len(results)} before/after public-entrypoint comparisons; runtime version {version}")


if __name__ == "__main__":
    main()
