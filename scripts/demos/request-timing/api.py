#!/usr/bin/env python3
"""Synthetic loopback fixture for the shared terminal capture lifecycle."""

import http.server
import json
import pathlib
import signal
import sys
import time


address_file, requests_file = map(pathlib.Path, sys.argv[1:3])
stopping = False


def stop(_signal, _frame):
    global stopping
    stopping = True


class API(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        pass

    def do_GET(self):
        if self.path != "/models/model_synthetic":
            self.send_error(404, "Synthetic route not found")
            return
        body = b'{"id":"model_synthetic","object":"model","created":0,"owned_by":"synthetic"}'
        started = time.monotonic()
        time.sleep(0.18)
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.flush()
        headers = time.monotonic() - started
        time.sleep(0.24)
        split = len(body) // 2
        self.wfile.write(body[:split])
        self.wfile.flush()
        first = time.monotonic() - started
        time.sleep(0.24)
        self.wfile.write(body[split:])
        self.wfile.flush()
        complete = time.monotonic() - started
        with requests_file.open("a") as output:
            output.write(json.dumps({
                "path": self.path, "status": 200,
                "headers_ms": round(headers * 1000),
                "first_data_ms": round(first * 1000),
                "complete_ms": round(complete * 1000),
            }) + "\n")


signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)
with http.server.ThreadingHTTPServer(("127.0.0.1", 0), API) as server:
    server.timeout = 0.1
    address_file.write_text(f"http://127.0.0.1:{server.server_port}\n")
    while not stopping:
        server.handle_request()
