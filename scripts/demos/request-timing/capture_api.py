#!/usr/bin/env python3
"""Serve synthetic Responses fixtures for the capture terminal replay."""

import hashlib
import http.server
import json
import pathlib
import signal
import sys
import threading
import time


address_file = pathlib.Path(sys.argv[1])
requests_file = pathlib.Path(sys.argv[2])
audit_lock = threading.Lock()
stopping = False


def stop(_signal, _frame):
    global stopping
    stopping = True


def encode(value):
    return json.dumps(value, separators=(",", ":")).encode()


class API(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        pass

    def do_POST(self):
        self.connection.settimeout(5)
        started = time.monotonic()
        length = int(self.headers.get("Content-Length", "0"))
        # This restriction belongs only to this fixed synthetic fixture.
        if self.path != "/responses" or not 0 < length <= 4096:
            self.send_error(400)
            return
        raw = self.rfile.read(length)
        request = json.loads(raw)
        assert request["model"] == "fake-model"
        assert request["input"] == "synthetic private prompt"
        assert request["store"] is False
        assert request["max_output_tokens"] == 8
        streaming = request.get("stream", False)
        assert isinstance(streaming, bool)
        response = {
            "id": "resp_synthetic", "object": "response", "status": "completed",
            "model": "fake-model", "output": [],
            "usage": {"input_tokens": 3, "output_tokens": 2, "total_tokens": 5},
            "metadata": {"note": "synthetic private response"},
        }
        body = encode(response)
        time.sleep(0.08)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream" if streaming else "application/json")
        self.send_header("X-Request-ID", "req_synthetic_capture")
        self.send_header("Connection", "close")
        if not streaming:
            self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        headers_ms = round((time.monotonic() - started) * 1000)
        if streaming:
            time.sleep(0.08)
            self.wfile.write(b": synthetic private heartbeat\r\n\r\n")
            self.wfile.flush()
            events = [
                {"type": "response.created", "sequence_number": 0,
                 "response": {"status": "in_progress", "output": []}},
                {"type": "response.output_text.delta", "sequence_number": 1,
                 "delta": "synthetic answer"},
                {"type": "response.completed", "sequence_number": 2, "response": response},
            ]
            for event in events:
                time.sleep(0.12)
                self.wfile.write(b"event: " + event["type"].encode() + b"\r\ndata: " + encode(event) + b"\r\n\r\n")
                self.wfile.flush()
        else:
            time.sleep(0.12)
            self.wfile.write(body)
            self.wfile.flush()
        self.close_connection = True
        record = {
            "path": self.path, "status": 200, "stream": streaming,
            "request_sha256": hashlib.sha256(raw).hexdigest(),
            "headers_ms": headers_ms,
            "fixture_finished_ms": round((time.monotonic() - started) * 1000),
        }
        with audit_lock, requests_file.open("a") as audit:
            audit.write(json.dumps(record) + "\n")


signal.signal(signal.SIGINT, stop)
signal.signal(signal.SIGTERM, stop)
with http.server.ThreadingHTTPServer(("127.0.0.1", 0), API) as server:
    server.daemon_threads = False
    server.timeout = 0.1
    address_file.write_text(f"http://127.0.0.1:{server.server_port}/")
    while not stopping:
        server.handle_request()
