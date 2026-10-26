#!/usr/bin/env python3
"""Share PTY helpers and dispatch the current pager observer."""
import importlib.util
import json
import os
import pathlib
import re
import runpy
import subprocess
import sys
import tempfile
import termios
import time

spec = importlib.util.spec_from_file_location(
    'navigation_terminal', pathlib.Path(__file__).with_name('image_picker_harness.py'))
terminal_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(terminal_module)
ASCIINEMA = 'asciinema'
FOOTER = 'q: quit'
LEGACY_HINTS = ('p: print', 'p: all', 'i: inline')

def drain(terminal, duration=0.25):
    deadline = time.monotonic() + duration
    while time.monotonic() < deadline:
        terminal.read(min(0.03, max(0, deadline-time.monotonic())))


def reconstruct_text(events, width, height):
    with tempfile.TemporaryDirectory(prefix='list-navigation-screen-') as temporary:
        recording = pathlib.Path(temporary)/'screen.cast'
        header = {'version': 2, 'width': width, 'height': height}
        recording.write_text('\n'.join(json.dumps(value) for value in [header, *events])+'\n')
        result = subprocess.run([ASCIINEMA, 'convert', '-f', 'txt', str(recording), str(pathlib.Path(temporary)/'screen.txt')],
                                capture_output=True, text=True, check=True, timeout=5,
                                env={'PATH': os.environ.get('PATH', '/usr/bin:/bin'),
                                     'HOME': temporary, 'ASCIINEMA_STATE_HOME': temporary+'/state',
                                     'ASCIINEMA_CONFIG_HOME': temporary+'/config'})
        return (pathlib.Path(temporary)/'screen.txt').read_text()


def screen_text(terminal):
    # Tea emits cursor-addressed differences. Stripping ANSI cannot reconstruct
    # the visible value (item_001 can become item_002 by writing only "2").
    # Reuse asciinema's terminal emulator, already required by our demo tooling.
    cache_key = (len(terminal.raw), len(terminal.events))
    if getattr(terminal, '_screen_key', None) == cache_key:
        return terminal._screen_text
    result = reconstruct_text(terminal.events, *terminal.initial_size)
    terminal._screen_key, terminal._screen_text = cache_key, result
    return result



def wait_screen(terminal, marker):
    deadline = time.monotonic()+12
    while marker not in screen_text(terminal):
        terminal.read()
        assert terminal.child.poll() is None, terminal.text()[-1500:]
        assert time.monotonic() < deadline, ('screen marker missing', marker, screen_text(terminal)[-1500:])


def finish(terminal, expected, timeout=6):
    deadline = time.monotonic() + timeout
    while terminal.child.poll() is None:
        terminal.read()
        assert time.monotonic() < deadline, 'command did not exit: ' + terminal.text()[-1500:]
    drain(terminal, 0.1)
    assert terminal.child.returncode == expected, (terminal.child.returncode, expected, terminal.text()[-2000:])
    restored = termios.tcgetattr(terminal.slave)
    initial = terminal.initial[:]
    if sys.platform == 'darwin':
        restored[3] &= ~termios.PENDIN
        initial[3] &= ~termios.PENDIN
    assert restored == initial, 'terminal input mode was not restored'
    for mode in (b'25', b'2004'):
        on = terminal.raw.rfind(b'\x1b[?' + mode + b'h')
        off = terminal.raw.rfind(b'\x1b[?' + mode + b'l')
        if mode == b'25' and off >= 0:
            assert on > off, 'cursor remained hidden'
        if mode == b'2004' and on >= 0:
            assert off > on, 'bracketed paste remained enabled'
    alternate = False
    for control in re.finditer(rb'\x1b\[\?(?:47|1047|1049)([hl])|\x1b\[([23])J', terminal.raw):
        mode, erase = control.groups()
        if mode:
            alternate = mode == b'h'
        else:
            assert alternate and erase == b'2', 'pager erased original history'
    assert not alternate, 'pager did not restore the original screen'




def navigation_footer(width, complete=False):
    """Return the control that remains visible on every pager page."""
    return FOOTER


if __name__ == '__main__':
    runpy.run_path(str(pathlib.Path(__file__).parent/'demos/list-pager/observe.py'), run_name='__main__')
