#!/usr/bin/env python3
"""Check every visible command in a real PTY at narrow and normal widths."""
import concurrent.futures
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import struct
import subprocess
import sys
import tempfile
import termios
import time

catalog_path, binary, output = map(Path, sys.argv[1:])
catalog = json.loads(catalog_path.read_text())
output.mkdir(parents=True, exist_ok=True)
ansi = re.compile(rb"\x1b\[[0-9;]*m")

with tempfile.TemporaryDirectory() as state:
    env = {"PATH": "/usr/bin:/bin", "HOME": state, "XDG_CONFIG_HOME": state,
           "XDG_CACHE_HOME": state, "OPENAI_CLI_NO_UPDATE_CHECK": "1",
           "OPENAI_BASE_URL": "http://127.0.0.1:1/v1", "TERM": "xterm-256color",
           "COLORTERM": "truecolor", "COLORFGBG": "15;0", "LANG": "en_US.UTF-8"}

    def capture(item):
        index, record, width = item
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 60, width, 0, 0))
        attrs = termios.tcgetattr(slave)
        attrs[1] &= ~termios.ONLCR
        termios.tcsetattr(slave, termios.TCSANOW, attrs)
        process = subprocess.Popen(["openai", *record["Path"], "--help"], executable=str(binary.resolve()),
                                   stdin=slave, stdout=slave, stderr=slave, env=env, start_new_session=True)
        os.close(slave)
        chunks = []
        deadline = time.monotonic() + 20
        try:
            while time.monotonic() < deadline:
                if select.select([master], [], [], 0.1)[0]:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError:
                        break
                    if not chunk:
                        break
                    chunks.append(chunk)
                elif process.poll() is not None:
                    break
            code = process.wait(timeout=2)
        finally:
            os.close(master)
            if process.poll() is None:
                process.kill()
                process.wait()
        data = b"".join(chunks)
        (output / f"{index:04}-{width}.ansi").write_bytes(data)
        text = ansi.sub(b"", data).decode()
        issues = []
        if code:
            issues.append({"kind": "exit", "code": code})
        if "\x1b" in text:
            issues.append({"kind": "unexpected_escape"})
        if re.search(rb"\x1b\[(?:1|4)(?:;|m)", data):
            issues.append({"kind": "unexpected_emphasis"})
        for line_number, line in enumerate(text.splitlines(), 1):
            if "\t" in line or line.rstrip() != line:
                issues.append({"kind": "whitespace", "line": line_number})
            if len(line) > width:
                issues.append({"kind": "width", "line": line_number, "text": line})
        return {"index": index, "path": record["Path"], "width": width, "issues": issues}

    tasks = [(index, record, width) for index, record in enumerate(catalog)
             if not record["Hidden"] for width in [40, 80]]
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        results = list(pool.map(capture, tasks))
    state_files = [str(p.relative_to(state)) for p in Path(state).rglob("*")]
(output / "audit.json").write_text(json.dumps({"results": results, "state_files": state_files}, indent=2) + "\n")
print(f"Captured {len(results)} terminal help pages. Review width exceptions in audit.json.")
