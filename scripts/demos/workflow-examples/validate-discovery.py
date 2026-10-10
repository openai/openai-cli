#!/usr/bin/env python3
"""Validate complete discovery captures and the copied, local-only recovery."""
import json
from pathlib import Path
import sys


def require(condition, message):
    if not condition:
        raise SystemExit(message)


def prepare(root):
    before = (root / "before-index.stdout").read_bytes()
    after = (root / "after-index.stdout").read_bytes()
    require(len(before.splitlines()) == 64, "Prior draft no longer produces the verified 64-line discovery output")
    require(len(after.splitlines()) == 6, "Candidate discovery must contain exactly six lines")
    for topic in (b"files", b"audio", b"models", b"--help"):
        require(b"openai examples " + topic in after, "Missing copyable discovery command")
    for role in ("before", "after"):
        for probe in ("index", "files", "files-json"):
            require(not (root / f"{role}-{probe}.stderr").read_bytes(), "Unexpected successful-probe diagnostic")
        require(not (root / f"{role}-failure.stdout").read_bytes(), "Format failure unexpectedly produced stdout")
    for probe in ("files", "files-json"):
        require((root / f"before-{probe}.stdout").read_bytes() == (root / f"after-{probe}.stdout").read_bytes(),
                "Files recipe or JSON bytes changed")
    old_error = (root / "before-failure.stderr").read_text()
    error = (root / "after-failure.stderr").read_text()
    require("Try:" not in old_error, "Prior draft unexpectedly offers copied recovery")
    commands = [line.removeprefix("Try: ") for line in error.splitlines() if line.startswith("Try: ")]
    require(commands == ["openai examples files --format text"], "Recovery is not the allowed offline recipe command")
    # Store the diagnostic's exact command. The scene checks it before execution.
    (root / "recovery-command.txt").write_text(commands[0] + "\n")


def output_events(path, width):
    records = [json.loads(line) for line in path.read_text().splitlines()]
    require((records[0].get("width"), records[0].get("height")) == (width, 24), "Unexpected viewport: " + str(path))
    output = "".join(event[2] for event in records[1:] if event[1] == "o").replace("\r\n", "\n")
    return output


def check(root, runtime):
    prepare(root)
    recovery = (root / "recovery-command.txt").read_text().strip()
    counts = {}
    for profile, width in (("dark", 80), ("light", 80), ("no-color", 40)):
        folder = root / profile
        for role in ("before", "after"):
            label = role.upper()
            tail = f"\n# {label}: command finished\n$ "
            index = output_events(folder / f"{role}.cast", width)
            require(index.endswith(tail), "Missing complete discovery boundary")
            index_bytes = index.split("$ openai examples\n", 1)[1][:-len(tail)].encode()
            require(index_bytes == (root / f"{role}-index.stdout").read_bytes(), "Discovery output changed in the PTY")
            require((folder / f"{role}.status").read_text() == "index: 0\n", "Discovery status changed")
            counts[f"{profile}-{role}"] = len(index_bytes.splitlines())
            failure = output_events(folder / f"{role}-recovery.cast", width)
            require(failure.endswith(tail), "Missing complete recovery boundary")
            failure = failure.split("$ openai examples files --format yaml --format-error text\n", 1)[1][:-len(tail)]
            expected_error = (root / f"{role}-failure.stderr").read_text()
            require(failure.startswith(expected_error), "PTY diagnostic differs from the copied diagnostic")
            status = (folder / f"{role}-recovery.status").read_text()
            if role == "before":
                require(status == "format failure: 1\n", "Baseline failure status changed")
                require("Try:" not in failure, "Baseline capture contains invented recovery")
            else:
                require(status == "format failure: 1\ncopied recovery: 0\n", "Candidate failure or recovery status changed")
                prefix = expected_error + "\n$ " + recovery + "\n"
                require(failure.startswith(prefix), "Captured recovery did not copy the printed Try command")
                require(failure[len(prefix):].encode() == (root / "after-files.stdout").read_bytes(),
                        "Copied recovery did not print the original Files recipe")
            metadata = (folder / "metadata.txt").read_text()
            expected_status = 1 if role == "before" else 0
            require(f"{role}-recovery exit status: {expected_status} (expected {expected_status})" in metadata,
                    "Missing checked capture status")
    for home in runtime.glob("*-home"):
        require(not list(home.iterdir()), "A local command created files: " + str(home))
    result = {
        "result": "pass",
        "comparison": "prior published draft e0e2c883, runtime 664118a4, versus follow-up candidate",
        "discovery_line_counts": counts,
        "assertions": [
            "All discovery commands exit zero; prior output retains all 64 lines and candidate output has six lines.",
            "PTY discovery bytes match redirected output across dark, light, and NO_COLOR profiles.",
            "Unsupported YAML output exits one before and after; its stdout is empty.",
            "Copied Try recovery exactly matches the PTY diagnostic and prints unchanged Files recipe bytes.",
            "Files recipe and Files JSON bytes match the prior published draft.",
            "Local commands tolerate invalid remote configuration, require no credentials, and leave empty homes unchanged.",
        ],
        "limits": [
            "No API recipe executes, no API fixture runs, and no live endpoint is configured.",
            "Full raw output is retained; final viewport images show natural scroll rather than cropped help.",
            "Terminal replay does not establish native graphical terminal or font behavior.",
            "Media still requires visual inspection before publication.",
        ],
    }
    (root / "validation.json").write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    root = Path(sys.argv[2])
    if sys.argv[1] == "prepare":
        prepare(root)
    elif sys.argv[1] == "check":
        check(root, Path(sys.argv[3]))
    else:
        raise SystemExit("Expected prepare or check")
