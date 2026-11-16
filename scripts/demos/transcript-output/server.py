#!/usr/bin/env python3
"""Serve one fixed finite transcript after validating synthetic multipart input."""

import email.parser
import email.policy
import hashlib
import http.server
import json
import signal
import sys
import threading


AUDIO = b"synthetic audio fixture\n"
RESPONSE = {
    "task": "transcribe",
    "duration": 12.8,
    "text": "Thanks for calling. I need help.",
    "segments": [
        {"id": "demo_seg_001", "type": "transcript.text.segment", "start": 0,
         "end": 5.2, "speaker": "A", "text": "Thanks for calling."},
        {"id": "demo_seg_002", "type": "transcript.text.segment", "start": 5.2,
         "end": 12.8, "speaker": "B", "text": "I need help."},
    ],
}
BODY = json.dumps(RESPONSE, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG")
    failures = []
    stop = threading.Event()

    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def setup(self):
                self.request.settimeout(5)
                super().setup()

            def send_error(self, code, message=None, explain=None):
                failures.append("unexpected HTTP request")
                super().send_error(code, "Synthetic fixture request rejected")

            def do_POST(self):
                try:
                    if self.path != "/v1/audio/transcriptions":
                        raise ValueError("unexpected request path")
                    if self.headers.get("Authorization") != "Bearer synthetic-demo-key":
                        raise ValueError("expected fake authorization")
                    if self.headers.get("Transfer-Encoding"):
                        raise ValueError("unexpected transfer encoding")
                    length = int(self.headers.get("Content-Length", "0"))
                    # This fixture-only limit bounds the small request parser.
                    if not 0 < length <= 256 * 1024:
                        raise ValueError("unexpected fixture request size")
                    raw = self.rfile.read(length)
                    if len(raw) != length:
                        raise ValueError("incomplete fixture request")
                    content_type = self.headers.get("Content-Type", "")
                    message = email.parser.BytesParser(policy=email.policy.default).parsebytes(
                        b"Content-Type: " + content_type.encode("ascii") +
                        b"\r\nMIME-Version: 1.0\r\n\r\n" + raw
                    )
                    if message.get_content_type() != "multipart/form-data" or not message.is_multipart():
                        raise ValueError("expected multipart form data")
                    if message.defects:
                        raise ValueError("malformed multipart data")
                    fields = {}
                    for part in message.iter_parts():
                        name = part.get_param("name", header="content-disposition")
                        if part.get_content_disposition() != "form-data" or not name or name in fields:
                            raise ValueError("invalid multipart field")
                        if part.defects or part.is_multipart():
                            raise ValueError("malformed multipart field")
                        value = part.get_payload(decode=True)
                        if name == "file":
                            if part.get_filename() != "sample.wav" or value != AUDIO:
                                raise ValueError("expected synthetic audio fixture")
                            fields[name] = {
                                "filename": "sample.wav", "bytes": len(AUDIO),
                                "sha256": hashlib.sha256(AUDIO).hexdigest(),
                            }
                        else:
                            if part.get_filename() is not None:
                                raise ValueError("unexpected multipart file")
                            fields[name] = value.decode("utf-8")
                    expected = {
                        "file": {"filename": "sample.wav", "bytes": len(AUDIO),
                                 "sha256": hashlib.sha256(AUDIO).hexdigest()},
                        "model": "gpt-4o-transcribe-diarize",
                        "response_format": "diarized_json", "chunking_strategy": "auto",
                    }
                    if fields != expected:
                        raise ValueError("unexpected synthetic request fields")
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(BODY)))
                    self.end_headers()
                    self.wfile.write(BODY)
                    self.wfile.flush()
                    log.write(json.dumps({
                        "method": "POST", "path": self.path, "status": 200,
                        "body": fields, "response_bytes": len(BODY),
                        "response_sha256": hashlib.sha256(BODY).hexdigest(),
                    }, sort_keys=True) + "\n")
                    log.flush()
                except Exception as error:
                    # Never record unvalidated headers, uploaded bytes, or request values.
                    failures.append(type(error).__name__)
                    try:
                        self.send_error(400)
                    except (BrokenPipeError, ConnectionResetError, TimeoutError):
                        # The client may disconnect or time out during the error response.
                        # The original failure is already recorded for shutdown.
                        pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Fixture)
        signal.signal(signal.SIGTERM, lambda *_: stop.set())
        signal.signal(signal.SIGINT, lambda *_: stop.set())
        worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02})
        worker.start()
        try:
            with open(sys.argv[1], "x", encoding="utf-8") as address:
                address.write(f"http://127.0.0.1:{server.server_port}/v1\n")
            stop.wait()
        finally:
            server.shutdown()
            server.server_close()
            worker.join()
        if failures:
            raise RuntimeError("synthetic request or evidence failure: " + ", ".join(failures))


if __name__ == "__main__":
    main()
