#!/usr/bin/env python3
"""Capture Webhooks and Skills from two real CLI binaries using synthetic data."""
import argparse
import json
from pathlib import Path
import runpy
import sys
import tempfile
import time

from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, str(Path(__file__).resolve().parent))
sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from fixture import serve
from image_picker_harness import Terminal, screen


WIDTH, HEIGHT = 76, 12
ROWS = HEIGHT - 2
navigation = runpy.run_path(str(Path(__file__).resolve().parents[2] / 'check-list-navigation.py'))


def capture(binary, resource, mode):
    args = [resource, 'list']
    if mode == 'browse':
        args.append('--browse')
    prefix = 'whe' if resource == 'webhooks' else 'skill'
    with serve(resource=resource, pages=3, rows=ROWS) as server, tempfile.TemporaryDirectory() as home:
        env = {'HOME': home, 'XDG_CONFIG_HOME': home, 'PATH': '/usr/bin:/bin', 'LANG': 'en_US.UTF-8',
               'TERM': 'xterm-256color', 'TERM_PROGRAM': 'Apple_Terminal', 'NO_COLOR': '1', 'PAGER': 'cat',
               'OPENAI_BASE_URL': f'http://127.0.0.1:{server.server_port}/v1',
               'OPENAI_CLI_NO_AUTO_UPDATE': '1', 'OPENAI_CLI_NO_UPDATE_CHECK': '1'}
        command = '$ openai ' + ' '.join(args)
        # Give both binaries the same shell line above their first result.
        launch = 'import os, sys; print(sys.argv[1], flush=True); os.execv(sys.argv[2], sys.argv[2:])'
        t = Terminal(sys.executable, ['-I', '-S', '-c', launch, command, str(binary), *args],
                     env, width=WIDTH, height=HEIGHT)
        try:
            if mode != 'browse':
                # Both default invocations must return without keyboard input.
                navigation['finish'](t, 0)
                first = b'\n'.join(t.raw.split(b'\n')[:HEIGHT])
                visible = screen(first, WIDTH, HEIGHT).decode()
                assert f'{prefix}_demo_001' in visible, visible
                assert f'{prefix}_demo_030' in t.text() and len(server.requests) == 3
                assert b'\x1b[?1049h' not in t.raw
                if mode == 'print':
                    assert 'NAME' in visible and 'Space: browse' not in t.text()
                return [visible]

            def wait_for_range(start, last, *, preview=False):
                deadline = time.monotonic() + 8
                while time.monotonic() < deadline:
                    t.read(.04)
                    visible = (screen(t.raw, WIDTH, HEIGHT).decode() if preview
                               else navigation['screen_text'](t)).rstrip('\n')
                    if (f'{resource.title()} {start}–{last}' in visible
                            and f'{prefix}_demo_{start:03}' in visible and f'{prefix}_demo_{last:03}' in visible):
                        return visible
                raise AssertionError(t.text())

            first = wait_for_range(1, 5, preview=True)
            assert len(server.requests) == 1, server.requests
            assert command in first, "the first table hid the command that opened it"
            assert 'Space: browse' in first, "preview controls must explain how to expand"
            assert b'\x1b[?1049h' not in t.raw, "preview replaced the shell screen"
            t.send(b' ')
            second = wait_for_range(6, 5 + ROWS)
            assert b'\x1b[?1049h' in t.raw, "Space did not open the full-screen view"
            t.send(b'b')
            wait_for_range(1, 5)
            assert len(server.requests) == 2, server.requests
            t.send(b'q')
            navigation['finish'](t, 0)
            return [first, second]
        finally:
            t.close()


def render(text, resource, caption, font):
    advance = font.getlength('0')
    width = round(advance * WIDTH) + 48
    image = Image.new('RGB', (width, 70 + HEIGHT * 23), '#0d1117')
    draw = ImageDraw.Draw(image)
    draw.rectangle((0, 0, width, 44), fill='#161b22')
    draw.text((20, 11), resource.title(), font=font, fill='#f0f6fc')
    draw.text((width - 20, 11), caption, font=font, fill='#b1bac4', anchor='rt')
    for row, line in enumerate(text.splitlines()):
        draw.text((20, 56 + row * 23), line, font=font, fill='#e6edf3')
    return image


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('before', type=Path)
    parser.add_argument('after', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--font', default='/System/Library/Fonts/Menlo.ttc')
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    font = ImageFont.truetype(args.font, 18)
    frames = []
    transcript = {}
    for resource in ('webhooks', 'skills'):
        before = capture(args.before.resolve(), resource, 'before')[0]
        printed = capture(args.after.resolve(), resource, 'print')[0]
        preview, advanced = capture(args.after.resolve(), resource, 'browse')
        transcript[resource] = {'before': before, 'printed': printed, 'preview': preview, 'next': advanced}
        group = [render(text, resource, label, font) for text, label in
                 [(before, 'Before'), (printed, 'After'), (preview, 'After · --browse'), (advanced, 'After · Space')]]
        if resource == 'webhooks':
            group[0].save(args.output / 'before.png')
            group[1].save(args.output / 'after.png')
        frames.extend(group)
    frames[0].save(args.output / 'pager.gif', save_all=True, append_images=frames[1:],
                   duration=[2200, 2600, 2500, 2200] * 2, loop=0, optimize=True)
    (args.output / 'screens.json').write_text(json.dumps(transcript, indent=2) + '\n')


if __name__ == '__main__':
    main()
