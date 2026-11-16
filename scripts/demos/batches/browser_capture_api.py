#!/usr/bin/env python3
"""Serve the existing read-only Batch browser fixture for terminal recordings."""
import argparse
import os
from pathlib import Path
import signal
import sys
from http.server import HTTPServer

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from try_browser import BrowserFixture
from workflow_fixture import write_new


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("address", type=Path)
    parser.add_argument("log", type=Path)
    args = parser.parse_args()
    stopping = False

    def stop(*_):
        nonlocal stopping
        stopping = True

    for name in (signal.SIGINT, signal.SIGTERM):
        signal.signal(name, stop)
    descriptor = os.open(args.log, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as log:
        fixture = BrowserFixture(log)
        with HTTPServer(("127.0.0.1", 0), fixture.handler()) as server:
            server.timeout = 0.1
            write_new(args.address, f"http://127.0.0.1:{server.server_port}/v1".encode())
            while not stopping:
                server.handle_request()
    if fixture.failures:
        raise SystemExit("Synthetic browser fixture failed; inspect the retained request log.")


if __name__ == "__main__":
    main()
