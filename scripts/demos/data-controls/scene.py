#!/usr/bin/env python3
"""Run real CLI commands with bounded execution and explicit JSON evidence."""

import json
import os
import pathlib
import subprocess
import sys
import time


COMMANDS = {
    "retention": ["admin:organization:projects:data-retention", "retrieve", "--project-id", "proj_demo"],
    "pending": ["admin:organization:external-storage", "validate", "--external-storage-id", "ext_requested"],
    "validated": ["admin:organization:external-storage", "retrieve", "--external-storage-id", "ext_returned"],
}


def main():
    if not all(os.isatty(fd) for fd in (0, 1, 2)):
        raise RuntimeError("scene requires terminal input and output")
    mode = os.environ["DEMO_MODE"]
    name = os.environ["DEMO_CASE"]
    if mode not in {"before", "after"} or name not in COMMANDS:
        raise ValueError("unsupported demo scene")
    output = pathlib.Path(os.environ["DEMO_OUTPUT"])
    command = ["openai", *COMMANDS[name]]
    print("\033[2J\033[H" + os.environ["DEMO_SCENE_LABEL"], flush=True)
    print("Synthetic loopback API | no cloud changes\n", flush=True)
    if name == "pending":
        print("Live validate writes cloud test objects; successful validation activates retention.\n", flush=True)
    print("$ " + " ".join(command), flush=True)
    result = subprocess.run(command, timeout=15, check=False)
    with (output / "statuses.jsonl").open("a", encoding="utf-8") as log:
        log.write(json.dumps({"scene": f"{mode}-{name}", "format": "default", "status": result.returncode}) + "\n")
    if result.returncode:
        raise SystemExit(result.returncode)
    json_command = ["openai", "--format", "json", *COMMANDS[name]]
    explicit = subprocess.run(json_command, capture_output=True, timeout=15, check=False)
    (output / f"{mode}-{name}.json").write_bytes(explicit.stdout)
    (output / f"{mode}-{name}.stderr").write_bytes(explicit.stderr)
    with (output / "statuses.jsonl").open("a", encoding="utf-8") as log:
        log.write(json.dumps({"scene": f"{mode}-{name}", "format": "json", "status": explicit.returncode}) + "\n")
    if explicit.returncode:
        raise SystemExit(explicit.returncode)
    if name == "retention":
        print("\n$ " + " ".join(json_command), flush=True)
        if sys.stdout.buffer.write(explicit.stdout) != len(explicit.stdout):
            raise OSError("short JSON display write")
        sys.stdout.buffer.flush()
    print("\n$ ", end="", flush=True)
    time.sleep(2)


if __name__ == "__main__":
    main()
