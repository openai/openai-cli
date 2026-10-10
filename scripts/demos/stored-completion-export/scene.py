#!/usr/bin/env python3
"""Run the displayed command with real terminal stderr and verify its file."""

import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import sys
import time


def say(message=""):
    print(message, flush=True)


def main():
    if not all(os.isatty(fd) for fd in (0, 1, 2)):
        raise RuntimeError("scene requires terminal input, output, and errors")
    scene = os.environ["DEMO_SCENE"]
    if scene not in {"before", "after", "existing", "empty"}:
        raise ValueError("unknown demo scene")
    directory = pathlib.Path(os.environ["DEMO_DATA_DIR"])
    fixture = json.loads(pathlib.Path(os.environ["DEMO_FIXTURE"]).read_text())
    binary = shutil.which("openai")
    if binary is None:
        raise RuntimeError("selected CLI binary is missing")
    os.chdir(directory)
    destination = pathlib.Path("completions.jsonl")
    if destination.exists():
        raise RuntimeError("demo output directory must start empty")
    if scene == "existing":
        destination.write_bytes(b"Preserve this existing file.\n")

    sys.stdout.write("\033[2J\033[H")
    say(os.environ["DEMO_SCENE_LABEL"])
    say("Synthetic API | only completions created with store=true")
    say()
    time.sleep(0.4)
    if scene == "before":
        args = ["openai", "chat", "completions", "list", "--format", "jsonl", "--max-items", "-1", "--limit", "2"]
        say("$ openai chat completions list --format jsonl \\")
        say("    --max-items -1 --limit 2 > completions.jsonl")
        with destination.open("xb") as output:
            result = subprocess.run(args, stdin=subprocess.DEVNULL, stdout=output, timeout=15, check=False)
    else:
        args = ["openai", "chat", "completions", "export", "--limit", "2", "--output", str(destination)]
        say("$ openai chat completions export --limit 2 \\")
        say("    --output completions.jsonl")
        result = subprocess.run(args, stdin=subprocess.DEVNULL, timeout=15, check=False)
    expected_status = 1 if scene == "existing" else 0
    if result.returncode != expected_status:
        raise RuntimeError(f"CLI exit status {result.returncode}; expected {expected_status}")
    expected = b"Preserve this existing file.\n" if scene == "existing" else b"" if scene == "empty" else fixture["jsonl"].encode()
    actual = destination.read_bytes()
    if actual != expected:
        raise AssertionError("saved bytes differ from the synthetic fixture or previous file")
    if list(directory.glob(".openai-download-*.tmp")):
        raise AssertionError("CLI left an export stage after exit")
    if scene == "before":
        say("$ wc -l < completions.jsonl")
        with destination.open("rb") as source:
            subprocess.run(["wc", "-l"], stdin=source, timeout=5, check=True)
    elif scene == "existing":
        say("$ cat completions.jsonl")
        subprocess.run(["cat", str(destination)], stdin=subprocess.DEVNULL, timeout=5, check=True)
    evidence = {"scene": scene, "command": args, "exit_status": result.returncode,
                "binary_sha256": hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),
                "file_sha256": hashlib.sha256(actual).hexdigest(), "file_bytes": len(actual),
                "complete_records": len(actual.splitlines()) if scene != "existing" else None,
                "destination_preserved": scene == "existing", "temporary_files": []}
    pathlib.Path(os.environ["DEMO_EVIDENCE"]).write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
    say()
    sys.stdout.write("$ ")
    sys.stdout.flush()
    time.sleep(2)
    return result.returncode


if __name__ == "__main__":
    raise SystemExit(main())
