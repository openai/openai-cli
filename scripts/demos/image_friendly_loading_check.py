#!/usr/bin/env python3
"""Check delayed synthetic generation and record a failure-edit-retry scene."""

import argparse
import fcntl
import hashlib
import json
import pathlib
import re
import struct
import subprocess
import sys
import tempfile
import termios
import time
import unicodedata

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from image_recovery_check import Fixture, Process, PROMPT, environment, saved


RETAINED = b"Your prompt and settings are still here. Press Enter to try again."
FAILURE = b"Couldn't create your image. Try changing the prompt or settings."
OLD_RETAINED = b"Your draft is kept"
ANSI = re.compile(rb"\x1b(?:\[[0-?]*[ -/]*[@-~]|[78])")
BAR = re.compile(rb"\[(?:#|" + "━".encode() + rb"| )+\]")
SPINNER = re.compile(r"([\u2800-\u28ff|/\\-]) +Generating image")
MACHINE = {"json", "jsonl", "yaml", "raw", "transform", "error-json"}
PROMPTS = {
    "prompt-long": "A synthetic orange robot " * 32 + "END_OF_LONG_PROMPT",
    "prompt-unicode": "雪 e\u0301 🦉 rainbow " * 24,
    "prompt-controls": "O'neil\n\t\x1b]0;x\x07\u202e",
}


def loading_check(process, start, before, width, prompt=PROMPT):
    process.pump(2.2)
    pending = bytes(process.output[start:])
    label = b"Generating image" if width >= 20 else b"Working..."
    assert label in pending, "no visible activity during the delayed response"
    if width >= 20:
        assert pending.count(label) > 1, "activity stopped updating during the response"
        assert len(set(SPINNER.findall(pending.decode()))) > 1, "the visible spinner did not animate"
    assert b"Saved image:" not in pending and b"Couldn't create your image" not in pending
    assert b"%" not in pending, "the synthetic API does not report a completion percentage"
    if not before:
        assert not BAR.search(pending) and b"Estimated" not in pending, "the removed estimate bar is still visible"
    if not before and width >= 40:
        assert b"2s elapsed" in pending, "elapsed time did not advance"
    if not before and width >= 80:
        assert b"Ctrl+C to cancel" in pending, "cancellation guidance is missing"
    if not before and width >= 40:
        assert b"0s elapsed" in pending, "the request did not reset its elapsed timer"
        assert re.search(rb"Generating image '(?:\\.|[^'\r\n])*'", pending), "prompt quotation is missing or broken"
        if prompt == PROMPT and width >= 80:
            assert ("'" + prompt + "'").encode() in pending, "short prompt was not retained in the loading line"
        if len(prompt) > width:
            assert b"...'" in pending, "long prompt did not show bounded truncation"
            assert prompt.encode() not in pending, "full long prompt entered the loading frame"
        if prompt == PROMPTS["prompt-controls"]:
            for literal in (b"\\n", b"\\t", b"\\u001b", b"\\u202e", b"\\'"):
                assert literal in pending, "prompt control or quote did not remain visibly escaped"
        assert b"\x1b]" not in pending and b"\x07" not in pending and "\u202e".encode() not in pending
    # Cursor positioning may use softwrap instead of a literal newline. Check
    # each visible fragment here; inspect actual rows in the rendered replay.
    for frame in ANSI.split(pending):
        for line in frame.decode(errors="strict").splitlines():
            if "Generating image" in line or "elapsed" in line or BAR.search(line.encode()):
                line = line.lstrip()
                cells = sum(0 if unicodedata.combining(c) else 2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in line)
                assert cells < width, "loading feedback wraps in the narrow PTY"
                if not before and "elapsed" in line:
                    assert "[" not in line and "]" not in line, "the status line still contains a bar"


def assert_loading_cleared(output, before):
    # Completion remains as a plain result line. Only transient feedback
    # must disappear before it and the saved paths.
    completion = re.search(rb"\r\x1b\[J[^\r\n\x1b]*Images saved[^\r\n\x1b]*\r?\n", output)
    transient = output[:completion.start() + len(b"\r\x1b[J")] if completion else output
    markers = list(BAR.finditer(transient)) + list(re.finditer(rb"Generating image|Saving image|Images saved", transient))
    if markers:
        cleanup = transient[max(marker.end() for marker in markers):]
        assert b"\x1b[J" in cleanup or cleanup.count(b"\x1b[2K") >= 2, "the two loading rows were not cleared"
    else:
        assert b"\r\x1b[2K" in transient or b"\r\x1b[J" in transient, "the loading line was not cleared"


def assert_completion(output, before, succeeded, width=104):
    if before:
        return
    if not succeeded:
        assert b"Images saved" not in output, "unsuccessful request showed completion"
        return
    completion = re.search(rb"\r\x1b\[J([^\r\n\x1b]*Images saved[^\r\n\x1b]*)\r?\n", output)
    assert completion, "successful saving did not retain its completion line"
    line = completion.group(1)
    assert PROMPT.encode() not in line, "the completion line retained the prompt"
    assert not BAR.search(line) and b"[" not in line and b"]" not in line, "completion still contains a bar"
    if width >= 40:
        assert re.fullmatch(rb"Images saved \| (?:\d+s|\d+m \d{2}s) elapsed", line), "completion status is not plain saved text with elapsed time"
    assert b"Estimated" not in line and b"Ctrl+C" not in line, "completion retained waiting guidance"
    assert completion.end() <= output.index(b"Saved image:"), "completion appeared after the saved path"


def wait_result(process, start, failed):
    if failed:
        process.wait_for(lambda: RETAINED in process.output[start:] or OLD_RETAINED in process.output[start:], timeout=8)
    else:
        process.wait_for(lambda: b"Saved image:" in process.output[start:], timeout=8)
    process.pump(1.1)


def check(binary, output, case, before=False, mirror=False):
    failed = case in ("retry", "error-json")
    fixture = Fixture(fail=failed, delay=4)
    fixture.partial = case == "partial-save"
    process = None
    result = dict(case=case)
    prompt = PROMPTS.get(case, PROMPT)
    try:
        with tempfile.TemporaryDirectory(prefix="image-friendly-") as directory:
            home = pathlib.Path(directory)
            env = environment(home, fixture)
            env["GOMAXPROCS"] = "2"
            if case == "pipe":
                completed = subprocess.run([str(binary), "images", "generate", "--prompt", PROMPT],
                                           env=env, input=b"", capture_output=True, timeout=10)
                assert completed.returncode == 0
                assert b"Generating image" not in completed.stdout + completed.stderr
                assert b"Estimated" not in completed.stdout + completed.stderr
                assert b"Saving image" not in completed.stdout + completed.stderr and b"Images saved" not in completed.stdout + completed.stderr
                assert b"\x1b" not in completed.stdout + completed.stderr
                result.update(exit_status=completed.returncode, stdout=completed.stdout.decode(), stderr=completed.stderr.decode())
            else:
                args = None
                if case in MACHINE:
                    flags = ["--format", case]
                    if case == "error-json":
                        flags = ["--format-error", "json"]
                    elif case == "raw":
                        flags = ["--raw-output"]
                    elif case == "transform":
                        flags = ["--transform", "data.0.b64_json"]
                    args = flags + ["images", "generate", "--prompt", PROMPT, "--inline", "off"]
                if case.startswith("narrow-") or case in PROMPTS:
                    args = ["images", "generate", "--prompt", prompt, "--inline", "off"]
                if case == "partial-save":
                    args = ["images", "generate", "--prompt", PROMPT, "--count", "2", "--inline", "off"]
                process = Process(binary, env, args=args, mirror=mirror)
                width = int(case.removeprefix("narrow-")) if case.startswith("narrow-") else 104
                fcntl.ioctl(process.fd, termios.TIOCSWINSZ, struct.pack("HHHH", 28, width, 0, 0))
                if args is None:
                    process.ready()
                    process.submit()
                process.wait_for(lambda: len(fixture.requests) == 1)
                start = len(process.output)
                if case in MACHINE:
                    process.wait_for(lambda: process.status is not None, timeout=8)
                    assert process.status == (1 if failed else 0)
                    assert b"Generating image" not in process.output
                    assert b"Create image" not in process.output
                    assert b"Estimated" not in process.output and b"Images saved" not in process.output and b"Saving image" not in process.output
                else:
                    if case == "queued":
                        process.send(b"\r" * 50)
                    loading_check(process, start, before, width, prompt)
                    if case == "cancel":
                        canceled_at = time.monotonic()
                        assert process.stop() == 130
                        result["cancel_seconds"] = time.monotonic() - canceled_at
                        assert result["cancel_seconds"] < 1, "cancellation took more than one second"
                        assert_loading_cleared(bytes(process.output[start:]), before)
                        assert_completion(bytes(process.output[start:]), before, False)
                    elif case == "partial-save":
                        process.wait_for(lambda: process.status is not None, timeout=8)
                        assert process.status == 1
                        returned = bytes(process.output[start:])
                        assert_loading_cleared(returned[:returned.index(b"Saved image:")], before)
                        assert_completion(returned, before, False)
                        assert len(fixture.requests) == 1
                    elif case.startswith("narrow-") or case in PROMPTS:
                        process.wait_for(lambda: process.status is not None, timeout=8)
                        assert process.status == 0
                        assert fixture.requests[0]["prompt"] == prompt, "loading changed the API prompt"
                        returned = bytes(process.output[start:])
                        position = returned.index(b"Saved image:")
                        if width >= 20:
                            assert_loading_cleared(returned[:position], before)
                            assert_completion(returned, before, True, width)
                        assert b"Generating image" not in returned[position:]
                    else:
                        wait_result(process, start, failed)
                        returned = bytes(process.output[start:])
                        boundaries = (FAILURE, b"Request failed") if failed else (b"Saved image:",)
                        position = min(returned.index(boundary) for boundary in boundaries if boundary in returned)
                        assert_loading_cleared(returned[:position], before)
                        assert_completion(returned, before, not failed)
                        assert b"Generating image" not in returned[position:], "loading continued after the result"
                        if failed and not before:
                            assert RETAINED in returned
                            assert b"openai help" not in returned, "picker recovery still shows generic help diagnostics"
                        if case == "retry":
                            process.send(" with a blue hat")
                            process.pump(0.3)
                            process.send(b"\r")
                            process.wait_for(lambda: len(fixture.requests) == 2)
                            second = len(process.output)
                            loading_check(process, second, before, width, PROMPT + " with a blue hat")
                            wait_result(process, second, False)
                            assert_completion(bytes(process.output[second:]), before, True)
                            assert fixture.requests[-1]["prompt"] == PROMPT + " with a blue hat"
                        assert len(fixture.requests) == (2 if case == "retry" else 1)
                        assert process.stop() == 130
                result["exit_status"] = process.status
            assert len(fixture.requests) == (2 if case == "retry" else 1), "feedback changed the request count"
            files = saved(home)
            assert len(files) == (0 if case == "cancel" or case in MACHINE else 1)
            result.update(saved_files=files, requests=fixture.requests)
    finally:
        if process:
            (output / (case + ".pty.txt")).write_bytes(process.output)
            process.close()
        (output / (case + ".requests.json")).write_text(json.dumps(fixture.requests, indent=2) + "\n")
        fixture.close()
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("output", type=pathlib.Path)
    parser.add_argument("--before", action="store_true")
    parser.add_argument("--scene", action="store_true")
    parser.add_argument("--case", action="append", dest="cases")
    args = parser.parse_args()
    args.binary = args.binary.resolve()
    args.output.mkdir(parents=True, exist_ok=True)
    cases = args.cases or (["retry"] if args.scene else ["success", "retry", "cancel", "queued", "json", "jsonl", "yaml", "raw", "transform", "error-json", "pipe",
        "narrow-40", "narrow-20", "narrow-12", "prompt-long", "prompt-unicode", "prompt-controls", "partial-save"])
    initial_hash = hashlib.sha256(args.binary.read_bytes()).hexdigest()
    results = []
    for case in cases:
        if args.scene:
            print("\033[2J\033[H" + ("Before" if args.before else "After") + " | Synthetic responses delayed 4s | Native zsh PTY", flush=True)
            print("$ openai images generate", flush=True)
        try:
            result = check(args.binary, args.output, case, args.before, args.scene)
            result["result"] = "pass"
        except Exception as error:
            result = dict(case=case, result="fail", error=str(error))
        results.append(result)
        if args.scene:
            time.sleep(2)
        else:
            print(result["result"].upper() + ": " + case, flush=True)
    final_hash = hashlib.sha256(args.binary.read_bytes()).hexdigest()
    if initial_hash != final_hash:
        results.append(dict(case="binary-identity", result="fail", error="binary changed during the checks"))
    report = dict(binary=str(args.binary), sha256=initial_hash,
                  before=args.before, response_delay_seconds=4, cases=results)
    (args.output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    return int(any(result["result"] != "pass" for result in results))


if __name__ == "__main__":
    raise SystemExit(main())
