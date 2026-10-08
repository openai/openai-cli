#!/usr/bin/env python3
"""Check picker feedback channels using separate UI and diagnostic PTYs.

Uses macOS/zsh terminals and synthetic loopback requests only.
Before: --case error --expect stderr. After: --case retry --case cancel --case direct --expect stdout.
"""

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import sys
import tempfile
import termios
import threading
import time

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO / 'scripts/demos'))
from image_recovery_check import Fixture, Process, PNG, environment, saved

PROMPT = 'SYNTHETIC_PICKER_PRIVATE'
RETRY = PROMPT + ' retry'
DIRECT = 'SYNTHETIC_DIRECT_PRIVATE'
WIDTHS = {'stdout': 96, 'stderr': 60}


class SplitProcess(Process):
    """Reuse picker input and lifecycle helpers while capturing channels separately."""

    def __init__(self, binary, env, args=None):
        self.streams = {'stdout': bytearray(), 'stderr': bytearray()}
        err_master, err_slave = pty.openpty()
        fcntl.ioctl(err_slave, termios.TIOCSWINSZ, struct.pack('HHHH', 28, WIDTHS['stderr'], 0, 0))
        try:
            super().__init__(binary, env, args, stderr_fd=err_slave)
        except BaseException:
            os.close(err_master)
            raise
        finally:
            os.close(err_slave)
        self.descriptors = [self.fd, err_master]
        self.readers = {self.fd: 'stdout', err_master: 'stderr'}
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack('HHHH', 28, WIDTHS['stdout'], 0, 0))

    def pump(self, duration=0.1):
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            ready, _, _ = select.select(list(self.readers), [], [], min(0.05, max(0, deadline - time.monotonic())))
            for descriptor in ready:
                try:
                    data = os.read(descriptor, 65536)
                except OSError:
                    data = b''
                if data:
                    self.output.extend(data)
                    self.streams[self.readers[descriptor]].extend(data)
                else:
                    self.readers.pop(descriptor)
            if self.status is None:
                pid, status = os.waitpid(self.pid, os.WNOHANG)
                if pid:
                    self.status = os.waitstatus_to_exitcode(status)
            if self.status is not None and not ready:
                break

    def close(self):
        if self.status is None:
            os.kill(self.pid, signal.SIGKILL)
            os.waitpid(self.pid, 0)
        for descriptor in self.descriptors:
            os.close(descriptor)


def fixture(case):
    api = Fixture()
    api.gates, api.events, api.errors = [], [], []
    original = api.server.RequestHandlerClass

    class Handler(original):
        def do_POST(self):
            api.events.append({'method': 'POST', 'path': self.path})
            request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            gate = threading.Event()
            api.gates.append(gate)
            api.requests.append(request)
            index = len(api.requests)
            if not gate.wait(8):
                api.errors.append('response gate timed out')
                return
            failed = index == 1 and case in ('error', 'retry', 'direct')
            body = {'error': {'message': 'Synthetic request failure', 'type': 'invalid_request_error'}} if failed else {
                'created': 1704067200, 'data': [{'b64_json': PNG}],
            }
            data = json.dumps(body).encode()
            try:
                self.send_response(401 if failed else 200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                self.wfile.write(data)
            except (BrokenPipeError, ConnectionResetError):
                pass  # Cancellation deliberately disconnects this synthetic client.

        def do_GET(self):
            api.events.append({'method': 'GET', 'path': self.path})
            self.send_response(400)
            self.send_header('Content-Length', '0')
            self.end_headers()

    api.server.RequestHandlerClass = Handler
    return api


def offsets(process):
    return {stream: len(data) for stream, data in process.streams.items()}


def loading(process, start, prompt, stream):
    label = b'Generating image' + (b" '" + prompt.encode() + b"'" if prompt else b'')

    def complete():
        current = process.streams[stream][start[stream]:]
        return label in current and b'1s elapsed' in current

    process.wait_for(complete, timeout=6)
    current = bytes(process.streams[stream][start[stream]:])
    other = 'stderr' if stream == 'stdout' else 'stdout'
    other_bytes = bytes(process.streams[other][start[other]:])
    assert re.search(rb'[|/\\-] +' + re.escape(label), current), 'spinner missing from selected stream'
    assert b'Ctrl+C to cancel' in current
    anchor = b'\r\x1b[J\x1b7\x1b[' + str(WIDTHS[stream]).encode() + b'G  \r'
    assert anchor in current, 'loading geometry did not use the selected file descriptor'
    assert label not in other_bytes, 'loading label appeared on both streams'
    assert b'Estimated' not in current
    if stream == 'stdout' or not prompt:
        assert PROMPT.encode() not in process.streams['stderr']
        assert RETRY.encode() not in process.streams['stderr']
        assert DIRECT.encode() not in process.streams['stderr']
    return label


def run(binary, output, case, expected):
    api = fixture(case)
    process = None
    output.mkdir(parents=True, exist_ok=False)
    try:
        with tempfile.TemporaryDirectory(prefix='picker-stream-check-') as temporary:
            home = Path(temporary)
            env = environment(home, api)
            env.update(LC_ALL='C', TERM_SESSION_ID='')
            args = None
            if case == 'direct':
                args = ['images', 'generate', '--prompt', DIRECT, '--model', 'gpt-image-2',
                        '--name', 'synthetic-safe-name', '--inline', 'off']
            process = SplitProcess(binary, env, args)
            selected = 'stderr' if case == 'direct' else expected
            start = offsets(process)
            if case != 'direct':
                process.ready()
                start = offsets(process)
                process.submit(PROMPT)
            process.wait_for(lambda: len(api.requests) == 1)
            first_label = loading(process, start, None if case == 'direct' else PROMPT, selected)
            if case == 'cancel':
                canceled_at = time.monotonic()
                assert process.stop() == 130
                cancellation_seconds = time.monotonic() - canceled_at
                assert cancellation_seconds < 1
            else:
                api.gates[0].set()
                if case == 'direct':
                    process.wait_for(lambda: process.status is not None)
                    assert process.status == 1
                else:
                    process.wait_for(lambda: b'Authentication failed' in process.streams['stderr'])
                    process.wait_for(lambda: bytes(process.streams['stdout']).count(b'Create image') >= 2)
                    process.pump(0.9)
                    reopened = bytes(process.streams['stdout'])
                    assert b'Generating image' not in reopened[reopened.rfind(b'Create image'):], 'loading redrew over the reopened picker'
                    diagnostic = bytes(process.streams['stderr'])
                    assert b'Generating image' not in diagnostic[diagnostic.index(b'Authentication failed'):], 'loading continued after the error'
                    if case == 'retry':
                        start_second = offsets(process)
                        process.send(' retry')
                        process.pump(0.2)
                        process.send(b'\r')
                        process.wait_for(lambda: len(api.requests) == 2)
                        loading(process, start_second, RETRY, selected)
                        second = process.streams[selected][start_second[selected]:]
                        assert first_label not in second, 'retry retained the old loading prompt'
                        api.gates[1].set()
                        process.wait_for(lambda: b'Saved image:' in process.streams['stdout'])
                        process.wait_for(lambda: bytes(process.streams['stdout']).count(b'Create image') >= 3)
                        process.pump(0.9)
                        stdout = bytes(process.streams['stdout'])
                        assert b'Images saved | ' in process.streams[selected]
                        if selected == 'stdout':
                            assert stdout.index(b'Images saved | ') < stdout.index(b'Saved image:')
                            assert b'Generating image' not in stdout[stdout.index(b'Saved image:'):]
                    assert process.stop() == 130

            count = 2 if case == 'retry' else 1
            assert len(api.requests) == len(api.events) == count
            assert all(event == {'method': 'POST', 'path': '/v1/images/generations'} for event in api.events)
            assert [r['prompt'] for r in api.requests] == ([PROMPT, RETRY] if case == 'retry' else [DIRECT if case == 'direct' else PROMPT])
            if case == 'retry':
                assert {k: v for k, v in api.requests[0].items() if k != 'prompt'} == {
                    k: v for k, v in api.requests[1].items() if k != 'prompt'}, 'retry changed request settings'
            assert not api.errors, api.errors
            captures = {stream: bytes(data) for stream, data in process.streams.items()}
            last_label = captures[selected].rfind(b'Generating image')
            assert captures[selected].rfind(b'\r\x1b[J') > last_label, 'final loading frame was not cleared'
            if expected == 'stdout' or case == 'direct':
                for prompt in (PROMPT, RETRY, DIRECT):
                    assert prompt.encode() not in captures['stderr']
            files = saved(home)
            assert len(files) == (1 if case == 'retry' else 0)
            record = {'case': case, 'passed': True, 'feedback_stream': selected,
                      'stdout_columns': WIDTHS['stdout'], 'stderr_columns': WIDTHS['stderr'],
                      'exit_status': process.status, 'requests': api.requests, 'events': api.events,
                      'saved_files': files, 'diagnostics_contain_picker_prompt': PROMPT.encode() in captures['stderr']}
            if case == 'cancel':
                record['cancellation_seconds'] = cancellation_seconds
            (output / 'result.json').write_text(json.dumps(record, indent=2) + '\n')
            return record
    finally:
        for gate in api.gates:
            gate.set()
        if process is not None:
            for stream, data in process.streams.items():
                (output / (stream + '.pty.bin')).write_bytes(data)
            process.close()
        (output / 'requests.json').write_text(json.dumps(api.requests, indent=2) + '\n')
        api.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--expect', choices=('stdout', 'stderr'), required=True)
    parser.add_argument('--case', choices=('error', 'retry', 'cancel', 'direct'), action='append', dest='cases', required=True)
    args = parser.parse_args()
    binary = args.binary.resolve()
    digest = hashlib.sha256(binary.read_bytes()).hexdigest()
    args.output.mkdir(parents=True, exist_ok=False)
    cases = [run(binary, args.output / case, case, args.expect) for case in args.cases]
    assert hashlib.sha256(binary.read_bytes()).hexdigest() == digest
    (args.output / 'results.json').write_text(json.dumps({'source_commit': args.source_commit,
        'binary': str(binary), 'sha256': digest, 'expected_picker_stream': args.expect, 'cases': cases}, indent=2) + '\n')
    print(json.dumps({'passed': len(cases), 'expected_picker_stream': args.expect}))


if __name__ == '__main__':
    main()
