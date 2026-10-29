#!/usr/bin/env python3
"""Exercise Models API pagination through an actual CLI process and a private terminal."""
import argparse
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import select
import signal
import socket
import subprocess
import tempfile
import threading
from urllib.parse import urlencode

spec = importlib.util.spec_from_file_location(
    "navigation", Path(__file__).with_name("check-list-navigation.py"))
navigation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(navigation)

FIRST = "/v1/models?limit=100"
TOKEN = "https://cursor.fixture.invalid/a?x=1+2&y=%/界"
SECOND = "/v1/models?" + urlencode([("limit", "100"), ("page", TOKEN)])


def record(identifier):
    return {"id": identifier, "object": "model", "owned_by": "fixture-owner"}


def page(identifier, more=False, cursor=None):
    return {"object": "page", "data": [record(identifier)], "has_more": more, "next": cursor}


class Fixture(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        server = self.server
        index = len(server.paths)
        server.paths.append(self.path)
        if index >= len(server.replies):
            self.send_error(409, "unexpected request")
            return
        status, payload = server.replies[index]
        if payload is None:
            server.pending.set()
            while not server.finished.is_set():
                if select.select([self.connection], [], [], 0.05)[0]:
                    if not self.connection.recv(1, socket.MSG_PEEK):
                        server.closed.set()
                        break
            return
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        if status == 503:
            self.send_header("Retry-After-Ms", "10")
        self.end_headers()
        self.wfile.write(body)


@contextmanager
def command(binary, prefix, replies, flags=(), height=16, redirect_stdout=False):
    with tempfile.TemporaryDirectory() as home, ThreadingHTTPServer(("127.0.0.1", 0), Fixture) as server:
        server.paths, server.replies = [], replies
        server.pending, server.closed, server.finished = (threading.Event() for _ in range(3))
        worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02}, daemon=True)
        worker.start()
        env = {"PATH": "/usr/bin:/bin", "HOME": home, "TERM": "xterm-256color", "NO_COLOR": "1",
               "LANG": "en_US.UTF-8", "OPENAI_CLI_NO_UPDATE_CHECK": "1", "GOMAXPROCS": "2",
               "OPENAI_API_KEY": "synthetic-models-cursor", "OPENAI_BASE_URL": f"http://127.0.0.1:{server.server_port}/v1"}
        if prefix:
            env["OPENAI_CLI_MAIN_DISPATCH_PROCESS"] = "1"
        terminal = None
        try:
            terminal = navigation.terminal_module.Terminal(binary, [*prefix, "models", "list", *flags],
                                                         env, width=90, height=height,
                                                         output_file=subprocess.DEVNULL if redirect_stdout else None)
            yield terminal, server
        finally:
            server.finished.set()
            close = [terminal.close] if terminal is not None else []
            navigation.terminal_module.cleanup_fixture_resources(*close, server.shutdown, lambda: worker.join(timeout=3))


def send_and_wait(terminal, key, marker):
    offset = len(terminal.raw)
    terminal.send(key)
    terminal.wait(marker, timeout=5, after=offset)


def quit_cleanly(terminal):
    terminal.send(b"q")
    navigation.finish(terminal, 0)


def successful_paging(binary, prefix):
    with command(binary, prefix, [(200, page("alpha-first", True, TOKEN)),
                                  (200, page("zephyr-last"))]) as (terminal, server):
        terminal.wait("alpha-first")
        terminal.resize(56, 10)
        navigation.drain(terminal, 0.15)
        terminal.resize(90, 16)
        navigation.drain(terminal, 0.15)
        assert server.paths == [FIRST], "initial display or resize fetched a continuation"
        send_and_wait(terminal, b" ", "zeph")
        assert server.paths == [FIRST, SECOND], "continuation changed route, token encoding, or page size"
        # These keys may revisit either cached page without requesting another page.
        for back, forward in [(b"k", b"j"), (b"\x1b[A", b"\x1b[B"), (b"u", b"d"), (b"\x15", b"\x04")]:
            send_and_wait(terminal, back, "alph")
            send_and_wait(terminal, forward, "zeph")
        send_and_wait(terminal, b"b", "alph")
        send_and_wait(terminal, b" ", "zeph")
        quit_cleanly(terminal)
        assert server.paths == [FIRST, SECOND], "cached navigation fetched another page"


def legacy_paging(binary, prefix):
    records = [record("zeta-last"), *[record(f"alpha-{i:03}") for i in reversed(range(100))]]
    with command(binary, prefix, [(200, {"object": "list", "data": records})], height=120) as (terminal, server):
        terminal.wait("alpha-000")
        terminal.wait("Showing 100 of 101 models.")
        send_and_wait(terminal, b" ", "zeta")
        terminal.wait("Showing 1 of 101 models.")
        send_and_wait(terminal, b"b", "alph")
        quit_cleanly(terminal)
        assert server.paths == [FIRST], "legacy response was downloaded twice"


def explicit_modes(binary, prefix):
    body = {"object": "list", "data": [record("alpha-first")]}
    for flags in [("--format", "auto"), ("--sort-by", "~id"), ("--max-items", "1"), ("--filter", "id=alpha-first")]:
        with command(binary, prefix, [(200, body)], flags) as (terminal, server):
            terminal.wait("alpha-first")
            quit_cleanly(terminal)
            assert server.paths == ["/v1/models"], "explicit options changed the request contract"
    for value in ["json", "raw"]:
        with command(binary, prefix, [(200, body)], ("--format", value)) as (terminal, server):
            navigation.finish(terminal, 0)
            assert server.paths == ["/v1/models"] and "alpha-first" in terminal.text()
    with command(binary, prefix, [(200, body)], redirect_stdout=True) as (terminal, server):
        navigation.finish(terminal, 0)
        assert server.paths == ["/v1/models"], "redirected output requested an API page"


def failing_pages(binary, prefix):
    for status in (401, 403):
        failure = {"error": {"code": "unsupported_parameter", "param": "limit",
                             "type": "invalid_request_error", "message": "synthetic denied"}}
        with command(binary, prefix, [(status, failure)]) as (terminal, server):
            navigation.finish(terminal, 1)
            assert server.paths == [FIRST], "authentication or authorization failure retried without the limit"
    for invalid in [page("beta-partial", True, TOKEN), page("beta-partial", True, None),
                    {"object": "page", "data": [], "has_more": True, "next": "different"}]:
        with command(binary, prefix, [(200, page("alpha-first", True, TOKEN)), (200, invalid)]) as (terminal, server):
            terminal.wait("alpha-first")
            terminal.send(b" ")
            navigation.finish(terminal, 1)
            printed = bytes(terminal.raw).rsplit(b"\x1b[?1049l", 1)
            assert len(printed) == 2 and printed[1].count(b"alpha-first") == 1, "previous page must print exactly once"
            if invalid["data"]:
                assert printed[1].count(b"beta-partial") == 1, "partial page must print exactly once"
            assert b"Output may be incomplete" in printed[1] and TOKEN.encode() not in printed[1]
            assert server.paths == [FIRST, SECOND], "bad continuation triggered another request"


def transient_failures(binary, prefix):
    first = (200, page("alpha-first", True, TOKEN))
    busy = (503, {"error": {"type": "server_error", "message": "fixture busy"}})
    with command(binary, prefix, [first, busy, (200, page("zephyr-last"))]) as (terminal, server):
        terminal.wait("alpha-first")
        send_and_wait(terminal, b" ", "zeph")
        quit_cleanly(terminal)
        assert server.paths == [FIRST, SECOND, SECOND], "a transient failure must retry the same cursor"
    with command(binary, prefix, [first, busy, busy, busy]) as (terminal, server):
        terminal.wait("alpha-first")
        terminal.send(b" ")
        navigation.finish(terminal, 1)
        printed = bytes(terminal.raw).rsplit(b"\x1b[?1049l", 1)
        assert len(printed) == 2 and printed[1].count(b"alpha-first") == 1, "previous page must print exactly once"
        assert b"HTTP 503" in printed[1] and TOKEN.encode() not in printed[1]
        assert server.paths == [FIRST, SECOND, SECOND, SECOND], "exhausted retries restarted the list"


def mixed_rollout(binary, prefix):
    # A legacy replica ignores both the page size and the continuation token.
    catalog = [record("alpha-first"), *[record(f"unaccepted-{i}") for i in range(12297)]]
    replies = [(200, page("alpha-first", True, TOKEN)), (200, {"object": "list", "data": catalog})]
    with command(binary, prefix, replies) as (terminal, server):
        terminal.wait("alpha-first")
        terminal.send(b" ")
        navigation.finish(terminal, 1)
        printed = bytes(terminal.raw).rsplit(b"\x1b[?1049l", 1)
        assert len(printed) == 2 and printed[1].count(b"alpha-first") == 1, "legacy continuation duplicated a row"
        assert b"unaccepted-" not in terminal.raw, "legacy continuation displayed unrelated rows"
        assert b"Output may be incomplete" in printed[1]
        assert server.paths == [FIRST, SECOND], "legacy continuation restarted the list"


def cancel_pending(binary, prefix):
    for later in (False, True):
        replies = [(200, page("alpha-first", True, TOKEN))] if later else []
        for action, code in [(b"q", 0), (b"\x03", 130), (signal.SIGINT, 130), (signal.SIGTERM, 143)]:
            with command(binary, prefix, [*replies, (200, None)]) as (terminal, server):
                terminal.wait("q: quit")
                if later:
                    terminal.wait("alpha-first")
                    terminal.send(b" ")
                assert server.pending.wait(5), "Models request did not reach the server"
                if isinstance(action, bytes):
                    terminal.send(action)
                else:
                    os.kill(terminal.child.pid, action)
                navigation.finish(terminal, code)
                assert server.closed.wait(3), "quit or signal left the HTTP request running"
                assert server.paths == ([FIRST, SECOND] if later else [FIRST])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    parser.add_argument("--test-binary", action="store_true")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    prefix = ["-test.run=^TestMainDispatchProcess$", "--", "openai"] if args.test_binary else []
    def interrupted(signum, frame):
        raise TimeoutError("pagination observer interrupted")

    signal.signal(signal.SIGTERM, interrupted)
    for check in [successful_paging, legacy_paging, explicit_modes, failing_pages, transient_failures,
                  mixed_rollout, cancel_pending]:
        check(binary, prefix)
        print(f"{check.__name__}: passed", flush=True)


if __name__ == "__main__":
    main()
