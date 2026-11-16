#!/usr/bin/env python3
"""Serve two synthetic Files pages and an empty batch-purpose list."""

import hashlib
import http.server
import json
import pathlib
import signal
import sys
import urllib.parse


RECORDS = [
    {"id": "file-demo-a", "object": "file", "filename": "alpha.txt", "purpose": "user_data"},
    {"id": "file-demo-b", "object": "file", "filename": "beta.txt", "purpose": "user_data"},
]


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG")
    stopped = False
    failures = []

    def stop(*_):
        nonlocal stopped
        stopped = True

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def setup(self):
                self.request.settimeout(2)
                super().setup()

            def log_message(self, *_):
                pass

            def send_error(self, code, message=None, explain=None):
                failures.append("unexpected HTTP error")
                super().send_error(code, message, explain)

            def do_GET(self):
                try:
                    url = urllib.parse.urlsplit(self.path)
                    assert url.path in {"/before/v1/files", "/after/v1/files"}
                    assert self.headers.get("Authorization") == "Bearer synthetic-demo-key"
                    query = urllib.parse.parse_qs(url.query, strict_parsing=True)
                    allowed = {"limit": ["10000"], "order": ["desc"],
                               "purpose": ["batch"], "after": ["file-demo-a"]}
                    assert all(key in allowed and value == allowed[key] for key, value in query.items())
                    assert not ("purpose" in query and "after" in query)
                    data = [] if "purpose" in query else [RECORDS[1 if "after" in query else 0]]
                    page = {"object": "list", "data": data,
                            "has_more": bool(data) and "after" not in query,
                            "first_id": data[0]["id"] if data else None,
                            "last_id": data[-1]["id"] if data else None}
                    body = json.dumps(page, separators=(",", ":")).encode()
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    self.wfile.flush()
                    log.write(json.dumps({"scene": url.path.split("/")[1], "query": query,
                                          "sha256": hashlib.sha256(body).hexdigest()}) + "\n")
                    log.flush()
                except Exception as error:
                    failures.append(type(error).__name__)
                    self.close_connection = True

        class Server(http.server.HTTPServer):
            def handle_error(self, *_):
                failures.append("request lifecycle failure")

        with Server(("127.0.0.1", 0), Fixture) as server:
            server.timeout = 0.1
            pathlib.Path(sys.argv[1]).write_text(f"http://127.0.0.1:{server.server_port}\n")
            while not stopped:
                server.handle_request()
    if failures:
        raise RuntimeError("synthetic fixture failure: " + ", ".join(failures))


if __name__ == "__main__":
    main()
