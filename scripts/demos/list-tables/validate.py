#!/usr/bin/env python3
"""Compare public commands against synthetic responses without inherited secrets."""

import errno
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import select
import struct
import subprocess
import sys
import termios
import time
import fcntl


COMMANDS = {
    "files": ["files", "list"],
    "batches": ["batches", "list"],
    "projects": ["admin", "organization", "projects", "list"],
}
IDS = {
    "files": ["file-demo-support", "file-demo-research-20261007"],
    "batches": ["batch-demo-support", "batch-demo-research-20261007"],
    "projects": ["proj_demo_support", "proj_demo_research_20261007"],
}
HEADERS = {
    "files": ["ID", "FILENAME", "PURPOSE", "SIZE", "STATUS"],
    "batches": ["ID", "STATUS"],
    "projects": ["ID", "NAME", "STATUS"],
}
MODES = {
    "auto": [],
    "text": ["--format", "text"],
    "json": ["--format", "json"],
    "jsonl": ["--format", "jsonl"],
    "pretty": ["--format", "pretty"],
    "raw": ["--format", "raw"],
    "yaml": ["--format", "yaml"],
    "transform": ["--transform", "id"],
    "raw-output": ["--transform", "id", "--raw-output"],
    "raw-output-only": ["--raw-output"],
    "auto-mixed": ["--format", "AuTo"],
    "text-mixed": ["--format", "TEXT"],
    "json-mixed": ["--format", "JSON"],
    "auto-transform": ["--format", "auto", "--transform", "id"],
}


def run(binary, args, endpoint, tty, width=110):
    env = {
        "PATH": "/usr/bin:/bin", "LANG": "en_US.UTF-8", "TERM": "xterm-256color",
        "OPENAI_API_KEY": "synthetic-demo-key", "OPENAI_ADMIN_KEY": "synthetic-demo-admin-key",
        "OPENAI_BASE_URL": endpoint, "NO_COLOR": "1", "FORCE_COLOR": "0",
        "GOMAXPROCS": "2",
    }
    if not tty:
        result = subprocess.run([binary, *args], env=env, stdin=subprocess.DEVNULL,
                                capture_output=True, timeout=15, check=False)
        return result.returncode, result.stdout, result.stderr
    master, slave = pty.openpty()
    process = None
    try:
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 48, width, 0, 0))
        process = subprocess.Popen([binary, *args], env=env, stdin=slave,
                                   stdout=slave, stderr=subprocess.PIPE)
        os.close(slave)
        slave = -1
        output = bytearray()
        deadline = time.monotonic() + 15
        while True:
            if time.monotonic() > deadline:
                raise TimeoutError("TTY machine comparison exceeded 15 seconds")
            if select.select([master], [], [], 0.2)[0]:
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    break
                if not chunk:
                    break
                output.extend(chunk)
        _, stderr = process.communicate(timeout=2)
        return process.returncode, bytes(output), stderr
    finally:
        os.close(master)
        if slave >= 0:
            os.close(slave)
        if process is not None and process.poll() is None:
            process.kill()
            process.communicate()


def main():
    if len(sys.argv) not in {5, 7}:
        raise SystemExit("usage: validate.py BEFORE_BINARY AFTER_BINARY API_URL OUTPUT_DIR [--resource RESOURCE]")
    before, after, url, output_arg = sys.argv[1:5]
    resources = list(COMMANDS)
    if len(sys.argv) == 7:
        if sys.argv[5] != "--resource" or sys.argv[6] not in COMMANDS:
            raise SystemExit("expected --resource files|batches|projects")
        resources = [sys.argv[6]]
    output = Path(output_arg)
    width = json.loads((output / f"{resources[0]}-after.cast").read_text().splitlines()[0])["width"]
    for resource in resources:
        for version in ("before", "after"):
            scene = f"{resource}-{version}"
            cast = [json.loads(line) for line in (output / f"{scene}.cast").read_text().splitlines()]
            capture = "".join(event[2] for event in cast[1:] if event[1] == "o")
            command_marker = "$ openai " + " ".join(COMMANDS[resource]) + "\r\n"
            assert capture.count(command_marker) == 1, (scene, "ambiguous command boundary")
            relayed = capture.split(command_marker, 1)[1]
            annotation_marker = "$ "
            assert relayed.count(annotation_marker) == 1, (scene, "ambiguous prompt boundary")
            relayed = relayed.split(annotation_marker, 1)[0]
            evidence = json.loads((output / f"{scene}.relay.json").read_text())
            assert evidence["exit_status"] == 0, (scene, "child exit status", evidence)
            assert hashlib.sha256(relayed.encode()).hexdigest() == evidence["sha256"], (scene, "capture changed CLI bytes")
    print(f"PASS {len(resources) * 2} exact terminal relay digests and child exit statuses")
    machine = output / "machine"
    machine.mkdir()
    requests = output / "requests.jsonl"
    checks = 0

    def compare(name, resource, mode, scenario="normal", tty=False, expected=0, count=1, extra=()):
        nonlocal checks
        results = []
        for version, binary in [("before", before), ("after", after)]:
            start = len(requests.read_text().splitlines())
            result = run(binary, [*MODES[mode], *COMMANDS[resource], *extra], f"{url}/{scenario}/v1", tty, width=width)
            events = [json.loads(line) for line in requests.read_text().splitlines()[start:]]
            (machine / f"{name}-{version}.stdout").write_bytes(result[1])
            (machine / f"{name}-{version}.stderr").write_bytes(result[2])
            (machine / f"{name}-{version}.status").write_text(f"{result[0]}\n")
            (machine / f"{name}-{version}.requests.json").write_text(json.dumps(events, indent=2) + "\n")
            assert result[0] == expected, (name, version, "exit status", result[0], expected)
            assert len(events) == count, (name, version, "request count", events)
            expected_events = [{
                "scenario": scenario, "resource": "organization/projects" if resource == "projects" else resource,
                "page": page, "status": 400 if scenario == "error" or scenario == "partial" and page == 2 else 200,
                "cursor_valid": True,
            } for page in range(1, count + 1)]
            assert events == expected_events, (name, version, "request sequence", events, expected_events)
            results.append(result)
        assert results[0] == results[1], (name, "before/after output differs")
        checks += 1
        print(f"PASS {name}: identical stdout/stderr/status; {count} request(s) per binary")

    for resource in resources:
        for mode in MODES:
            compare(f"pipe-{resource}-{mode}", resource, mode)
        for mode in MODES:
            if mode not in {"auto", "auto-mixed"}:
                compare(f"tty-{resource}-{mode}", resource, mode, tty=True)
        for maximum in (-1, 0, 1):
            compare(f"pipe-{resource}-maximum-{maximum}", resource, "auto", extra=("--max-items", str(maximum)))
        for scenario in ("empty", "controls", "unknown"):
            compare(f"pipe-{resource}-{scenario}", resource, "auto", scenario=scenario)
        compare(f"pipe-{resource}-error", resource, "json", scenario="error", expected=1)
        compare(f"pipe-{resource}-zero-error", resource, "auto", scenario="error", expected=1, extra=("--max-items", "0"))
        compare(f"pipe-{resource}-multipage", resource, "json", scenario="multipage", count=2)
        compare(f"pipe-{resource}-partial", resource, "json", scenario="partial", expected=1, count=2)
    for resource in resources:
        ids = IDS[resource]
        for version in ["before", "after"]:
            transcript = (output / f"{resource}-{version}.txt").read_text()
            for resource_id in ids:
                assert resource_id in transcript, (resource, version, "missing full ID", resource_id)
        cast = output / f"{resource}-after.cast"
        assert json.loads(cast.read_text().splitlines()[0])["width"] == width, "resource capture widths differ"
        transcript = (output / f"{resource}-after.txt").read_text()
        if width == 110 or resource == "batches":
            header = r"\s+".join(HEADERS[resource])
            assert re.search(header, transcript), (resource, "missing table headers", width)
        else:
            label = "Filename:" if resource == "files" else "Name:"
            assert label in transcript, (resource, "missing narrow fallback label")
    print(f"PASS {checks} machine comparisons and {len(resources) * 2} terminal transcript ID checks")
    print(f"Explicit-mode PTY comparisons used width {width}; stdin/stdout were terminals")
    print(f"PASS {len(resources)} terminal table or narrow-fallback assertions")


if __name__ == "__main__":
    main()
