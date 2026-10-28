#!/usr/bin/env python3
"""Relay exact CLI bytes and send reviewed keys after rendered markers appear."""
import errno
import codecs
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time


def visible(events, width, height, runtime):
    cast = runtime / 'screen.cast'
    text = runtime / 'screen.txt'
    cast.write_text('\n'.join(json.dumps(value) for value in [
        {'version': 2, 'width': width, 'height': height}, *events]) + '\n')
    result = subprocess.run([os.environ['DEMO_ASCIINEMA'], 'convert', '--overwrite', '-f', 'txt', str(cast), str(text)],
                            capture_output=True, timeout=5,
                            env={'PATH': '/usr/bin:/bin', 'HOME': str(runtime),
                                 'ASCIINEMA_CONFIG_HOME': str(runtime / 'config'),
                                 'ASCIINEMA_STATE_HOME': str(runtime / 'state')})
    if result.returncode:
        raise RuntimeError(result.stderr.decode('utf-8', 'replace'))
    return text.read_text()


def main():
    if len(sys.argv) < 2 or not all(os.isatty(fd) for fd in (0, 1, 2)):
        raise SystemExit('scene.py requires a command and terminal stdin/stdout/stderr')
    actions = json.loads(os.environ['DEMO_ACTIONS_JSON'])
    size = os.get_terminal_size(1)
    original = termios.tcgetattr(1)
    pid, master = pty.fork()
    if pid == 0:
        try:
            fcntl.ioctl(1, termios.TIOCSWINSZ, struct.pack('HHHH', size.lines, size.columns, 0, 0))
            os.execvp(sys.argv[1], sys.argv[1:])
        except Exception:
            os._exit(99)
    waited = False
    try:
        relay = original[:]
        relay[1] &= ~termios.OPOST
        termios.tcsetattr(1, termios.TCSADRAIN, relay)
        digest, events, inputs = hashlib.sha256(), [], []
        decoder = codecs.getincrementaldecoder('utf-8')()
        started = time.monotonic()
        pending = None
        with tempfile.TemporaryDirectory(prefix='viewer-scene-') as temporary:
            runtime = Path(temporary)
            while True:
                now = time.monotonic()
                if now - started > 30:
                    raise TimeoutError('viewer scene exceeded 30 seconds')
                if pending is not None and now >= pending:
                    action = actions[len(inputs)]
                    key = action['key'].encode()
                    if os.write(master, key) != len(key):
                        raise OSError('short input write')
                    inputs.append({'time': now - started, 'key': action['key'], 'ready': action['marker']})
                    pending = None
                if not select.select([master], [], [], 0.05)[0]:
                    continue
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    break
                if not chunk:
                    break
                digest.update(chunk)
                decoded = decoder.decode(chunk)
                if decoded:
                    events.append([time.monotonic() - started, 'o', decoded])
                if sys.stdout.buffer.write(chunk) != len(chunk):
                    raise OSError('short terminal relay write')
                sys.stdout.buffer.flush()
                if pending is None and len(inputs) < len(actions):
                    action = actions[len(inputs)]
                    screen = visible(events, size.columns, size.lines, runtime)
                    if action['marker'] in screen and action['footer'] in screen:
                        pending = time.monotonic() + 1.5
        decoder.decode(b'', final=True)
        _, status = os.waitpid(pid, 0)
        waited = True
        code = os.waitstatus_to_exitcode(status)
        if len(inputs) != len(actions):
            raise AssertionError(('scene exited before its actions completed', inputs, actions))
        evidence = {'sha256': digest.hexdigest(), 'exit_status': code, 'inputs': inputs}
        with open(os.environ['DEMO_RELAY_EVIDENCE'], 'x', encoding='utf-8') as output:
            json.dump(evidence, output, indent=2)
            output.write('\n')
        return code
    finally:
        try:
            os.close(master)
            if not waited:
                try:
                    os.kill(pid, signal.SIGKILL)
                except ProcessLookupError:
                    # The child can exit before SIGKILL during teardown.
                    pass
                os.waitpid(pid, 0)
        finally:
            termios.tcsetattr(1, termios.TCSADRAIN, original)


if __name__ == '__main__':
    result = main()
    raise SystemExit(result if result >= 0 else 128 - result)
