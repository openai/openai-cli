#!/usr/bin/env python3
"""Observe the real zsh suggestion widget in an owned, fixed-size PTY.

Run under the shared private_pty resource allocation. This uses local help,
never submits a selected command, and does not change an installed shell.
The repository's pinned pyte observer supplies viewport and history cells.
"""

import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import tempfile
import termios
import time


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


class Terminal:
    def __init__(self, pyte, binary, hook, home, columns, rows, prompt):
        self.columns = columns
        self.screen = pyte.HistoryScreen(columns, rows, history=10000)
        self.stream = pyte.ByteStream(self.screen)
        self.raw = bytearray()
        self.pending = b''
        self.status = None
        self.home = home
        (home / 'bin').mkdir()
        (home / 'bin' / 'openai').symlink_to(binary)
        (home / 'hook.zsh').write_text(hook)
        (home / 'prompt').write_text(prompt)
        (home / '.zshrc').write_text('''
PROMPT=$(<"$HOME/prompt")
PROMPT2='> '
RPROMPT=''
unset HISTFILE
SAVEHIST=0
bindkey -e
stty opost onlcr
source "$HOME/hook.zsh"
capture_buffer() {
  printf '%s\\0%d\\0' "$BUFFER" "$CURSOR" >> "$HOME/captures"
}
zle -N capture_buffer
bindkey '^X^G' capture_buffer
bindkey '^X^R' openai_suggestion_restore
preexec() { print -r -- "$1" >> "$HOME/commands"; }
print -r -- 'PRIOR_TRANSCRIPT_SENTINEL'
''')
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack('HHHH', rows, columns, 0, 0))
            os.execve('/bin/zsh', ['zsh', '-d', '-i'], {
                'HOME': str(home), 'ZDOTDIR': str(home),
                'PATH': str(home/'bin')+':/usr/bin:/bin',
                'TERM': 'xterm-256color', 'NO_COLOR': '1',
                'OPENAI_BASE_URL': 'http://127.0.0.1:1', 'LANG': 'en_US.UTF-8',
            })
        self.owned_groups = {self.pid}

    def pump(self, duration=.05):
        deadline = time.monotonic()+duration
        while time.monotonic() < deadline:
            try:
                group = os.tcgetpgrp(self.fd)
                if group > 0:
                    self.owned_groups.add(group)
            except OSError:
                # Ownership was not observable; retain only previously observed groups.
                pass
            if not select.select([self.fd], [], [], max(0, deadline-time.monotonic()))[0]:
                continue
            try:
                data = os.read(self.fd, 65536)
            except OSError:
                return
            if not data:
                return
            self.raw.extend(data)
            if len(self.raw) > 2**20:
                raise AssertionError('Terminal trace exceeded 1 MiB')
            # pyte does not understand Kitty's keyboard-protocol pop. It does
            # not move the cursor or change cells. Preserve the full raw trace.
            value = re.sub(rb'\x1b\[<[0-9]*u', b'', self.pending+data)
            partial = re.search(rb'\x1b(?:\[(?:<[0-9]*)?)?$', value)
            self.pending = value[partial.start():] if partial else b''
            self.stream.feed(value[:partial.start()] if partial else value)

    def wait(self, predicate, label, timeout=10):
        deadline = time.monotonic()+timeout
        while time.monotonic() < deadline:
            if predicate():
                return
            self.pump()
        raise AssertionError(label+'\n'+'\n'.join(self.screen.display))

    def send(self, data):
        os.write(self.fd, data)

    def transcript(self):
        if len(self.screen.history.top) >= 10000 or self.screen.history.bottom:
            raise AssertionError('Incomplete observer history')
        history = [''.join(line.get(column, self.screen.default_char).data
                           for column in range(self.columns))
                   for line in self.screen.history.top]
        lines = [line.rstrip() for line in history+self.screen.display]
        while lines and not lines[-1]:
            lines.pop()
        return lines

    def capture(self, number):
        self.send(b'\x18\x07')
        path = self.home/'captures'
        self.wait(lambda: path.exists() and len(path.read_bytes().split(b'\0')) == number*2+1,
                  'Buffer widget did not return')
        self.pump(.1)
        fields = path.read_bytes().split(b'\0')
        return fields[-3].decode(), int(fields[-2])

    def close(self):
        # The retained child PID cannot be reused before waitpid. Never signal
        # historical foreground groups, which may have already ended.
        groups = {self.pid}
        try:
            foreground = os.tcgetpgrp(self.fd)
            if foreground > 0 and os.getsid(foreground) == self.pid:
                groups.add(foreground)
        except OSError:
            # Ownership was not observable; historical groups are unsafe signal targets.
            pass
        if self.status is None:
            for group in groups:
                try:
                    os.killpg(group, signal.SIGHUP)
                except ProcessLookupError:
                    # The owned group may exit before signal delivery.
                    pass
            deadline = time.monotonic()+2
            while time.monotonic() < deadline:
                ended, status = os.waitpid(self.pid, os.WNOHANG)
                if ended:
                    self.status = os.waitstatus_to_exitcode(status)
                    break
                self.pump()
            if self.status is None:
                try:
                    os.killpg(self.pid, signal.SIGKILL)
                except ProcessLookupError:
                    # The child may exit between the wait and forced cleanup.
                    pass
                _, status = os.waitpid(self.pid, 0)
                self.status = os.waitstatus_to_exitcode(status)
        self.remaining_groups = []
        for group in groups:
            try:
                os.killpg(group, 0)
            except ProcessLookupError:
                continue
            self.remaining_groups.append(group)
        os.close(self.fd)


def wrapped_lines(text, columns):
    return [part[start:start+columns].rstrip()
            for part in text.split('\n') for start in range(0, max(1, len(part)), columns)]


def run_case(pyte, binary, hook, output, name, columns, rows, prompt, draft):
    snapshots, checks, failure = [], [], None
    with tempfile.TemporaryDirectory(prefix='command-widget-') as directory:
        home = Path(directory)
        terminal = Terminal(pyte, binary, hook, home, columns, rows, prompt)
        (output/(name+'.owner.json')).write_text(json.dumps({'owned_root_pid': terminal.pid})+'\n')
        try:
            terminal.wait(lambda: terminal.transcript()[-1:] == ['$'], 'Initial prompt missing')
            terminal.send(b'openai models --help\r')
            terminal.wait(lambda: 'Key setup: openai help setup' in terminal.transcript()
                          and terminal.transcript()[-1:] == ['$'], 'Local help did not return')
            initial = terminal.transcript()
            prior = initial[:initial.index('Key setup: openai help setup')+1]
            selected = 'openai models retrieve --help'
            terminal.send(b'\x1b[200~'+draft.encode()+b'\x1b[201~'+b'\x02'*4)
            initial_buffer, initial_cursor = terminal.capture(1)
            if (initial_buffer, initial_cursor) != (draft, len(draft)-4):
                raise AssertionError('Initial editable draft or cursor changed')
            draft_rows = terminal.transcript()[len(prior):]
            draft_cursor_row = len(terminal.screen.history.top)+terminal.screen.cursor.y-len(prior)
            draft_cursor_column = terminal.screen.cursor.x
            selected_rows = wrapped_lines(prompt+selected, columns)
            capture_number = 1
            for phase, key, expected, cursor in [
                ('select', b'\r', selected, len(selected)),
                ('restore', None, draft, len(draft)-4),
                ('cancel-escape', b'\x1b', draft, len(draft)-4),
                ('cancel-interrupt', b'\x03', draft, len(draft)-4),
                ('select-again', b'\r', selected, len(selected)),
                ('restore-again', None, draft, len(draft)-4),
            ]:
                if key is None:
                    terminal.send(b'\x18\x12')
                    terminal.pump(.1)
                else:
                    terminal.send(b'\x18\x09')
                    terminal.wait(lambda: 'Search' in '\n'.join(terminal.screen.display), 'Picker did not open')
                    terminal.send(b'retrieve')
                    terminal.wait(lambda: re.search(r'Search\s+retrieve', '\n'.join(terminal.screen.display)),
                                  'Search did not update')
                    terminal.pump(.15)
                    start = len(terminal.raw)
                    terminal.send(key)
                    terminal.wait(lambda: b'\x1b[?2004l' in terminal.raw[start:], 'Picker did not restore input mode')
                    terminal.pump(.1)
                capture_number += 1
                # Observe immediately after picker/restore returns. An extra
                # inspection widget must not repair a broken display first.
                transcript = terminal.transcript()
                snapshots.append({'phase': phase, 'cells_before_capture': terminal.screen.display[:],
                                  'transcript_before_capture': transcript,
                                  'cursor_before_capture': [terminal.screen.cursor.x, terminal.screen.cursor.y]})
                if transcript[:len(prior)] != prior:
                    raise AssertionError(f'{phase}: prior help transcript overwritten')
                block = selected_rows if expected == selected else draft_rows
                relative_row = len(selected_rows)-1 if expected == selected else draft_cursor_row
                column = len(selected_rows[-1]) if expected == selected else draft_cursor_column
                start = len(terminal.screen.history.top)+terminal.screen.cursor.y-relative_row
                if transcript[start:start+len(block)] != block or terminal.screen.cursor.x != column:
                    raise AssertionError(f'{phase}: editable prompt block not rendered at cursor: '
                                         f'{transcript[start:start+len(block)]!r}')
                # Every post-help row must be a known prompt block or one
                # exact diagnostic. Unknown partial rows expose picker residue.
                tail = transcript[len(prior):]
                allowed_blocks = [draft_rows, selected_rows,
                                  ['Command inserted. Enter runs it.'], ['Command selection canceled.']]
                while tail:
                    found = next((known for known in allowed_blocks if tail[:len(known)] == known), None)
                    if found is None:
                        raise AssertionError(f'{phase}: unexpected post-widget rows: {tail!r}')
                    tail = tail[len(found):]
                buffer, actual_cursor = terminal.capture(capture_number)
                snapshots[-1].update(buffer=buffer, cursor=actual_cursor)
                if (buffer, actual_cursor) != (expected, cursor):
                    raise AssertionError(f'{phase}: draft/cursor changed: {(buffer, actual_cursor)!r}')
                if (home/'commands').read_text() != 'openai models --help\n':
                    raise AssertionError(f'{phase}: a selected command was submitted')
                checks.append({'phase': phase, 'prior_transcript_preserved': True,
                               'prompt_visible': True, 'draft_and_cursor_exact': True,
                               'selected_command_not_submitted': True})
        except Exception as error:
            failure = str(error) or type(error).__name__
        finally:
            terminal.close()
            (output/(name+'.raw')).write_bytes(terminal.raw)
            (output/(name+'.screens.json')).write_text(json.dumps(snapshots, indent=2)+'\n')
            for filename in ('captures', 'commands'):
                if (home/filename).exists():
                    (output/(name+'.'+filename)).write_bytes((home/filename).read_bytes())
    return {'name': name, 'checks': checks, 'failure': failure,
            'owned_root_reaped': terminal.status is not None,
            'owned_root_pid': terminal.pid,
            'observed_foreground_groups': sorted(terminal.owned_groups),
            'groups_requiring_forced_cleanup': terminal.remaining_groups}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=Path)
    parser.add_argument('--hook', type=Path, required=True)
    parser.add_argument('--observer-source-directory', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    args.binary = args.binary.resolve(strict=True)
    args.hook = args.hook.resolve(strict=True)
    args.output.mkdir(parents=True, exist_ok=False)
    def interrupted(_number, _frame):
        raise KeyboardInterrupt
    for number in (signal.SIGHUP, signal.SIGTERM):
        signal.signal(number, interrupted)
    loader_path = Path(__file__).resolve().parent/'audio-workbench'/'observer.py'
    spec = importlib.util.spec_from_file_location('command_widget_observer', loader_path)
    loader = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loader)
    pyte = loader._verified_packages(args.observer_source_directory)
    identity = digest(args.binary)
    hook_bytes = args.hook.read_bytes()
    hook_identity = hashlib.sha256(hook_bytes).hexdigest()
    hook = hook_bytes.decode().replace('__APPNAME__', 'openai').replace('__DOUBLE_TAB__', '0')
    cases = [run_case(pyte, args.binary, hook, args.output, *case) for case in [
        ('framed', 108, 36, '$ ', 'openai --version'),
        ('near-bottom', 76, 23, '$ ', 'openai --version'),
        ('wrapped-draft', 40, 12, '$ ', "openai responses create --input 'this is a local draft'"),
        ('multiline', 76, 23, 'Context\n$ ', 'openai responses create \\\n  --help'),
    ]]
    report = {'binary': str(args.binary), 'binary_sha256': identity,
              'binary_unchanged': identity == digest(args.binary),
              'hook': str(args.hook), 'hook_sha256': hook_identity,
              'hook_unchanged': hook_identity == digest(args.hook),
              'probe_sha256': digest(Path(__file__)),
              'observer_loader_sha256': digest(loader_path),
              'observer_manifest_sha256': loader.MANIFEST_SHA256,
              'cases': cases,
              'limits': 'Owned native zsh PTY and pinned software observer; no GUI or resize claim.'}
    (args.output/'results.json').write_text(json.dumps(report, indent=2)+'\n')
    print(json.dumps({'checks_passed': sum(len(case['checks']) for case in cases),
                      'failures': [case for case in cases if case['failure']]}))
    return int(not report['binary_unchanged'] or not report['hook_unchanged'] or
               any(case['failure'] or case['groups_requiring_forced_cleanup'] for case in cases))


if __name__ == '__main__':
    raise SystemExit(main())
