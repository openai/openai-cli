#!/usr/bin/env python3
"""Synthetic Batch workflow API. This fixture accepts fake credentials only."""

import argparse
import email.parser
import email.policy
import json
import os
from pathlib import Path
import signal
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlsplit

LABELS = {
    "sentiment-positive": "positive",
    "sentiment-negative": "negative",
    "sentiment-neutral": "neutral",
}
INPUT_RECORDS = [
    {
        "custom_id": custom_id,
        "method": "POST",
        "url": "/v1/responses",
        "body": {
            "model": "synthetic-demo-model",
            "instructions": "Classify the message as positive, negative, or neutral. Return only the label.",
            "input": text,
            "max_output_tokens": 32,
        },
    }
    for custom_id, text in zip(
        LABELS, ["I love this.", "This is awful.", "The package arrived."]
    )
]
INPUT_BYTES = b"".join((json.dumps(row) + "\n").encode() for row in INPUT_RECORDS)
OUTPUT_RECORDS = [
    {
        "custom_id": custom_id,
        "response": {
            "status_code": 200,
            "request_id": "req_synthetic_" + str(index),
            "body": {
                "id": "resp_synthetic_" + str(index),
                "object": "response",
                "status": "completed",
                "temperature": 1,
                "top_p": 1,
                "store": False,
                "output": [{"type": "message", "role": "assistant", "content": [
                    {"type": "output_text", "text": LABELS[custom_id]}
                ]}],
                "usage": {"input_tokens": 31, "output_tokens": 5, "total_tokens": 36},
            },
        },
        "error": None,
    }
    for index, custom_id in enumerate(
        ["sentiment-neutral", "sentiment-positive", "sentiment-negative"]
    )
]
OUTPUT_BYTES = b"".join((json.dumps(row) + "\n").encode() for row in OUTPUT_RECORDS)


def write_new(path, data):
    """Publish only new, private evidence files and check flush/close errors."""
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        if stream.write(data) != len(data):
            raise OSError("incomplete evidence write")
        stream.flush()
        os.fsync(stream.fileno())


def output_label(record):
    return record["response"]["body"]["output"][0]["content"][0]["text"]


class WorkflowFixture:
    def __init__(self, log):
        self.log = log
        self.states = {}
        self.failures = []

    def record(self, event):
        encoded = json.dumps(event) + "\n"
        if self.log.write(encoded) != len(encoded):
            raise OSError("incomplete request evidence")
        self.log.flush()
        os.fsync(self.log.fileno())

    def state(self, namespace):
        return self.states.setdefault(namespace, {"uploads": 0, "creates": 0, "snapshots": 0})

    def file(self):
        return {"id": "file_input_demo", "object": "file", "bytes": len(INPUT_BYTES),
                "filename": "requests.jsonl", "purpose": "batch", "created_at": 1792800000,
                "status": "processed"}

    def batch(self, status="completed"):
        finished = 3 if status == "completed" else 1 if status == "in_progress" else 0
        return {"id": "batch_demo", "object": "batch", "endpoint": "/v1/responses",
                "input_file_id": "file_input_demo", "completion_window": "24h", "status": status,
                "created_at": 1792800000, "in_progress_at": 1792800002,
                "completed_at": 1792800005 if status == "completed" else None,
                "output_file_id": "file_result" if status == "completed" else None,
                "error_file_id": None, "request_counts": {"total": 3, "completed": finished, "failed": 0},
                "metadata": {"example": "synthetic sentiment labels"}}

    def handler(self):
        fixture = self

        class Handler(BaseHTTPRequestHandler):
            timeout = 3

            def log_message(self, *_args):
                pass

            def do_GET(self):
                self.respond()

            def do_POST(self):
                self.respond()

            def respond(self):
                try:
                    if self.headers.get("Authorization") != "Bearer synthetic-demo-key":
                        raise ValueError("fixture requires its fake API key")
                    parsed = urlsplit(self.path)
                    namespace, separator, route = parsed.path.partition("/v1")
                    if not separator or namespace not in ("", "/before", "/after"):
                        raise ValueError("unexpected fixture namespace")
                    state = fixture.state(namespace)
                    query = parse_qs(parsed.query)
                    response = self.route(state, route, query)
                    body = response if isinstance(response, bytes) else json.dumps(response).encode()
                    fixture.record({"method": self.command, "namespace": namespace, "path": route,
                                    "status": 200, "uploads": state["uploads"], "creates": state["creates"]})
                    self.send_response(200)
                    self.send_header("Content-Type", "application/jsonl" if isinstance(response, bytes) else "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.send_header("x-should-retry", "false")
                    self.end_headers()
                    self.wfile.write(body)
                    self.wfile.flush()
                except Exception as error:
                    fixture.failures.append(type(error).__name__)
                    fixture.record({"method": self.command, "failure": type(error).__name__})
                    self.send_error(400, "synthetic fixture request failed")

            def route(self, state, route, query):
                if self.command == "POST":
                    length = int(self.headers.get("Content-Length", "0"))
                    if not 0 < length <= 1024 * 1024:
                        raise ValueError("unexpected synthetic request size")
                    body = self.rfile.read(length)
                    if len(body) != length:
                        raise ValueError("incomplete synthetic request")
                    if route == "/files":
                        if state["uploads"]:
                            raise ValueError("upload repeated")
                        message = email.parser.BytesParser(policy=email.policy.default).parsebytes(
                            b"Content-Type: " + self.headers.get("Content-Type", "").encode("ascii")
                            + b"\r\nMIME-Version: 1.0\r\n\r\n" + body
                        )
                        parts = {part.get_param("name", header="content-disposition"): part.get_payload(decode=True)
                                 for part in message.iter_parts()}
                        if parts != {"purpose": b"batch", "file": INPUT_BYTES}:
                            raise ValueError("upload does not match the synthetic inputs")
                        state["uploads"] += 1
                        return fixture.file()
                    if route == "/batches":
                        if state["uploads"] != 1 or state["creates"]:
                            raise ValueError("batch creation repeated or upload absent")
                        if json.loads(body) != {"input_file_id": "file_input_demo", "endpoint": "/v1/responses", "completion_window": "24h"}:
                            raise ValueError("unexpected synthetic batch parameters")
                        state["creates"] += 1
                        return fixture.batch("validating")
                    raise ValueError("unexpected mutation")
                if self.command != "GET":
                    raise ValueError("unexpected method")
                if route == "/files":
                    if query.get("purpose") != ["batch"]:
                        raise ValueError("Batch input list must request purpose=batch")
                    rows = [fixture.file()] if state["uploads"] else []
                    return {"object": "list", "data": rows, "has_more": False,
                            "first_id": "file_input_demo" if rows else None, "last_id": "file_input_demo" if rows else None}
                if state["creates"] != 1:
                    raise ValueError("create a synthetic batch first")
                if route == "/batches":
                    state["snapshots"] += 1
                    batch = fixture.batch("in_progress" if state["snapshots"] == 1 else "completed")
                    return {"object": "list", "data": [batch], "has_more": False,
                            "first_id": "batch_demo", "last_id": "batch_demo"}
                if route == "/batches/batch_demo":
                    return fixture.batch()
                if route == "/files/file_input_demo/content":
                    return INPUT_BYTES
                if route == "/files/file_result/content":
                    return OUTPUT_BYTES
                raise ValueError("unexpected read route")

        return Handler


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("address_file", type=Path)
    parser.add_argument("request_log", type=Path)
    args = parser.parse_args()
    stopping = False

    def stop(_signal, _frame):
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGTERM, stop)
    descriptor = os.open(args.request_log, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as log:
        fixture = WorkflowFixture(log)
        with HTTPServer(("127.0.0.1", 0), fixture.handler()) as server:
            server.timeout = 0.1
            write_new(args.address_file, f"http://127.0.0.1:{server.server_port}/v1".encode())
            while not stopping:
                server.handle_request()
    if fixture.failures:
        raise SystemExit("Synthetic fixture failed; inspect its retained request log.")


if __name__ == "__main__":
    main()
