#!/usr/bin/env python3
"""Exercise native image helper death and cancellation through the real CLI.

Run with python3 -I -B scripts/check-image-output-lifecycle.py BINARY OUTPUT.
Uses private PTYs and synthetic loopback responses. Requires ps and, on macOS,
lsof permission to inspect this test's own children. No real terminal settings
or production APIs are used; native graphical appearance is not verified.
"""
import argparse
import base64
import importlib.util
import json
import os
import pathlib
import random
import re
import signal
import struct
import subprocess
import sys
import tempfile
import threading
import time
import zlib


def load_script(name):
    spec = importlib.util.spec_from_file_location(
        name, pathlib.Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


picker = load_script('image_picker_harness')
compatibility = load_script('check-image-output-compatibility')


def fixture_png(large=False):
    width, height, channels = (1024, 1024, 3) if large else (2, 1, 4)
    pixels = random.Random(342).randbytes(width * height * channels) if large else compatibility.PIXELS
    stride = width * channels
    rows = b''.join(b'\x00' + pixels[i:i + stride] for i in range(0, len(pixels), stride))

    def chunk(kind, data):
        return (struct.pack('>I', len(data)) + kind + data
                + struct.pack('>I', zlib.crc32(kind + data) & 0xffffffff))

    return (b'\x89PNG\r\n\x1a\n'
            + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 2 if large else 6, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b''))


def process_group(group):
    # Read executable names, never unrelated process arguments or environments.
    output = subprocess.check_output(
        ['/bin/ps', '-axo', 'pid=,ppid=,pgid=,stat=,comm='], text=True)
    result = {}
    for line in output.splitlines():
        fields = line.split(None, 4)
        if len(fields) == 5 and int(fields[2]) == group and not fields[3].startswith('Z'):
            result[int(fields[0])] = {'parent': int(fields[1]), 'state': fields[3], 'executable': fields[4]}
    return result


def pipe_count(pid):
    if sys.platform == 'linux':
        count = 0
        for path in pathlib.Path('/proc', str(pid), 'fd').iterdir():
            try:
                count += os.readlink(path).startswith('pipe:')
            except FileNotFoundError:
                pass  # An unrelated descriptor in this owned process closed.
        return count
    output = subprocess.check_output(
        ['/usr/sbin/lsof', '-nP', '-a', '-p', str(pid), '-Fft'], text=True)
    return output.splitlines().count('tPIPE')


def helpers(terminal):
    return {pid: info for pid, info in process_group(terminal.child.pid).items()
            if pid != terminal.child.pid}


def signal_helper(terminal, pid, value):
    # Every process in this private session's group descends from our CLI.
    # Only select a live, observed helper; never signal a name-matched process.
    assert pid in helpers(terminal), 'owned helper disappeared before signal'
    os.kill(pid, value)


def stopped_helpers(terminal, timeout=4):
    deadline = time.monotonic() + timeout
    while True:
        remaining = helpers(terminal)
        if not remaining or time.monotonic() >= deadline:
            return remaining
        time.sleep(0.04)  # Deliberately do not release terminal backpressure.


def close_terminal(terminal):
    # The CLI is our session leader. While it is alive, its group cannot be
    # reused. If it died, kill only still-observed members of that owned group.
    if terminal.child.poll() is None:
        try:
            os.killpg(terminal.child.pid, signal.SIGKILL)
        except ProcessLookupError:
            # The process group exited between poll() and killpg().
            pass
    else:
        for pid in helpers(terminal):
            try:
                signal_helper(terminal, pid, signal.SIGKILL)
            except (ProcessLookupError, AssertionError):
                # The owned helper exited between observation and signaling.
                pass
    terminal.close()


class Fixture(picker.http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        self.server.requests.append(json.loads(self.rfile.read(int(self.headers['Content-Length']))))
        self.server.request_started.set()
        self.server.release.wait(15)
        data = json.dumps({'data': [{'b64_json': base64.b64encode(self.server.png).decode()}]}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass  # Failure cases deliberately disconnect.


def run_case(binary, output, name, png):
    result = {'case': name, 'passed': False}
    with tempfile.TemporaryDirectory(prefix='image-output-lifecycle-') as temporary:
        home = pathlib.Path(temporary)
        server = picker.http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
        server.png, server.requests = png, []
        server.request_started, server.release = threading.Event(), threading.Event()
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        env = {'PATH': '/usr/bin:/bin', 'HOME': str(home), 'TERM': 'xterm-kitty',
               'TERM_PROGRAM': 'kitty', 'LANG': 'en_US.UTF-8', 'CI': 'false',
               'OPENAI_API_KEY': 'synthetic-lifecycle-key',
               'OPENAI_BASE_URL': f'http://127.0.0.1:{server.server_port}/v1'}
        terminal = None
        try:
            terminal = picker.Terminal(binary, ['images', 'generate', '--prompt', 'synthetic lifecycle',
                                               '--inline', 'on', '--output-dir', str(home), '--name', 'result'], env)
            picker.wait_for_request(terminal, server.request_started)
            idle = {pid: pipe_count(pid) for pid in helpers(terminal)}
            assert idle, 'no prepared native helper observed'
            result['idle_pipe_counts'] = idle
            server.release.set()
            terminal.wait('\x1b_G')
            if name == 'normal':
                terminal.finish(0, expect_picker=False)
                frames = re.findall(rb'\x1b_G([^;]*);(.*?)\x1b\\', terminal.raw, re.S)
                assert len(frames) == terminal.raw.count(b'\x1b_G') == 1, 'incomplete image output'
                compatibility.check_fixture_png(base64.b64decode(frames[0][1], validate=True))
                result['exact_pixel_previews'] = 1
            else:
                # A blocked job retains its data and cancellation pipes. Observe
                # the increase instead of depending on helper spawn order: this
                # selects the old cat supervisor or a redesigned direct worker.
                topology = helpers(terminal)
                supervisors = {info['parent'] for info in topology.values()
                               if pathlib.Path(info['executable']).name == 'cat' and info['parent'] in idle}
                counts = {pid: pipe_count(pid) for pid in idle if pid in topology}
                active = list(supervisors) if supervisors else [pid for pid, count in counts.items()
                                                               if count > idle[pid]]
                result['topology_before'] = topology
                result['active_pipe_counts'] = counts
                assert len(active) == 1, ('cannot identify active output owner', active)
                result['active_owner'] = active[0]
                started = time.monotonic()
                if name == 'owner-death':
                    signal_helper(terminal, active[0], signal.SIGKILL)
                    terminal.child.send_signal(signal.SIGINT)
                elif name == 'cancel':
                    terminal.child.send_signal(signal.SIGINT)
                else:
                    terminal.child.send_signal(signal.SIGTERM if name == 'parent-term' else signal.SIGKILL)
                remaining = stopped_helpers(terminal)
                result['helpers_after_deadline'] = remaining
                result['helper_stop_seconds'] = time.monotonic() - started
                # Preserve failing evidence, then release/kill only owned work
                # in finally. Draining here would hide a blocked-output leak.
                assert not remaining, 'native output survived cancellation or owner death'
                code = {'owner-death': 130, 'cancel': 130, 'parent-term': -signal.SIGTERM,
                        'parent-kill': -signal.SIGKILL}[name]
                terminal.finish(code, expect_picker=False)
                result['exit'] = code
            assert not helpers(terminal), 'helper survived CLI completion'
            assert (home / 'result.png').read_bytes() == png, 'saved original changed'
            assert len(server.requests) == 1, 'unexpected API request count'
            # Exit and joined helpers establish the writer boundary. Linux can
            # retain more queued PTY bytes than Terminal.finish's short drain;
            # they are old output, not proof of a write after process exit.
            drain_deadline = time.monotonic() + 3
            quiet_until = time.monotonic() + 0.1
            while time.monotonic() < quiet_until:
                before = len(terminal.raw)
                terminal.read(0.02)
                if len(terminal.raw) != before:
                    quiet_until = time.monotonic() + 0.1
                assert time.monotonic() < drain_deadline, 'terminal queue did not become quiet'
            after = len(terminal.raw)
            until = time.monotonic() + 0.2
            while time.monotonic() < until:
                terminal.read(0.02)
            assert len(terminal.raw) == after, 'output continued after command completion'
            result.update(passed=True, saved_originals=1, requests=1, exit=terminal.child.returncode)
        except Exception as error:
            result['error'] = str(error)
            result['exit_before_cleanup'] = terminal.child.poll() if terminal is not None else None
            saved = home / 'result.png'
            result['saved_original_preserved'] = saved.is_file() and saved.read_bytes() == png
        finally:
            server.release.set()
            try:
                if terminal is not None:
                    try:
                        terminal.save(output / name)
                    finally:
                        close_terminal(terminal)
            finally:
                server.shutdown()
                server.server_close()
                thread.join()
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    parser.add_argument('output')
    args = parser.parse_args()
    if sys.platform not in ('darwin', 'linux'):
        parser.error('native helper lifecycle checks require macOS or Linux')
    binary = str(pathlib.Path(args.binary).resolve())
    output = pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    summary = output / 'results.json'
    summary.unlink(missing_ok=True)
    small, large = fixture_png(), fixture_png(large=True)
    results = []
    for name in ('normal', 'cancel', 'owner-death', 'parent-term', 'parent-kill'):
        result = run_case(binary, output, name, small if name == 'normal' else large)
        results.append(result)
        summary.write_text(json.dumps(results, indent=2) + '\n')
        print(('PASS' if result['passed'] else 'FAIL'), name, result.get('error', ''), flush=True)
    return 0 if all(result['passed'] for result in results) else 1


if __name__ == '__main__':
    sys.exit(main())
