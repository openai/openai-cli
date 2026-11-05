#!/usr/bin/env python3
"""Loopback-only synthetic API for the empty vector-store search demo."""
import http.server
import json
import pathlib
import signal
import sys


class Search(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        valid = self.path == "/v1/vector_stores/vs_demo/search"
        try:
            valid = valid and json.loads(body).get("query") == "hello"
        except (ValueError, AttributeError):
            valid = False
        self.send_response(200 if valid else 400)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        if valid:
            self.wfile.write(b'{"object":"vector_store.search_results.page","search_query":["hello"],"data":[],"has_more":false,"next_page":null}')
        else:
            self.wfile.write(b'{"error":{"message":"Unexpected synthetic request"}}')

    def log_message(self, *_):
        pass


def stop(_signal, _frame):
    raise SystemExit(0)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, stop)
    with http.server.HTTPServer(("127.0.0.1", 0), Search) as server:
        pathlib.Path(sys.argv[1]).write_text(f"http://127.0.0.1:{server.server_port}/v1")
        server.serve_forever()
