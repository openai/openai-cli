#!/usr/bin/env python3
"""Serve short synthetic model IDs and varied owners, with a loading gate."""

import hashlib
import http.server
import json
import pathlib
import signal
import sys
import threading
import time


def main():
    if len(sys.argv) != 5:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG FIXTURE_METADATA LOADING_GATE")
    gate = pathlib.Path(sys.argv[4])
    gate.mkdir()
    owners = ("openai", "system", "demo-research-team")
    items = [{"id": f"demo-text-2026-10-01-{i:03d}", "object": "model", "created": 1700000000,
              "owned_by": owners[i % len(owners)], "shutdown_date": "2030-01-01" if i % 2 else None}
             for i in range(47, -1, -1)]
    body = json.dumps({"object": "list", "data": items}, separators=(",", ":")).encode()
    pathlib.Path(sys.argv[3]).write_text(json.dumps({
        "models": len(items), "response_bytes": len(body),
        "sha256": hashlib.sha256(body).hexdigest(), "data": "synthetic only",
        "response_order": "descending model IDs", "records": items,
    }, indent=2) + "\n")
    failures = []
    lock = threading.Lock()
    started = time.monotonic()
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def send_error(self, code, message=None, explain=None):
                with lock:
                    failures.append("unexpected HTTP error")
                super().send_error(code, message, explain)

            def do_GET(self):
                try:
                    if self.path not in {"/before/v1/models", "/after/v1/models", "/loading/v1/models"}:
                        raise AssertionError("unexpected synthetic request path")
                    requested = time.monotonic()
                    if self.path == "/loading/v1/models":
                        (gate / "requested").write_text("request received\n")
                        deadline = requested + 15
                        while not (gate / "release").exists():
                            if stop.wait(0.02) or time.monotonic() >= deadline:
                                raise TimeoutError("loading scene did not release its response")
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    self.wfile.flush()
                    with lock:
                        log.write(json.dumps({"path": self.path, "status": 200,
                                              "response_bytes": len(body),
                                              "response_sha256": hashlib.sha256(body).hexdigest(),
                                              "held_seconds": time.monotonic() - requested,
                                              "elapsed_seconds": time.monotonic() - started}) + "\n")
                        log.flush()
                except Exception as error:
                    with lock:
                        failures.append(type(error).__name__)

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
        server.daemon_threads = False
        stop = threading.Event()
        signal.signal(signal.SIGTERM, lambda *_: stop.set())
        signal.signal(signal.SIGINT, lambda *_: stop.set())
        worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02})
        worker.start()
        try:
            pathlib.Path(sys.argv[1]).write_text(f"http://127.0.0.1:{server.server_port}\n")
            stop.wait()
        finally:
            server.shutdown()
            server.server_close()
            worker.join()
        if failures:
            raise RuntimeError("synthetic response or request-log failure: " + ", ".join(failures))


if __name__ == "__main__":
    main()
