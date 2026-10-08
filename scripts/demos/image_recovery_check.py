#!/usr/bin/env python3
"""Exercise image draft recovery through a native zsh PTY and loopback API.

Usage: image_recovery_check.py BINARY OUTPUT_DIRECTORY [--baseline]
       image_recovery_check.py BINARY OUTPUT_DIRECTORY --scene success|retry [--baseline]
No live credentials, paid requests, third-party Python modules, or terminal backend setup.
"""

import argparse
import base64
import fcntl
import hashlib
import http.server
import json
import os
import pathlib
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time


PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP438AAAAQBAYDFKhhdAAAAAElFTkSuQmCC"
PROMPT = "A synthetic orange robot"


class Fixture:
    def __init__(self, fail=False, delay=0.4):
        self.requests = []
        self.fail = fail
        self.delay = delay
        self.error_status = 400
        self.partial = False
        fixture = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *unused):
                pass

            def do_POST(self):
                request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                fixture.requests.append(request)
                index = len(fixture.requests)
                time.sleep(fixture.delay)
                failed = index <= int(fixture.fail)
                body = {"error": {"message": "Synthetic request failure", "type": "invalid_request_error"}} if failed else {
                    "created": 1704067200,
                    "data": [{"b64_json": PNG} for _ in range(request.get("n", 1))],
                }
                if fixture.partial:
                    body["data"][1]["b64_json"] = "synthetic-invalid-base64"
                data = json.dumps(body).encode()
                try:
                    self.send_response(fixture.error_status if failed else 200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(data)))
                    self.end_headers()
                    self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError):
                    pass  # Cancellation deliberately closes the client connection.

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = "http://127.0.0.1:%d/v1" % self.server.server_port

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()


def environment(home, fixture):
    return dict(PATH="/usr/bin:/bin", HOME=str(home), LANG="en_US.UTF-8", TERM="xterm-256color",
                SHELL="/bin/zsh", OPENAI_PICKER_SHELL="zsh", OPENAI_API_KEY="synthetic-recovery-key",
                OPENAI_BASE_URL=fixture.url, NO_COLOR="1", GOMAXPROCS="2")


class Process:
    def __init__(self, binary, env, args=None, mirror=False):
        self.output = bytearray()
        self.status = None
        self.mirror = mirror
        pid, self.fd = pty.fork()
        if pid == 0:
            os.execve("/bin/zsh", ["zsh", "-f", "-c", 'exec "$@"', "image-recovery",
                                  str(binary), *(args or ["images", "generate"])], env)
        self.pid = pid
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", 28, 104, 0, 0))

    def pump(self, duration=0.1):
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            if select.select([self.fd], [], [], min(0.05, max(0, deadline - time.monotonic())))[0]:
                try:
                    data = os.read(self.fd, 65536)
                except OSError:
                    data = b""
                self.output.extend(data)
                if self.mirror and data:
                    sys.stdout.buffer.write(data)
                    sys.stdout.buffer.flush()
            if self.status is None:
                pid, status = os.waitpid(self.pid, os.WNOHANG)
                if pid:
                    self.status = os.waitstatus_to_exitcode(status)
            if self.status is not None:
                break

    def wait_for(self, condition, timeout=6):
        deadline = time.monotonic() + timeout
        while not condition():
            self.pump()
            if not condition() and (self.status is not None or time.monotonic() > deadline):
                raise AssertionError("process exited or timed out before expected event; status=%s" % self.status)

    def send(self, value):
        os.write(self.fd, value if isinstance(value, bytes) else value.encode())

    def ready(self):
        self.wait_for(lambda: b"Create image" in self.output)
        self.pump(0.2)

    def submit(self, prompt=PROMPT):
        self.send(prompt)
        self.pump(0.15)
        self.send(b"\r")

    def stop(self):
        if self.status is None:
            self.send(b"\x03")
            self.wait_for(lambda: self.status is not None)
        return self.status

    def close(self):
        if self.status is None:
            os.kill(self.pid, signal.SIGKILL)
            os.waitpid(self.pid, 0)
        os.close(self.fd)


def saved(home):
    files = sorted(home.rglob("*.png"))
    assert all(file.read_bytes() == base64.b64decode(PNG) for file in files), "saved bytes changed"
    return [str(file.relative_to(home)) for file in files]


def preference(home, count="1"):
    path = home / "Library/Application Support/openai/image-picker.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(dict(version=1, model="gpt-image-2", size="1536x1024", quality="high",
                                   background="opaque", format="png", count=count, output_dir="")))
    path.chmod(0o600)
    return path


def run_case(binary, output, case, baseline=False, mirror=False):
    fixture = Fixture(fail=case == "retry", delay=2 if case == "cancel-request" else 0.4)
    if case == "retry-500":
        fixture.fail, fixture.error_status = 3, 500
    fixture.partial = case == "partial-save"
    process = None
    result = {"case": case}
    try:
        with tempfile.TemporaryDirectory(prefix="image-recovery-") as directory:
            home = pathlib.Path(directory)
            env = environment(home, fixture)
            if case in ("flags", "repeat-count", "partial-save"):
                preference(home, "2")
            previous_state = {path: path.read_bytes() for path in home.rglob("image-picker.json")}
            direct = case in ("flags", "json", "json-pipe", "pipe-input", "pipe-output", "json-error")
            if direct:
                fixture.fail = case == "json-error"
                args = [str(binary)]
                if case in ("json", "json-pipe", "json-error"):
                    args += ["--format", "json", "--format-error", "json"]
                args += ["images", "generate"]
                if case != "pipe-input":
                    args += ["--prompt", PROMPT]
                if case == "flags":
                    args += ["--model", "gpt-image-2.5-sunburst", "--size", "1024x1024", "--quality", "low", "--count", "1"]
                if case in ("flags", "json"):
                    process = Process(binary, env, args[1:])
                    process.wait_for(lambda: process.status is not None)
                    assert process.status == 0
                    assert b"Create image" not in process.output
                else:
                    data = json.dumps({"prompt": PROMPT}).encode() if case == "pipe-input" else b""
                    completed = subprocess.run(args, input=data, env=env, capture_output=True, timeout=8)
                    result.update(exit_status=completed.returncode, stdout=completed.stdout.decode(), stderr=completed.stderr.decode())
                    assert completed.returncode == (1 if case == "json-error" else 0)
                    assert b"Create image" not in completed.stdout + completed.stderr
                    assert b"\x1b" not in completed.stdout + completed.stderr
                    if case == "json-error":
                        json.loads(completed.stderr)
                    if case == "json-pipe":
                        json.loads(completed.stdout)
                assert len(fixture.requests) == 1
                if case == "flags":
                    request = fixture.requests[0]
                    assert (request["model"], request["size"], request["quality"], request["n"]) == (
                        "gpt-image-2.5-sunburst", "1024x1024", "low", 1)
            else:
                process = Process(binary, env, mirror=mirror)
                process.ready()
                if case == "cancel-picker":
                    assert process.stop() == 130
                    assert not fixture.requests
                else:
                    if case == "tabs":
                        process.send(PROMPT)
                        process.send(b"\t\t\t")
                        process.pump(0.2)
                        process.send(b"\r")
                    else:
                        process.submit()
                    process.wait_for(lambda: len(fixture.requests) == 1)
                    if case == "cancel-request":
                        assert process.stop() == 130
                    elif case == "partial-save":
                        process.wait_for(lambda: process.status is not None)
                        assert process.status == 1 and len(fixture.requests) == 1
                    elif case == "restart-draft":
                        process.pump(1.5)
                        assert process.stop() == 130
                        (output / (case + "-first.pty.txt")).write_bytes(process.output)
                        process.close()
                        process = Process(binary, env)
                        process.ready()
                        if baseline:
                            assert b"Describe your image" in process.output and PROMPT.encode() not in process.output
                        else:
                            assert PROMPT.encode() in process.output, "reopening lost the saved draft"
                        assert process.stop() == 130 and len(fixture.requests) == 1
                    elif case in ("queued-enter", "held-enter", "held-ctrl-g", "paste"):
                        if case == "queued-enter":
                            process.send(b"\r" * 50)
                        elif case == "paste":
                            process.send(b"\x1b[200~queued text\r\x07\r\x1b[201~")
                        else:
                            for _ in range(38):
                                process.send(b"\r" if case == "held-enter" else b"\x07")
                                process.pump(0.05)
                        process.pump(1.1)
                        assert len(fixture.requests) == 1, "queued input started another paid request"
                        assert process.stop() == 130
                    else:
                        if case == "retry-500":
                            process.wait_for(lambda: any(note in process.output for note in (
                                b"Your draft is kept", b"Your prompt and settings are still here")), timeout=10)
                            assert len(fixture.requests) == 3, "SDK retry count changed"
                            process.pump(1)
                        else:
                            process.pump(1.7)
                        if baseline and case == "retry":
                            assert process.status == 1, "baseline unexpectedly recovered"
                        else:
                            assert process.status is None, "draft session exited"
                            if case != "repeat-count":
                                process.send(" with a blue hat")
                            process.pump(0.3)
                            process.send(b"\r")
                            request_count = 4 if case == "retry-500" else 2
                            process.wait_for(lambda: len(fixture.requests) == request_count)
                            expected = (" with a blue hat" if baseline else PROMPT + " with a blue hat")
                            if case == "repeat-count":
                                expected = PROMPT
                            assert fixture.requests[-1]["prompt"] == expected, "draft prompt was not retained"
                            process.pump(1.2)
                            assert len(fixture.requests) == request_count
                            assert process.stop() == 130
            files = saved(home)
            if case == "repeat-count":
                assert len(files) == 4 and len(set(files)) == 4, "batch saving replaced an earlier image"
            if case in ("partial-save", "retry-500", "restart-draft"):
                assert len(files) == 1
            if case in ("success", "tabs"):
                assert len(files) == 2
            if case == "retry":
                assert len(files) == (0 if baseline else 1)
            state_files = list(home.rglob("image-picker.json"))
            if direct:
                assert {path: path.read_bytes() for path in state_files} == previous_state, "direct command changed picker state"
            elif case == "cancel-picker":
                assert not state_files, "untouched cancellation created a draft"
            else:
                assert len(state_files) == 1, "expected exactly one private picker record"
                path = state_files[0]
                assert path.stat().st_mode & 0o777 == 0o600, "picker record is not private"
                state = json.loads(path.read_text())
                fields = {"version", "model", "size", "quality", "background", "format", "count", "output_dir"}
                if baseline:
                    assert set(state) == fields and state["version"] == 1, "baseline settings schema changed"
                    assert PROMPT not in path.read_text(), "baseline persisted prompt text"
                else:
                    request = fixture.requests[-1]
                    expected = dict(version=2, prompt=request["prompt"], model=request["model"],
                                    size=request["size"], quality=request["quality"],
                                    background=request["background"], format=request["output_format"],
                                    count=str(request["n"]), output_dir="")
                    assert state == expected, "saved draft differs from the last confirmed selection"
            result.update(requests=fixture.requests, saved_files=files,
                          preferences=[json.loads(path.read_text()) for path in state_files])
            if process:
                result["exit_status"] = process.status
                (output / (case + ".pty.txt")).write_bytes(process.output)
    finally:
        if process:
            (output / (case + ".pty.txt")).write_bytes(process.output)
            process.close()
        (output / (case + ".requests.json")).write_text(json.dumps(fixture.requests, indent=2) + "\n")
        fixture.close()
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("output", type=pathlib.Path)
    parser.add_argument("--baseline", action="store_true")
    parser.add_argument("--scene", choices=["success", "retry"])
    parser.add_argument("--case", action="append", dest="selected_cases")
    args = parser.parse_args()
    args.binary = args.binary.resolve()
    args.output.mkdir(parents=True, exist_ok=True)
    cases = args.selected_cases or ([args.scene] if args.scene else (["success", "retry"] if args.baseline else [
        "success", "retry", "queued-enter", "held-enter", "held-ctrl-g", "paste", "cancel-picker",
        "cancel-request", "flags", "json", "json-pipe", "pipe-input", "pipe-output", "json-error", "repeat-count",
        "retry-500", "partial-save", "restart-draft", "tabs"]))
    results = []
    for case in cases:
        try:
            if args.scene:
                print("\033[2J\033[HNative zsh PTY | synthetic loopback API | " + ("Before" if args.baseline else "After"))
                print("$ openai images generate", flush=True)
            result = run_case(args.binary, args.output, case, args.baseline, bool(args.scene))
            result["result"] = "pass"
        except Exception as error:
            result = dict(case=case, result="fail", error=str(error))
        results.append(result)
        if args.scene:
            time.sleep(2)
        else:
            print("\n%s: %s" % (result["result"].upper(), case), flush=True)
    report = dict(binary=str(args.binary), sha256=hashlib.sha256(args.binary.read_bytes()).hexdigest(),
                  platform=os.uname().sysname + " " + os.uname().machine,
                  shell="/bin/zsh -f", baseline=args.baseline, cases=results)
    (args.output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    return int(any(result["result"] != "pass" for result in results))


if __name__ == "__main__":
    raise SystemExit(main())
