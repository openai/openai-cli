#!/usr/bin/env python3
"""Try Batch workflows using an explicit CLI binary and a synthetic loopback API."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import tempfile
import threading
from http.server import HTTPServer

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from workflow_fixture import INPUT_BYTES, LABELS, OUTPUT_BYTES, WorkflowFixture, output_label, write_new


def stop_process(process):
    if process.poll() is not None:
        return
    try:
        if os.name == "posix":
            os.killpg(process.pid, signal.SIGTERM)
        else:
            process.terminate()
    except ProcessLookupError:
        # The process can exit between poll() and the signal.
        pass
    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        try:
            if os.name == "posix":
                os.killpg(process.pid, signal.SIGKILL)
            else:
                process.kill()
        except ProcessLookupError:
            # The process can exit while the timeout handler prepares SIGKILL.
            pass
        process.wait(timeout=3)


def run_command(binary, args, environment, directory, sequence, commands, display=True):
    if display:
        print("\n$ openai " + shlex.join(args), flush=True)
    process = subprocess.Popen([str(binary), *args], cwd=directory, env=environment,
                               stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               start_new_session=(os.name == "posix"))
    record = {"arguments": args, "exit": None, "process_stopped": False}
    commands.append(record)
    failure = None
    try:
        stdout, stderr = process.communicate(timeout=30)
    except (Exception, KeyboardInterrupt) as error:
        failure = error
        stop_process(process)
        stdout, stderr = process.communicate(timeout=3)
    finally:
        try:
            stop_process(process)
        finally:
            record.update(exit=process.poll(), process_stopped=process.poll() is not None)
    write_new(directory / f"{sequence:02d}.stdout", stdout)
    write_new(directory / f"{sequence:02d}.stderr", stderr)
    if display or failure is not None or process.returncode != 0:
        sys.stdout.buffer.write(stdout)
        sys.stdout.buffer.flush()
        sys.stderr.buffer.write(stderr)
        sys.stderr.buffer.flush()
    if failure is not None:
        raise failure
    if process.returncode != 0:
        raise RuntimeError(f"CLI command {sequence} failed with exit {process.returncode}; inspect retained logs")
    return stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path, help="Reviewed candidate executable; no installation occurs")
    parser.add_argument("--output-dir", type=Path, help="New evidence directory outside the repository")
    parser.add_argument("--show-checks", action="store_true", help="Also display list, input search, and JSON verification output")
    parser.add_argument("--simple", action="store_true", help="Try positional submit with combined waiting and saving")
    args = parser.parse_args()
    try:
        binary = args.binary.expanduser().resolve(strict=True)
    except OSError:
        parser.error("cannot open the candidate executable; check its path and read permission")
    if not binary.is_file() or not os.access(binary, os.X_OK):
        parser.error("binary must identify an executable file")
    repository = next((parent for parent in Path(__file__).resolve().parents
                       if (parent / "go.mod").is_file() and (parent / "cmd" / "openai").is_dir()), None)
    if args.output_dir:
        directory = args.output_dir.expanduser().resolve()
        if repository is not None and (directory == repository or repository in directory.parents):
            parser.error("keep trial evidence outside the repository")
        directory.mkdir(mode=0o700)
    else:
        directory = Path(tempfile.mkdtemp(prefix="f13-workflows-"))
    home = directory / "home"
    home.mkdir(mode=0o700)
    write_new(directory / "requests.jsonl", INPUT_BYTES)
    print("SYNTHETIC LOCAL TRIAL: three fake requests. No real API calls, API keys, or charges.", flush=True)
    print(f"Candidate: {binary}\nEvidence: {directory}", flush=True)
    print("Scenario: classify three messages, then inspect one answer.", flush=True)
    environment = {
        "PATH": os.defpath, "HOME": str(home), "USERPROFILE": str(home), "APPDATA": str(home),
        "LOCALAPPDATA": str(home), "XDG_CONFIG_HOME": str(home), "XDG_CACHE_HOME": str(home),
        "OPENAI_API_KEY": "synthetic-demo-key", "OPENAI_CLI_NO_UPDATE_CHECK": "1",
        "NO_COLOR": "1", "CI": "1", "LANG": "en_US.UTF-8", "GOMAXPROCS": "2",
    }
    if os.name == "nt" and "SystemRoot" in os.environ:
        environment["SystemRoot"] = os.environ["SystemRoot"]
    commands = []
    digest = hashlib.sha256()
    with binary.open("rb") as executable:
        for block in iter(lambda: executable.read(1024 * 1024), b""):
            digest.update(block)
    binary_sha256 = digest.hexdigest()
    outcome = {"synthetic": True, "passed": False, "binary": str(binary),
               "binary_sha256": binary_sha256,
               "commands": commands, "cleanup": {"server_stopped": False, "processes_stopped": False}}
    error = None
    log_descriptor = os.open(directory / "http-requests.jsonl", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(log_descriptor, "w", encoding="utf-8") as log:
        fixture = WorkflowFixture(log)
        server = HTTPServer(("127.0.0.1", 0), fixture.handler())
        thread = threading.Thread(target=server.serve_forever, name="synthetic-batch-api")
        thread.start()
        environment["OPENAI_BASE_URL"] = f"http://127.0.0.1:{server.server_port}/v1"
        try:
            run = lambda command, display=True: run_command(binary, command, environment, directory, len(commands) + 1, commands, display)
            if args.simple:
                print("\nOne command uploads, starts, waits, and saves results.", flush=True)
                submitted = run(["batches", "submit", "requests.jsonl", "--wait", "--output-dir", "results"])
            else:
                print("\nSubmit uploads the input file and creates one batch.", flush=True)
                submitted = run(["batches", "submit", "--file", "requests.jsonl", "--endpoint", "/v1/responses", "--completion-window", "24h"])
            if b"batch_demo" not in submitted:
                raise AssertionError("submission receipt did not identify the created batch")
            watched = run(["batches", "list", "--watch", "--watch-count", "2", "--poll-interval", "1s"], args.show_checks)
            if b"Batch snapshot 1" not in watched or b"Batch snapshot 2" not in watched:
                raise AssertionError("list did not display both snapshots")
            inputs = json.loads(run(["batches", "inputs", "--search", "requests.jsonl", "--format", "json"], args.show_checks))
            if len(inputs) != 1 or inputs[0]["id"] != "file_input_demo":
                raise AssertionError("input search did not identify the uploaded file")
            if not args.simple:
                print("\nWait saves the complete result file when the batch finishes.", flush=True)
                run(["batches", "wait", "batch_demo", "--output-dir", "results", "--poll-interval", "1s"])
            print("\nResults shows one answer beside its original input.", flush=True)
            readable = run(["batches", "results", "batch_demo", "--custom-id", "sentiment-positive", "--match-input"])
            if b"positive" not in readable or b"I love this." not in readable or b"Return only the label." not in readable:
                raise AssertionError("readable results lost the answer, input, or instructions")
            matched = json.loads(run(["batches", "results", "batch_demo", "--custom-id", "sentiment-positive", "--match-input", "--format", "json"], args.show_checks))
            row = matched["data"][0]
            if len(matched["data"]) != 1 or row["match"] != "matched" or row["inputs"][0]["custom_id"] != "sentiment-positive":
                raise AssertionError("result matching did not select the requested input")
            if output_label(row["results"][0]) != "positive":
                raise AssertionError("result matching used file order instead of custom_id")
            saved = (directory / "results" / "output.jsonl").read_bytes()
            if saved != OUTPUT_BYTES:
                raise AssertionError("download changed the synthetic output bytes")
            records = [json.loads(line) for line in saved.splitlines()]
            if {row["custom_id"]: output_label(row) for row in records} != LABELS or len(records) != 3:
                raise AssertionError("saved results did not match all three expected labels")
            state = fixture.states[""]
            if state != {"uploads": 1, "creates": 1, "snapshots": 2} or fixture.failures:
                raise AssertionError("fixture detected repeated mutations or an unexpected request")
            outcome.update(passed=True, fixture_state=state, expected_labels=LABELS,
                           saved_output_sha256=hashlib.sha256(saved).hexdigest())
        except (Exception, KeyboardInterrupt) as failure:
            error = failure
            outcome["failure"] = {"type": type(failure).__name__, "message": str(failure)}
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)
            outcome["cleanup"] = {"server_stopped": not thread.is_alive(),
                                  "processes_stopped": all(command["process_stopped"] for command in commands)}
            outcome["fixture_failures"] = fixture.failures
            if thread.is_alive():
                outcome["passed"] = False
                error = RuntimeError("fixture server did not stop")
    write_new(directory / "result.json", (json.dumps(outcome, indent=2) + "\n").encode())
    if error is not None:
        if isinstance(error, KeyboardInterrupt):
            print("Trial interrupted.", file=sys.stderr)
            if outcome["cleanup"]["processes_stopped"]:
                print("Child processes stopped.", file=sys.stderr)
            else:
                print("Child process cleanup is incomplete; inspect the retained logs.", file=sys.stderr)
            print(f"Evidence remains in {directory}", file=sys.stderr)
            return 130
        if isinstance(error, subprocess.TimeoutExpired):
            print("Trial command timed out. The child process stopped; inspect its retained logs before retrying.", file=sys.stderr)
        else:
            print(f"Trial failed: {error}", file=sys.stderr)
        print(f"Evidence remains in {directory}", file=sys.stderr)
        return 1
    print("\nVerified: all three saved labels match the synthetic fixture.")
    print("Verified: the CLI uploaded once and created one batch.")
    print("Verified: input matching used custom_id, despite reordered results.")
    print("These checks do not test your API key or account permissions.")
    print(f"Saved results: {directory / 'results' / 'output.jsonl'}")
    print(f"Evidence: {directory / 'result.json'}")
    print("Use --show-checks to display the additional checks. Their output is already saved with the evidence.")
    return 0


if __name__ == "__main__":
    def interrupted(_signal, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupted)
    sys.exit(main())
