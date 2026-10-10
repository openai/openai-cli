#!/usr/bin/env python3
"""Serve successful-empty and denied fine-tuning requests using synthetic data."""

import http.server
import json
import pathlib
import signal
import sys
import threading


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG")
    failures = []
    lock = threading.Lock()
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                try:
                    if self.path == "/fine_tuning/jobs":
                        status = 200
                        body = {"object": "list", "data": [], "has_more": False}
                    elif self.path == "/denied/fine_tuning/jobs":
                        status = 403
                        body = {"error": {"message": "Synthetic project access denied.",
                                          "type": "invalid_request_error", "code": "permission_denied"}}
                    else:
                        raise AssertionError("unexpected synthetic request path")
                    encoded = json.dumps(body, separators=(",", ":")).encode()
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(encoded)))
                    self.end_headers()
                    self.wfile.write(encoded)
                    self.wfile.flush()
                    with lock:
                        log.write(json.dumps({"path": self.path, "status": status}) + "\n")
                        log.flush()
                except Exception as error:
                    with lock:
                        failures.append(type(error).__name__)

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
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
            raise RuntimeError("fixture or log failure: " + ", ".join(failures))


if __name__ == "__main__":
    main()
