#!/usr/bin/env python3
"""Check one Bash Models filter/sort/limit command against a finite loopback response."""
import errno
import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import stat
import sys
import tempfile
import threading
import time


HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("picker_harness", HERE / "image_picker_harness.py")
picker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(picker)
COMMAND = ('exec openai --base-url "$MODEL_FIXTURE_BASE/after/v1" --api-key synthetic '
           '--format jsonl models list --filter \'id~"^demo-text-2026-10-01-00[12]$"\' '
           '--sort-by id --max-items 1')
EXPECTED = (b'{"id":"demo-text-2026-10-01-001","object":"model","created":1700000000,'
            b'"owned_by":"system","shutdown_date":"2030-01-01"}\n')


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: check-wsl-models.py CLI BASH")
    binary, bash = (str(Path(value).resolve(strict=True)) for value in sys.argv[1:])
    name = "models-filter-sort-limit"
    deadline = time.monotonic() + 25
    root = Path(tempfile.mkdtemp(prefix="openai-wsl-models-"))
    identity = root.stat()
    owned = []
    server = worker = None
    port = None
    lock = threading.Lock()
    received = threading.Event()
    requests = {"count": 0, "accepted": 0, "failures": []}
    report = {"case": name, "exit_code": 1, "cleanup_complete": False,
              "stdout_eof": False, "stderr_eof": False, "failures": [], "command": COMMAND}

    def interrupted(signum, _frame):
        raise TimeoutError("Models fixture interrupted or exceeded25seconds")

    for selected_signal in (signal.SIGALRM, signal.SIGTERM, signal.SIGINT):
        signal.signal(selected_signal, interrupted)
    signal.setitimer(signal.ITIMER_REAL, 25)
    try:
        owners = ("openai", "system", "demo-research-team")
        items = [{"id": f"demo-text-2026-10-01-{index:03d}", "object": "model", "created": 1700000000,
                  "owned_by": owners[index % len(owners)],
                  "shutdown_date": "2030-01-01" if index % 2 else None}
                 for index in range(47, -1, -1)]
        body = json.dumps({"object": "list", "data": items}, separators=(",", ":")).encode()
        body_hash = hashlib.sha256(body).hexdigest()
        assert len(body) == 5834 and body_hash == "65a35e98b76235a982e331fe094c5d044e3e6380c7bf2de51655459ff77f6d47"

        class ModelsFixture(http.server.BaseHTTPRequestHandler):
            def setup(self):
                super().setup()
                self.connection.settimeout(2)

            def log_message(self, *_):
                pass

            def parse_request(self):
                accepted = super().parse_request()
                if accepted:
                    with lock:
                        requests["count"] += 1
                return accepted

            def send_error(self, code, message=None, explain=None):
                with lock:
                    if not requests["failures"]:
                        requests["failures"].append("unexpected synthetic HTTP error")
                super().send_error(code, message, explain)

            def do_GET(self):
                with lock:
                    valid = requests["count"] == 1 and self.path == "/after/v1/models"
                    valid = valid and self.headers.get("Authorization") == "Bearer synthetic"
                    if not valid:
                        if not requests["failures"]:
                            requests["failures"].append("unexpected or repeated synthetic request")
                if not valid:
                    self.send_error(400)
                    return
                try:
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    self.wfile.flush()
                    with lock:
                        requests["accepted"] += 1
                    received.set()
                except (OSError, TimeoutError):
                    with lock:
                        if not requests["failures"]:
                            requests["failures"].append("synthetic response write failed")

            def do_POST(self):
                with lock:
                    if not requests["failures"]:
                        requests["failures"].append("unexpected synthetic method")
                self.send_error(405)

        server = http.server.HTTPServer(("127.0.0.1", 0), ModelsFixture)
        port = server.server_port
        worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02})
        worker.start()
        bindir = root / "bin"
        bindir.mkdir(mode=0o700)
        (bindir / "openai").symlink_to(binary)
        home = root / "home"
        home.mkdir(mode=0o700)
        for directory in ("tmp", "config", "cache", "data"):
            (home / directory).mkdir(mode=0o700)
        env = {"PATH": str(bindir) + ":/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(home),
               "USERPROFILE": str(home), "XDG_CONFIG_HOME": str(home / "config"),
               "XDG_CACHE_HOME": str(home / "cache"), "XDG_DATA_HOME": str(home / "data"),
               "TMPDIR": str(home / "tmp"), "SHELL": bash, "TERM": "dumb", "NO_COLOR": "1",
               "CI": "true", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8", "GOMAXPROCS": "2",
               "MODEL_FIXTURE_BASE": f"http://127.0.0.1:{port}"}
        selected_locale, locale_source = picker.fixture_utf8_locale(os.environ.get("LC_ALL"), env, owned)
        env.update(LANG=selected_locale, LC_ALL=selected_locale)
        report.update(locale=selected_locale, locale_source=locale_source)
        assert time.monotonic() + 15 <= deadline, "insufficient time for the full Models command"
        got = picker.capture_fixture([bash, "--noprofile", "--norc", "-c", COMMAND],
                                     env=env, cwd=home, timeout=15, owned_processes=owned)
        report.update(cli_call_count=1, cli_exit_code=got.returncode,
                      stdout_eof=got.stdout_eof, stderr_eof=got.stderr_eof,
                      stdout_bytes=len(got.stdout), stderr_bytes=len(got.stderr),
                      stdout_sha256=hashlib.sha256(got.stdout).hexdigest())
        assert got.returncode == 0 and got.stderr == b"", "Models command status or stderr differed"
        assert got.stdout == EXPECTED, "complete Models JSONL bytes differed"
        assert received.wait(timeout=max(0, min(2, deadline - time.monotonic()))), "missing accepted request"
        settings = [home / ".openai", home / "config/openai", home / "Library/Application Support/openai"]
        assert all(not path.exists() and not path.is_symlink() for path in settings), "Models command wrote OpenAI settings"
        report.update(stdout_utf8=got.stdout.decode("utf-8"), openai_settings_absent=True,
                      response_bytes=len(body), response_sha256=body_hash)
        assert time.monotonic() < deadline, "Models scene exceeded25seconds"
        report["exit_code"] = 0
    except BaseException as error:
        report["failures"].append(str(error)[:2000])
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        cleanup_errors = []
        for process in owned:
            try:
                if process.poll() is None:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        # The owned group exited; still reap the child and verify pipe EOF.
                        pass
                process.wait(timeout=2)
                if not process.fixture_stdout_eof or not process.fixture_stderr_eof:
                    cleanup_errors.append("Models pipe EOF was not observed")
            except BaseException as error:
                cleanup_errors.append("child cleanup: " + str(error)[:1000])
        if server is not None:
            try:
                if worker is not None and worker.is_alive():
                    server.shutdown()
            except BaseException as error:
                cleanup_errors.append("fixture shutdown: " + str(error)[:1000])
            try:
                server.server_close()
            except BaseException as error:
                cleanup_errors.append("fixture close: " + str(error)[:1000])
        report["fixture_thread_joined"] = False
        if worker is not None:
            try:
                worker.join(timeout=2)
                assert not worker.is_alive(), "Models fixture thread remained alive"
                report["fixture_thread_joined"] = True
            except BaseException as error:
                cleanup_errors.append("fixture join: " + str(error)[:1000])
        if port is not None and (worker is None or report["fixture_thread_joined"]):
            try:
                with socket.socket() as probe:
                    probe.settimeout(1)
                    result = probe.connect_ex(("127.0.0.1", port))
                report["listener_errno"] = result
                assert result == errno.ECONNREFUSED, "Models fixture listener remained open"
            except BaseException as error:
                cleanup_errors.append("fixture listener: " + str(error)[:1000])
        with lock:
            report.update(request_count=requests["count"], accepted_request_count=requests["accepted"])
            if requests != {"count": 1, "accepted": 1, "failures": []}:
                report["exit_code"] = 1
                report["failures"].append("request count or fixture failure")
        if not cleanup_errors:
            try:
                info = root.lstat()
                assert stat.S_ISDIR(info.st_mode) and (info.st_dev, info.st_ino) == (identity.st_dev, identity.st_ino)
                shutil.rmtree(root)
                assert not root.exists()
                report["cleanup_complete"] = True
            except BaseException as error:
                cleanup_errors.append("root cleanup: " + str(error)[:1000])
        if cleanup_errors:
            report["exit_code"] = 1
            report["failures"].extend(cleanup_errors)
    report["cleanup_scope"] = "Owned child, observed pipe EOF, fixture worker/listener, and private root; no complete descendant census."
    encoded = json.dumps(report, separators=(",", ":"))
    assert len(encoded.encode()) <= 65536, "Models receipt exceeded its bound"
    print("RECEIPT: " + encoded, flush=True)
    if report["exit_code"] == 0 and report["cleanup_complete"]:
        print("PASS: " + name, flush=True)
        return 0
    print("FAIL: " + name, file=sys.stderr, flush=True)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
