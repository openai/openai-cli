#!/usr/bin/env python3
"""Render retained presenter diagnostics; never run the CLI or send a signal.

Recipe (requires existing agg and ffmpeg installations):
  python3 scripts/demos/record-image-save-cancellation.py --packet RETAINED_PACKET \
    --output FRESH_OUTPUT --agg /path/to/agg --ffmpeg /path/to/ffmpeg

Input: reproduction.jsonl, RESULTS.json, CORE_COMMIT_BINDING.json, and fixed/{normal.jsonl,RESULTS.json}.
The output is a synthetic public-presenter replay, not a native terminal recording.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import textwrap
import time


TEST = "TestSavedImageCancellationPreservesRecovery"
PACKAGE = "github.com/openai/openai-cli/pkg/custom"
PATTERN = re.compile(
    r'interrupted=false status_contract=(\d+) kept_bytes=(\d+) '
    r'canceled_second_bytes=(\d+) receipt_bytes=(\d+) diagnostic=("(?:[^"\\]|\\.)*")\n$')
BEFORE = "Request canceled.\n"
AFTER = {
    "false": "Saved 1 image(s), but could not save the entire response. "
             "The listed files are kept; do not generate those images again.\n",
    "true": "Saved 1 image(s), but could not save the entire response or print all saved paths. "
            "Check the output folder before generating again.\n",
}
WIDTH, HEIGHT = 92, 23


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def observations(path, state):
    rows = [json.loads(line) for line in path.read_text().splitlines() if line]
    result = {}
    for failed_receipt in ("false", "true"):
        name = f"{TEST}/text/receipt_failure={failed_receipt}"
        selected = [row for row in rows if row.get("Test") == name and row.get("Package") == PACKAGE]
        outcomes = [row["Action"] for row in selected if row["Action"] in ("pass", "fail", "skip")]
        if outcomes != [state]:
            raise ValueError(f"Expected one {state} result for {name}")
        matches = [PATTERN.search(row.get("Output", "")) for row in selected]
        matches = [match for match in matches if match]
        if len(matches) != 1:
            raise ValueError(f"Expected one interrupted=false observation for {name}")
        status, kept, canceled, receipt = map(int, matches[0].groups()[:4])
        diagnostic = json.loads(matches[0].group(5))
        expected = BEFORE if state == "fail" else AFTER[failed_receipt]
        if diagnostic != expected or (status, kept, canceled) != (1, 74, 16):
            raise ValueError(f"Unexpected diagnostic or file-state contract for {name}")
        if (receipt == 0) != (failed_receipt == "true"):
            raise ValueError(f"Receipt observation contradicts {name}")
        result[failed_receipt] = {
            "test": name, "test_result": state, "interrupted": False,
            "status_contract": status, "kept_bytes": kept, "canceled_second_bytes": canceled,
            "receipt_bytes": receipt, "diagnostic": diagnostic,
        }
    parent = [row["Action"] for row in rows if row.get("Test") == TEST
              and row.get("Package") == PACKAGE and row["Action"] in ("pass", "fail", "skip")]
    package = [row["Action"] for row in rows if not row.get("Test")
               and row.get("Package") == PACKAGE and row["Action"] in ("pass", "fail", "skip")]
    if parent != [state] or package != [state]:
        raise ValueError("Parent or package result does not match the selected observations")
    return result


def scene(label, commit, observed):
    lines = [f"Image-save cancellation | {label.upper()} {commit[:7]}",
             "Synthetic public-presenter replay",
             "No CLI command, native signal, or terminal recording.", "",
             "Observed file state (both cases)", "  First PNG: 74 bytes kept.",
             "  Second file: canceled after 16 bytes; removed.", ""]
    for failed, caption in (("false", "Saved-path receipt succeeds"), ("true", "Saved-path receipt fails")):
        lines.append(caption)
        wrapped = textwrap.wrap(observed[failed]["diagnostic"].rstrip("\n"), width=WIDTH - 4)
        if len(wrapped) > 3:
            raise ValueError("Diagnostic exceeds the reviewed scene layout")
        lines.extend("  " + line for line in wrapped)
        lines.extend([""] * (4 - len(wrapped)))
    lines.append("Retained test output; replay timing is illustrative.")
    if len(lines) >= HEIGHT or any(len(line) > WIDTH for line in lines):
        raise ValueError("Scene does not fit the declared canvas")
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--packet", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--agg", required=True, type=Path)
    parser.add_argument("--ffmpeg", required=True, type=Path)
    args = parser.parse_args()
    packet, output = args.packet.resolve(), args.output.resolve()
    repository = Path(__file__).resolve().parents[2]
    if output.is_relative_to(repository) or (output.exists() and any(output.iterdir())):
        raise ValueError("Use a fresh output directory outside the repository")
    inputs = ["reproduction.jsonl", "RESULTS.json", "fixed/normal.jsonl",
              "fixed/RESULTS.json", "CORE_COMMIT_BINDING.json"]
    hashes = {name: digest(packet / name) for name in inputs}
    binding = json.loads((packet / "CORE_COMMIT_BINDING.json").read_text())
    baseline = json.loads((packet / "RESULTS.json").read_text())
    fixed = json.loads((packet / "fixed/RESULTS.json").read_text())
    normal = [item for item in fixed["commands"] if item["name"] == "normal"]
    if (binding["base"] != baseline["base"] or binding["base"] != fixed["base"]
            or not binding["all_849_tested_source_hashes_match"] or baseline["go_status"] != 1
            or len(normal) != 1 or normal[0]["status"] != 0
            or normal[0]["log_sha256"] != hashes["fixed/normal.jsonl"]):
        raise ValueError("Retained source binding or validation receipts disagree")
    for field in ("base", "candidate"):
        if not re.fullmatch(r"[0-9a-f]{40}", binding[field]):
            raise ValueError("Expected full source commit identities")
    evidence = {"before": observations(packet / "reproduction.jsonl", "fail"),
                "after": observations(packet / "fixed/normal.jsonl", "pass")}
    tools = {"agg": args.agg.resolve(strict=True), "ffmpeg": args.ffmpeg.resolve(strict=True)}
    output.mkdir(parents=True, exist_ok=True)
    metadata = {
        "state": "running", "scope": "Synthetic public-presenter replay from retained test diagnostics.",
        "not_established": ["CLI invocation", "native Ctrl+C or process exit", "PTY or terminal appearance"],
        "base": binding["base"], "candidate": binding["candidate"],
        "render_host": {"system": platform.system(), "machine": platform.machine()},
        "input_sha256": hashes, "script_sha256": digest(Path(__file__).resolve()),
        "shared_render_recipe_sha256": digest(repository / "scripts/demos/capture_and_render.sh"),
        "layout": {"columns": WIDTH, "rows": HEIGHT, "font": "Menlo", "font_size": 18},
        "tools": {name: {"path": str(path), "sha256": digest(path)} for name, path in tools.items()},
        "calls": [],
    }

    def interrupted(number, _frame):
        raise SystemExit(128 + number)

    signal.signal(signal.SIGTERM, interrupted)

    def run(argv, tag):
        started = time.monotonic()
        record = {"argv": [str(arg) for arg in argv], "forced_cleanup": []}
        metadata["calls"].append(record)
        with (output / f"{tag}.stdout").open("wb") as stdout, (output / f"{tag}.stderr").open("wb") as stderr:
            child = subprocess.Popen(record["argv"], cwd=output, stdin=subprocess.DEVNULL,
                                     stdout=stdout, stderr=stderr, start_new_session=True,
                                     env={"PATH": "/usr/bin:/bin", "HOME": str(output), "LANG": "en_US.UTF-8"})
            try:
                child.wait(timeout=45)
            except BaseException:
                if child.poll() is None:
                    record["forced_cleanup"].append("TERM")
                    try:
                        os.killpg(child.pid, signal.SIGTERM)
                    except ProcessLookupError:
                        # The process group can exit between poll() and SIGTERM.
                        pass
                    try:
                        child.wait(timeout=2)
                    except subprocess.TimeoutExpired:
                        record["forced_cleanup"].append("KILL")
                        try:
                            os.killpg(child.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            # The process group can exit after the wait times out.
                            pass
                        child.wait(timeout=2)
                raise
            finally:
                record.update(exit_code=child.returncode, elapsed_seconds=round(time.monotonic() - started, 3))
        if child.returncode:
            raise RuntimeError(f"{tag} failed; inspect its retained renderer log")
        return (output / f"{tag}.stdout").read_text()

    try:
        metadata["tools"]["agg"]["version"] = run([tools["agg"], "--version"], "agg-version").splitlines()[0]
        metadata["tools"]["ffmpeg"]["version"] = run([tools["ffmpeg"], "-version"], "ffmpeg-version").splitlines()[0]
        (output / "observations.json").write_text(json.dumps(evidence, indent=2) + "\n")
        for label, commit in (("before", binding["base"]), ("after", binding["candidate"])):
            text = scene(label, commit, evidence[label])
            (output / f"{label}.txt").write_text(text)
            header = {"version": 2, "width": WIDTH, "height": HEIGHT, "title": f"{label}: synthetic presenter replay"}
            event = [0, "o", "\u001b[?25l" + text.replace("\n", "\r\n")]
            (output / f"{label}.cast").write_text(json.dumps(header) + "\n" + json.dumps(event) + "\n")
            render = [tools["agg"], "--quiet", "--font-family", "Menlo", "--font-size", "18",
                      "--line-height", "1.2", "--theme", "dracula", "--fps-cap", "10", "--last-frame-duration", "5"]
            run([*render, f"{label}.cast", f"{label}.gif"], f"{label}-render")
            run([*render, "--select", "100%", f"{label}.cast", f"{label}-frame.gif"], f"{label}-frame")
            run([tools["ffmpeg"], "-hide_banner", "-loglevel", "error", "-y", "-i", f"{label}-frame.gif",
                 "-frames:v", "1", f"{label}.png"], f"{label}-png")
        # Match capture_and_render.sh: join separately rendered scenes, not cleared terminal frames.
        (output / "comparison-scenes.txt").write_text("file 'before.gif'\nfile 'after.gif'\n")
        run([tools["ffmpeg"], "-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "1",
             "-i", "comparison-scenes.txt", "-filter_complex", "[0:v]split[a][b];[a]palettegen[p];[b][p]paletteuse",
             "-vsync", "0", "-gifflags", "0", "-loop", "0", "-final_delay", "500", "comparison.gif"], "comparison")
        if hashes != {name: digest(packet / name) for name in inputs}:
            raise RuntimeError("Input evidence changed while rendering")
        metadata["state"] = "rendered; independent visual review pending"
    except BaseException as error:
        metadata["state"] = "failed; partial artifacts retained"
        metadata["failure_type"] = type(error).__name__
        raise
    finally:
        metadata["artifacts"] = {path.name: {"sha256": digest(path), "bytes": path.stat().st_size}
                                 for path in sorted(output.iterdir()) if path.is_file() and path.name != "metadata.json"}
        (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")


if __name__ == "__main__":
    main()
