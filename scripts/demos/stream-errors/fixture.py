#!/usr/bin/env python3
"""Serve only the two synthetic failure cases used by this recording."""

import http.server
import json
import pathlib
import signal
import sys


def main():
    address, log = map(pathlib.Path, sys.argv[1:])
    stopped = False
    failed = False

    def stop(_signal, _frame):
        nonlocal stopped
        stopped = True

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    with log.open("x", encoding="utf-8") as requests:
        class Handler(http.server.BaseHTTPRequestHandler):
            timeout = 2

            def log_message(self, *_args):
                pass

            def do_GET(self):
                nonlocal failed
                if self.headers.get("Authorization") != "Bearer synthetic-demo-key":
                    failed = True
                    self.send_error(400)
                    return
                if self.path == "/models/model_synthetic":
                    status, kind, body = 502, "application/json", b'{"error":{}}'
                elif self.path == "/responses/resp_synthetic?starting_after=10&stream=true":
                    event = {"type": "response.reasoning_text.delta", "item_id": "item_synthetic",
                             "output_index": 0, "content_index": 0, "sequence_number": 12,
                             "delta": "synthetic reasoning"}
                    status, kind = 200, "text/event-stream"
                    body = ("data: " + json.dumps(event) + "\n\n").encode()
                else:
                    failed = True
                    self.send_error(404)
                    return
                requests.write(json.dumps({"method": "GET", "path": self.path}) + "\n")
                requests.flush()
                self.send_response(status)
                self.send_header("Content-Type", kind)
                self.send_header("Content-Length", str(len(body)))
                self.send_header("x-should-retry", "false")
                self.end_headers()
                try:
                    self.wfile.write(body)
                    self.wfile.flush()
                except OSError:
                    failed = True

        with http.server.HTTPServer(("127.0.0.1", 0), Handler) as server:
            server.timeout = 0.1
            address.write_text(f"http://127.0.0.1:{server.server_port}", encoding="utf-8")
            while not stopped:
                server.handle_request()
    return int(failed)


if __name__ == "__main__":
    sys.exit(main())
