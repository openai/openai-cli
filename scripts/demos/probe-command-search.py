#!/usr/bin/env python3
"""Probe the isolated search trial through its native zsh prompt."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import signal
import struct
import termios
import time

parser = argparse.ArgumentParser()
parser.add_argument('launcher')
parser.add_argument('--output', required=True)
args = parser.parse_args()
launcher = Path(args.launcher).resolve()
root = launcher.parent
previous = (root / 'latest-run.txt').read_text() if (root / 'latest-run.txt').exists() else ''
pid, terminal = pty.fork()
if pid == 0:
    os.execve('/bin/bash', ['bash', str(launcher)], {'PATH': '/usr/bin:/bin', 'TERM': 'xterm-256color'})
fcntl.ioctl(terminal, termios.TIOCSWINSZ, struct.pack('HHHH', 30, 100, 0, 0))
pending = bytearray()
trace = bytearray()
checks = []
reaped = False
run = None
failure = None
completed = False


def pump(duration=0.05):
    end = time.monotonic() + duration
    while time.monotonic() < end:
        if select.select([terminal], [], [], max(0, end-time.monotonic()))[0]:
            try:
                data = os.read(terminal, 65536)
            except OSError:
                return
            if not data:
                return
            pending.extend(data)
            trace.extend(data)


def wait_for(predicate, label, timeout=12):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if predicate():
            return
        pump()
    raise AssertionError(label + ': ' + bytes(pending)[-1800:].decode(errors='replace'))


def expect(text):
    needle = text.encode()
    wait_for(lambda: needle in pending, 'Missing ' + repr(text))
    end = pending.index(needle) + len(needle)
    del pending[:end]


def send(text):
    os.write(terminal, text.encode() if isinstance(text, str) else text)


def read(name):
    return (run / name).read_text()


def shortcut():
    pending.clear()
    send(b'\x18\x09')
    expect('Search')


def explore():
    # A redraw also contains the title. Ignore previous frames before opening
    # another picker, so submission baselines follow the new shell invocation.
    pending.clear()
    before = read('submitted.txt')
    send('openai explore\r')
    wait_for(lambda: read('submitted.txt') == before+'[another command was submitted]\n',
             'Explore invocation did not reach the shell')
    expect('Explore commands')


def choose(query, command, standalone=False):
    before = read('submitted.txt')
    send(query)
    pump(.15)
    send('\r')
    wait_for(lambda: read('inserted.txt') == command+'\n', 'Selection did not fill prompt')
    expect('trial $ ' if standalone else 'Command inserted. Enter runs it.')
    pump(.15)
    assert read('submitted.txt') == before, 'Selection submitted a command'
    assert read('cursor.txt') == str(len(command))+'\n'


try:
    wait_for(lambda: (root/'latest-run.txt').exists() and (root/'latest-run.txt').read_text() != previous,
             'No isolated run')
    run = Path((root/'latest-run.txt').read_text().strip())
    wait_for(lambda: (run/'ready.txt').exists(), 'Trial not ready')
    expect('trial $ ')
    assert read('ready.txt').startswith('1 ')
    assert read('inserted.txt') == read('submitted.txt') == ''
    checks.append('isolated installer enables the hook with empty logs')

    shortcut()
    choose('retrieve', 'openai models retrieve --help')
    checks.append('recent search consumes selection Enter without submission')
    send(b'\x18r')
    pump(.1)
    send('print -r -- SYNTHETIC_CURSOR_TEXT')
    send(b'\x02'*4)
    shortcut()
    send(b'\x1b[200~ignored\ncommand\x1b[201~')
    pump(.15)
    assert read('submitted.txt') == ''
    send(b'\x1b')
    expect('Command selection canceled.')
    send('X\r')
    expect('SYNTHETIC_CURSOR_XTEXT\r\n')
    expect('trial $ ')
    checks.append('multiline paste cannot select; Escape preserves draft and cursor')

    send('openai files --help\r')
    expect('Key setup: openai help setup')
    expect('trial $ ')
    shortcut()
    choose('retrieve', 'openai files retrieve --help')
    before = read('submitted.txt')
    send('\r')
    expect('Key setup: openai help setup')
    expect('trial $ ')
    assert read('submitted.txt') == before+'openai files retrieve --help\n'
    checks.append('Files selection fills; a separate Enter submits exact help')

    explore()
    choose('models --help', 'openai models --help', standalone=True)
    checks.append('standalone explore fills the next prompt without submission')
    send(b'\x18r')
    pump(.1)
    for query, command in [('models list', 'openai models list'),
                           ('list models', 'openai models list'),
                           ('on', 'openai images inline on')]:
        explore()
        choose(query, command, standalone=True)
        checks.append('ranked search fills exact command without submission: ' + query)
        send(b'\x18r')
        pump(.1)
    for query in ['scribe', 'create-variation', 'zzzz-no-command']:
        explore()
        before = read('submitted.txt')
        send(query)
        expect('No matching commands')
        send('\r')
        pump(.15)
        assert read('submitted.txt') == before
        send(b'\x1b')
        expect('Command selection canceled.')
        expect('trial $ ')
        checks.append('unmatched or retired query cannot submit: ' + query)
    send('openai models --help\r')
    expect('Key setup: openai help setup')
    expect('trial $ ')
    shortcut()
    send('COMMAND')
    expect('No matching commands')
    send(b'\x1b')
    expect('Command selection canceled.')
    expect('trial $ ')
    checks.append('recent suggestions exclude the literal COMMAND syntax guide')
    send('openai models --help\r')
    expect('Key setup: openai help setup')
    expect('trial $ ')
    send('print -r -- CANCEL_OK')
    shortcut()
    send(b'\x03')
    expect('Command selection canceled.')
    send('\r')
    expect('CANCEL_OK\r\n')
    expect('trial $ ')
    checks.append('Ctrl-C preserves the shell draft')

    send('openai models --help\r')
    expect('Key setup: openai help setup')
    expect('trial $ ')
    shortcut()
    fcntl.ioctl(terminal, termios.TIOCSWINSZ, struct.pack('HHHH', 10, 30, 0, 0))
    expect('Resize to at least')
    before = read('submitted.txt')
    send('\r')
    pump(.15)
    assert read('submitted.txt') == before
    fcntl.ioctl(terminal, termios.TIOCSWINSZ, struct.pack('HHHH', 30, 100, 0, 0))
    expect('Search')
    send(b'\x1b')
    expect('Command selection canceled.')
    checks.append('small terminal refuses selection and recovers after resize')

    send('openai models ret\t')
    pump(.2)
    send('--help\r')
    expect('Key setup: openai help setup')
    expect('trial $ ')
    checks.append('ordinary Tab completion remains available')
    for name in ['inserted.txt', 'submitted.txt', 'cursor.txt', 'setup.txt', 'ready.txt']:
        assert 'SYNTHETIC_CURSOR' not in read(name)
    checks.append('trial evidence excludes draft text')
    send('exit\r')
    wait_for(lambda: not (run/'home').exists(), 'Temporary home remains')
    end = time.monotonic()+5
    while time.monotonic() < end:
        ended, status = os.waitpid(pid, os.WNOHANG)
        if ended:
            reaped = True
            assert os.waitstatus_to_exitcode(status) == 0
            break
        pump()
    assert reaped
    checks.append('normal exit cleans the temporary home')
    completed = True
except Exception as error:
    failure = str(error)
finally:
    if not completed and failure is None:
        failure = 'Probe interrupted before completion.'
    if not reaped:
        # Only terminate the owned session. No graphical terminal participates.
        try:
            os.killpg(pid, signal.SIGHUP)
        except ProcessLookupError:
            # The owned process group can exit before signal delivery.
            pass
        end = time.monotonic()+3
        while time.monotonic() < end:
            ended, _ = os.waitpid(pid, os.WNOHANG)
            if ended:
                reaped = True
                break
            pump()
        if not reaped:
            try:
                os.killpg(pid, signal.SIGKILL)
            except ProcessLookupError:
                # The group may exit between the wait and forced cleanup.
                pass
            os.waitpid(pid, 0)
            reaped = True
    os.close(terminal)
    output = Path(args.output)
    output.write_text(json.dumps({'checks': checks, 'failure': failure, 'run': str(run), 'owned_root_reaped': reaped}, indent=2)+'\n')
    output.with_suffix('.terminal.txt').write_bytes(trace)
if failure:
    raise SystemExit(failure)
print(json.dumps({'passed': len(checks), 'output': args.output}))
