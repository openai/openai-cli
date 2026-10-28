#!/usr/bin/env python3
"""Browse synthetic batches using an explicit local CLI binary. No live API calls."""

import argparse
import email.parser
import email.policy
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
from http.server import HTTPServer

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from try_workflows import stop_process
from workflow_fixture import INPUT_BYTES, OUTPUT_BYTES, OUTPUT_RECORDS, WorkflowFixture, write_new


class BrowserFixture(WorkflowFixture):
    """Keep fixed examples and allow explicitly created synthetic batches."""

    def files(self, state):
        return [self.file(), *state.get("uploaded_files", [])]

    def created_batch(self, record):
        retrieved = record["retrieves"]
        status = "validating" if retrieved == 0 else "in_progress" if retrieved < 3 else "completed"
        batch = self.batch(status)
        batch.update(id=record["id"], input_file_id=record["input_file_id"], errors=None,
                     metadata=record.get("metadata", {}))
        if status == "in_progress":
            batch["request_counts"]["completed"] = retrieved
        if status == "completed":
            batch["output_file_id"] = record["output_file_id"]
        if "output_expires_after" in record:
            batch["output_expires_after"] = record["output_expires_after"]
        return batch

    @staticmethod
    def page(rows, query):
        if set(query) - {"limit", "after", "purpose", "order"}:
            raise ValueError("unexpected synthetic list query")
        if "after" in query:
            matches = [index for index, row in enumerate(rows) if row["id"] == query["after"][0]]
            if not matches:
                raise ValueError("unknown synthetic list cursor")
            rows = rows[matches[0] + 1:]
        limit = int(query.get("limit", ["20"])[0])
        if not 1 <= limit <= 10000:
            raise ValueError("invalid synthetic list limit")
        selected = rows[:limit]
        return {"object": "list", "data": selected, "has_more": len(rows) > len(selected),
                "first_id": selected[0]["id"] if selected else None,
                "last_id": selected[-1]["id"] if selected else None}

    def examples(self):
        complete = self.batch()
        complete["metadata"] = {"scenario": "Three successful sentiment labels"}
        partial = self.batch()
        partial.update(id="batch_partial_demo", output_file_id="file_partial", error_file_id="file_error",
                       request_counts={"total": 3, "completed": 2, "failed": 1},
                       metadata={"scenario": "Two answers and one request error"})
        processing = self.batch("in_progress")
        processing.update(id="batch_processing_demo", metadata={"scenario": "Still processing; no results yet"})
        for batch in (complete, partial, processing):
            batch["errors"] = None
        return [complete, partial, processing]

    def handler(self):
        fixture = self
        base = super().handler()

        class Handler(base):
            def mutation(self, state, route, query):
                if query:
                    raise ValueError("unexpected synthetic mutation query")
                length = int(self.headers.get("Content-Length", "0"))
                if not 0 < length <= 1024 * 1024:
                    raise ValueError("unexpected synthetic request size")
                body = self.rfile.read(length)
                if len(body) != length:
                    raise ValueError("incomplete synthetic request")
                if route == "/files":
                    message = email.parser.BytesParser(policy=email.policy.default).parsebytes(
                        b"Content-Type: " + self.headers.get("Content-Type", "").encode("ascii")
                        + b"\r\nMIME-Version: 1.0\r\n\r\n" + body)
                    parts = list(message.iter_parts())
                    values = {part.get_param("name", header="content-disposition"): part.get_payload(decode=True)
                              for part in parts}
                    if len(parts) != 2 or values != {"purpose": b"batch", "file": INPUT_BYTES}:
                        raise ValueError("upload does not match the synthetic inputs")
                    state["uploads"] += 1
                    file = fixture.file()
                    file["id"] = "file_uploaded_demo_" + str(state["uploads"])
                    state.setdefault("uploaded_files", []).append(file)
                    return file
                if route != "/batches":
                    raise ValueError("unexpected synthetic mutation")
                request = json.loads(body)
                if not isinstance(request, dict) or set(request) - {
                    "input_file_id", "endpoint", "completion_window", "metadata", "output_expires_after"
                }:
                    raise ValueError("unexpected synthetic batch parameters")
                if (request.get("input_file_id") not in [row["id"] for row in fixture.files(state)]
                        or request.get("endpoint") != "/v1/responses"
                        or request.get("completion_window") != "24h"):
                    raise ValueError("synthetic batches require the provided Responses input and 24h window")
                metadata = request.get("metadata", {})
                if not isinstance(metadata, dict) or not all(isinstance(k, str) and isinstance(v, str) for k, v in metadata.items()):
                    raise ValueError("synthetic metadata must contain string values")
                expiration = request.get("output_expires_after")
                if expiration is not None and (
                    not isinstance(expiration, dict) or set(expiration) != {"anchor", "seconds"}
                    or expiration["anchor"] != "created_at" or type(expiration["seconds"]) is not int
                    or not 3600 <= expiration["seconds"] <= 2592000
                ):
                    raise ValueError("unexpected synthetic output expiration")
                state["creates"] += 1
                suffix = "" if state["creates"] == 1 else "_" + str(state["creates"])
                record = dict(request, id="batch_created_demo" + suffix, retrieves=0,
                              output_file_id="file_created_result" + suffix)
                state.setdefault("created_batches", {})[record["id"]] = record
                return fixture.created_batch(record)

            def route(self, state, route, query):
                if self.command == "POST":
                    return self.mutation(state, route, query)
                if self.command != "GET":
                    raise ValueError("unexpected browser demo method")
                rows = fixture.examples()
                created = state.get("created_batches", {})
                if route == "/batches":
                    state["snapshots"] += 1
                    return fixture.page([fixture.created_batch(row) for row in reversed(list(created.values()))] + rows, query)
                if route == "/files":
                    if query.get("purpose") != ["batch"]:
                        raise ValueError("Batch input list must request purpose=batch")
                    return fixture.page(fixture.files(state), query)
                for record in created.values():
                    if route == "/batches/" + record["id"]:
                        record["retrieves"] += 1
                        return fixture.created_batch(record)
                    if route == "/files/" + record["output_file_id"] + "/content" and record["retrieves"] >= 3:
                        return OUTPUT_BYTES
                for row in rows:
                    if route == "/batches/" + row["id"]:
                        return row
                for file in fixture.files(state):
                    if route == "/files/" + file["id"] + "/content":
                        return INPUT_BYTES
                    if route == "/files/" + file["id"]:
                        return file
                if route == "/files/file_result/content":
                    return OUTPUT_BYTES
                if route == "/files/file_partial/content":
                    return b"".join((json.dumps(row) + "\n").encode() for row in OUTPUT_RECORDS
                                    if row["custom_id"] != "sentiment-negative")
                if route == "/files/file_error/content":
                    return (json.dumps({"custom_id": "sentiment-negative", "response": {
                        "status_code": 500, "request_id": "req_synthetic_failed", "body": {
                            "error": {"code": "server_error", "type": "server_error",
                                      "message": "Synthetic service error. Retry this request in a new batch."}}},
                        "error": None}) + "\n").encode()
                raise ValueError("unexpected browser demo route")

        return Handler


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path, help="Reviewed CLI executable; no installation occurs")
    args = parser.parse_args()
    if not all(stream.isatty() for stream in (sys.stdin, sys.stdout, sys.stderr)):
        parser.error("run this interactive trial in a terminal without pipes or redirection")
    try:
        binary = args.binary.expanduser().resolve(strict=True)
    except OSError:
        parser.error("cannot open the candidate executable; check its path")
    if not binary.is_file() or not os.access(binary, os.X_OK):
        parser.error("binary must identify an executable file")
    directory = Path(tempfile.mkdtemp(prefix="f13-browser-"))
    isolated_home = directory / "home"
    isolated_home.mkdir(mode=0o700)
    environment = {
        "PATH": os.defpath, "HOME": str(isolated_home), "USERPROFILE": str(isolated_home),
        "APPDATA": str(isolated_home), "LOCALAPPDATA": str(isolated_home),
        "XDG_CONFIG_HOME": str(isolated_home), "XDG_CACHE_HOME": str(isolated_home),
        "OPENAI_API_KEY": "synthetic-demo-key", "OPENAI_CLI_NO_UPDATE_CHECK": "1",
        "TERM": os.environ.get("TERM", "xterm-256color"), "LANG": "en_US.UTF-8", "GOMAXPROCS": "2",
    }
    for name in ("SystemRoot", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORFGBG", "COLORTERM", "NO_COLOR"):
        if name in os.environ:
            environment[name] = os.environ[name]
    write_new(directory / "requests.jsonl", INPUT_BYTES)
    print("Demo: synthetic batches. No live API calls or charges.", flush=True)
    print("Press Ctrl+N to create a batch with requests.jsonl, or choose an uploaded input.", flush=True)
    print("Use the Responses endpoint. New demo batches finish after three progress updates.\n", flush=True)
    process = None
    stop_signal = None

    def request_stop(signum, _frame):
        nonlocal stop_signal
        # Let Popen return the child before cleanup, even if a second signal arrives.
        if stop_signal is None:
            stop_signal = signum

    signal.signal(signal.SIGINT, request_stop)
    signal.signal(signal.SIGTERM, request_stop)
    log_descriptor = os.open(directory / "request-log.jsonl", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(log_descriptor, "w", encoding="utf-8") as log:
        fixture = BrowserFixture(log)
        with HTTPServer(("127.0.0.1", 0), fixture.handler()) as server:
            thread = threading.Thread(target=server.serve_forever, name="synthetic-batch-browser")
            thread.start()
            environment["OPENAI_BASE_URL"] = f"http://127.0.0.1:{server.server_port}/v1"
            try:
                process = subprocess.Popen([str(binary), "batches", "browse"], cwd=directory,
                                           env=environment, start_new_session=(os.name == "posix"))
                while stop_signal is None:
                    try:
                        process.wait(timeout=0.2)
                        break
                    except subprocess.TimeoutExpired:
                        continue
            except OSError:
                print("Could not start the candidate executable. Check its permissions and platform.", file=sys.stderr)
            finally:
                try:
                    if process is not None:
                        stop_process(process)
                finally:
                    server.shutdown()
                    thread.join(timeout=5)
    code = process.returncode if process is not None else 1
    if stop_signal is not None:
        code = 128 + stop_signal
    outcome = {"synthetic": True, "exit": code, "fixture_failures": fixture.failures,
               "server_stopped": not thread.is_alive(),
               "process_stopped": process is not None and process.poll() is not None,
               "uploads": sum(state["uploads"] for state in fixture.states.values()),
               "creates": sum(state["creates"] for state in fixture.states.values()),
               "request_log": "request-log.jsonl", "input_file": "requests.jsonl"}
    write_new(directory / "session.json", (json.dumps(outcome, indent=2) + "\n").encode())
    print(f"\nDemo downloads and details: {directory}")
    if fixture.failures or not outcome["server_stopped"] or not outcome["process_stopped"]:
        print("The demo did not finish cleanly. Inspect session.json before repeating.", file=sys.stderr)
        return 1
    return code


if __name__ == "__main__":
    raise SystemExit(main())
