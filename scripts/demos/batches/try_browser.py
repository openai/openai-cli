#!/usr/bin/env python3
"""Browse synthetic batches using an explicit local CLI binary. No live API calls."""

import argparse
import json
import os
from pathlib import Path
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
    """Expose fixed successful, partially failed, and processing batches."""

    def examples(self):
        complete = self.batch()
        complete["metadata"] = {"scenario": "Three successful sentiment labels"}
        partial = self.batch()
        partial.update(id="batch_partial_demo", output_file_id="file_partial", error_file_id="file_error",
                       request_counts={"total": 3, "completed": 2, "failed": 1},
                       metadata={"scenario": "Two answers and one request error"})
        processing = self.batch("in_progress")
        processing.update(id="batch_processing_demo", metadata={"scenario": "Still processing; no results yet"})
        return [complete, partial, processing]

    def handler(self):
        fixture = self
        base = super().handler()

        class Handler(base):
            def route(self, state, route, query):
                if self.command != "GET":
                    raise ValueError("browser demo accepts read operations only")
                rows = fixture.examples()
                if route == "/batches":
                    return {"object": "list", "data": rows, "has_more": False,
                            "first_id": rows[0]["id"], "last_id": rows[-1]["id"]}
                for row in rows:
                    if route == "/batches/" + row["id"]:
                        return row
                if route == "/files/file_input_demo/content":
                    return INPUT_BYTES
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
    print("SYNTHETIC BATCH BROWSER: no live API calls, personal API keys, or charges.")
    print("Choose a batch, then inspect Status, Results, Errors, Inputs, or Download.")
    print("The three examples keep fixed statuses. Escape goes back; Ctrl+C exits.")
    print(f"Relative download paths use: {directory}", flush=True)
    process = None
    interrupted = False
    log_descriptor = os.open(directory / "requests.jsonl", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(log_descriptor, "w", encoding="utf-8") as log:
        fixture = BrowserFixture(log)
        with HTTPServer(("127.0.0.1", 0), fixture.handler()) as server:
            thread = threading.Thread(target=server.serve_forever, name="synthetic-batch-browser")
            thread.start()
            environment["OPENAI_BASE_URL"] = f"http://127.0.0.1:{server.server_port}/v1"
            try:
                process = subprocess.Popen([str(binary), "batches", "browse"], cwd=directory,
                                           env=environment, start_new_session=(os.name == "posix"))
                process.wait()
            except KeyboardInterrupt:
                interrupted = True
            except OSError:
                print("Could not start the candidate executable. Check its permissions and platform.", file=sys.stderr)
            finally:
                try:
                    if process is not None:
                        stop_process(process)
                finally:
                    server.shutdown()
                    thread.join(timeout=5)
    code = 130 if interrupted else process.returncode if process is not None else 1
    outcome = {"synthetic": True, "exit": code, "fixture_failures": fixture.failures,
               "server_stopped": not thread.is_alive(),
               "process_stopped": process is not None and process.poll() is not None}
    write_new(directory / "session.json", (json.dumps(outcome, indent=2) + "\n").encode())
    print(f"\nBrowser closed. Downloads and session evidence: {directory}")
    if fixture.failures or not outcome["server_stopped"] or not outcome["process_stopped"]:
        print("The demo did not finish cleanly. Inspect session.json before repeating.", file=sys.stderr)
        return 1
    return code


if __name__ == "__main__":
    raise SystemExit(main())
