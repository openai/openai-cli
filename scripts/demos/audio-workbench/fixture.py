#!/usr/bin/env python3
"""Synthetic speech fixture. No remote service or real audio is used."""

import hashlib
import http.server
import json
import math
from pathlib import Path
import select
import signal
import socket
import struct
import sys
import threading

TEXT = "Hello from the audio workbench.\nThis clip is synthetic."
INSTRUCTIONS = "Read clearly. Keep @literal and \\@literal unchanged."
DEFAULT_INSTRUCTIONS = "Speak warmly and clearly."
AUDITION = "Hello. This is a preview of this voice."
FAILURE_REQUEST_ID = "req_audio_demo_401"
FAILURE_CANARIES = ("SYNTHETIC_API_MESSAGE_CANARY", "sk-synthetic-do-not-print",
                    "synthetic-query-canary", "/synthetic/private-audio.wav")
AUTH_FAILURE = json.dumps({"error": {
    "message": ("SYNTHETIC_API_MESSAGE_CANARY sk-synthetic-do-not-print "
                "/synthetic/private-audio.wav "
                "https://example.invalid/private?token=synthetic-query-canary\n"
                "\x1b[2JUntrusted response text must never become terminal output."),
    "type": "authentication_error", "code": "invalid_api_key", "param": None,
}}).encode()
RATE = 24000
FRAMES = RATE * 2


def pcm_bytes():
    result = bytearray()
    amplitudes = (.08, .18, .36, .72, .95, .55, .25, .10)
    for frame in range(FRAMES):
        amplitude = amplitudes[frame // (RATE // 4)]
        sample = round(32767 * amplitude * math.sin(2 * math.pi * 440 * frame / RATE))
        result.extend(struct.pack("<h", sample))
    return bytes(result)


PCM = pcm_bytes()
WAV = (b"RIFF" + struct.pack("<I", 36 + len(PCM)) + b"WAVEfmt " +
       struct.pack("<IHHIIHH", 16, 1, 1, RATE, RATE * 2, 2, 16) +
       b"data" + struct.pack("<I", len(PCM)) + PCM)


class SpeechFixture(http.server.ThreadingHTTPServer):
    daemon_threads = False

    def __init__(self, log=None, partial=False, auth_failure=False):
        super().__init__(("127.0.0.1", 0), SpeechHandler)
        self.records, self.failures = [], []
        self.lock, self.log = threading.Lock(), log
        self.partial = partial
        self.auth_failure_pending = bool(auth_failure)
        self.partial_ready = threading.Event()
        self.partial_release = threading.Event()
        self.partial_closed = threading.Event()
        self.partial_done = threading.Event()
        self.partial_bytes = 0
        self.handler_threads = set()

    def process_request_thread(self, request, client_address):
        with self.lock:
            self.handler_threads.add(threading.current_thread())
        try:
            super().process_request_thread(request, client_address)
        finally:
            if self.partial:
                self.partial_done.set()

    @property
    def endpoint(self):
        return f"http://127.0.0.1:{self.server_port}/v1"

    def record(self, value):
        with self.lock:
            self.records.append(value)
            if self.log is not None:
                self.log.write(json.dumps(value, ensure_ascii=False) + "\n")
                self.log.flush()

    def claim_auth_failure(self):
        with self.lock:
            fail = self.auth_failure_pending
            self.auth_failure_pending = False
            return fail

    def queue_auth_failure(self):
        with self.lock:
            if self.auth_failure_pending:
                raise ValueError("Synthetic authentication failure is already pending")
            self.auth_failure_pending = True


class SpeechHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def setup(self):
        self.request.settimeout(5)
        super().setup()

    def do_POST(self):
        try:
            if self.path != "/v1/audio/speech":
                raise ValueError("unexpected path")
            if self.headers.get("Authorization") != "Bearer synthetic-demo-key":
                raise ValueError("expected fixture credential")
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 256 * 1024:
                raise ValueError("unexpected fixture request size")
            body = json.loads(self.rfile.read(length))
            if set(body) - {"input", "instructions", "model", "voice", "response_format", "speed", "stream_format"}:
                raise ValueError("unexpected request fields")
            if body.get("input") not in (TEXT, AUDITION):
                raise ValueError("expected synthetic text")
            if body.get("instructions", "") not in ("", INSTRUCTIONS, DEFAULT_INSTRUCTIONS):
                raise ValueError("expected synthetic instructions")
            if body.get("model") not in ("gpt-4o-mini-tts", "gpt-4o-mini-tts-2025-12-15", "tts-1"):
                raise ValueError("unexpected fixture model")
            if body.get("model") == "tts-1" and ("instructions" in body or body.get("voice") != "alloy"):
                raise ValueError("legacy fixture requires Alloy and omitted Instructions")
            if body.get("voice") not in ("coral", "ash", "ballad", "alloy"):
                raise ValueError("unexpected fixture voice")
            if body.get("response_format") not in ("wav", "pcm") or body.get("speed", 1) not in (1, 1.25):
                raise ValueError("unexpected audio settings")
            if body.get("stream_format", "audio") != "audio":
                raise ValueError("expected finite audio")
            if self.server.claim_auth_failure():
                self.server.record({"method": "POST", "path": self.path, "body": body,
                                    "response_status": 401, "request_id": FAILURE_REQUEST_ID,
                                    "response_bytes": len(AUTH_FAILURE),
                                    "response_sha256": hashlib.sha256(AUTH_FAILURE).hexdigest()})
                self.send_response(401)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(AUTH_FAILURE)))
                self.send_header("X-Request-ID", FAILURE_REQUEST_ID)
                self.end_headers()
                self.wfile.write(AUTH_FAILURE)
                return
            payload = WAV if body["response_format"] == "wav" else PCM
            self.server.record({"method": "POST", "path": self.path, "body": body,
                                "response_bytes": len(payload), "response_sha256": hashlib.sha256(payload).hexdigest(),
                                "frames": FRAMES, "sample_rate": RATE, "duration_seconds": 2})
            self.send_response(200)
            self.send_header("Content-Type", "audio/wav" if body["response_format"] == "wav" else "application/octet-stream")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            if self.server.partial:
                self.wfile.write(payload[:2048])
                self.wfile.flush()
                self.server.partial_bytes = 2048
                self.server.partial_ready.set()
                # Hold the rest until cancellation. Cleanup releases this gate
                # after failed assertions, so server_close can join the handler.
                while not self.server.partial_release.is_set():
                    if select.select([self.connection], [], [], .05)[0]:
                        if self.connection.recv(1, socket.MSG_PEEK) == b"":
                            self.server.partial_closed.set()
                            return
                        raise ValueError("unexpected data during partial response")
            else:
                self.wfile.write(payload)
        except (BrokenPipeError, ConnectionResetError):
            if self.server.partial_ready.is_set():
                self.server.partial_closed.set()
        except (ValueError, KeyError, TypeError, json.JSONDecodeError):
            self.server.failures.append("Synthetic speech request validation failed")
            self.send_error(400, "Synthetic fixture request rejected")


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: fixture.py ADDRESS_FILE REQUEST_LOG")
    stopped = threading.Event()
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        server = SpeechFixture(log)
        worker = threading.Thread(target=server.serve_forever)
        worker.start()
        try:
            Path(sys.argv[1]).write_text(server.endpoint, encoding="utf-8")
            for number in (signal.SIGINT, signal.SIGTERM):
                signal.signal(number, lambda *_: stopped.set())
            stopped.wait()
        finally:
            server.partial_release.set()
            server.shutdown()
            server.server_close()
            worker.join(5)
        if server.failures:
            raise SystemExit("Synthetic fixture validation failed")


if __name__ == "__main__":
    main()
