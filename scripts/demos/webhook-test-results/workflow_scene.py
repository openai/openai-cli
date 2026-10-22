#!/usr/bin/env python3
"""Record the real form, endpoint browser, and explicit confirmation."""
import codecs
import hashlib
import importlib.util
import json
import os
import pathlib
import shutil
import signal
import termios
import time

spec = importlib.util.spec_from_file_location('guided', pathlib.Path(__file__).with_name('guided_scene.py'))
guided = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guided)


class Terminal(guided.RelayTerminal):
    def read(self, duration=.05):
        before = len(self.raw)
        super().read(duration)
        if len(self.raw) > before:
            if not hasattr(self, 'decoder'):
                self.decoder = codecs.getincrementaldecoder('utf-8')()
            self.events[-1][2] = self.decoder.decode(bytes(self.raw[before:]))


def main():
    if not all(os.isatty(fd) for fd in (0, 1, 2)):
        raise RuntimeError('workflow demo requires terminal input and output')
    for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(sig, guided.interrupted)
    binary = shutil.which('openai')
    output = pathlib.Path(os.environ['DEMO_EVIDENCE_BASE'])
    evidence = {'width': 80, 'height': 24, 'checkpoints': {}, 'screens': {},
                'binary_sha256': hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest()}
    original = termios.tcgetattr(1)
    relay = termios.tcgetattr(1)
    relay[1] &= ~termios.OPOST
    print('$ openai webhooks create', flush=True)
    terminal = None
    try:
        termios.tcsetattr(1, termios.TCSANOW, relay)
        env = {key: os.environ[key] for key in ('PATH', 'HOME', 'OPENAI_BASE_URL')}
        env.update(TERM='xterm-256color', LANG='en_US.UTF-8', COLORTERM='truecolor', COLORFGBG='15;0',
                   FORCE_COLOR='1', GOMAXPROCS='2', OPENAI_API_KEY='synthetic-demo-key', XDG_CONFIG_HOME=env['HOME'])
        terminal = Terminal(binary, ['webhooks', 'create'], env, width=80, height=24)

        def checkpoint(name, marker):
            screen = guided.wait_screen(terminal, marker)
            guided.pause(terminal, 1.4)
            evidence['screens'][name] = screen
            evidence['checkpoints'][name] = time.monotonic() - terminal.started
            return screen

        def send(keys, marker):
            terminal.send(keys)
            return guided.wait_screen(terminal, marker)

        checkpoint('form', '› Name')
        guided.paste(terminal, 'Response notifications')
        send(b'\x1b[B', '› URL')
        guided.paste(terminal, 'https://example.com/webhook')
        send(b'\x1b[B', '› Events')
        send(b'\x1b[C' * 3, '‹ Background responses ›')
        checkpoint('category', '‹ Background responses ›')
        send(b'\x1b[B', '› Search')
        guided.paste(terminal, 'failed')
        checkpoint('search', 'response.failed')
        send(b'\x1b[B', '› [ ] response.failed')
        send(b' ', '[x] response.failed')
        checkpoint('selection', '[x] response.failed')
        send(b'\x15', '› Search')
        send(b'\x1b[B', 'Space select')
        send(b'\x01', '[x] response.completed')
        checkpoint('events', '[x] response.completed')
        send(b'\t', '› [ Continue to confirmation ]')
        send(b'\x1b[C', '› [ Existing webhooks ]')
        send(b'\r', 'whe_demo_responses')
        checkpoint('endpoints', 'whe_demo_responses')
        send(b'\r', 'https://example.com/responses')
        checkpoint('endpoint-details', 'https://example.com/responses')
        send(b'\x1b', 'whe_demo_responses')
        send(b'\x1b', '› [ Existing webhooks ]')
        screen = checkpoint('draft-return', '› [ Existing webhooks ]')
        assert all(value in screen for value in ('Response notifications', 'https://example.com/webhook',
                                                '[x] response.completed', '[x] response.failed'))
        send(b'\x1b[D', '› [ Continue to confirmation ]')
        send(b'\r', '[No]   Yes, create')
        checkpoint('confirmation', '[No]   Yes, create')
        send(b'y', '[Yes, create]')
        checkpoint('confirm-yes', '[Yes, create]')
        pathlib.Path(os.environ['DEMO_CONFIRMATION_FILE']).write_text('explicit Yes selected before Enter\n')
        terminal.send(b'\r')
        terminal.finish(0, timeout=12)
        evidence['result_screen'] = guided.wait_screen(terminal, 'whsec_fake_for_demo_only')
        assert 'Create endpoint now?' not in evidence['result_screen']
        evidence['exit_status'] = terminal.child.returncode
        raw = bytes(terminal.raw)
        assert ''.join(e[2] for e in terminal.events if e[1] == 'o').encode() == raw
        evidence['relayed_sha256'] = hashlib.sha256(raw).hexdigest()
        output.with_suffix('.tty').write_bytes(raw)
        header = {'version': 2, 'width': 80, 'height': 24, 'title': 'Webhook workflow; synthetic data'}
        output.with_suffix('.child.cast').write_text('\n'.join(json.dumps(e) for e in [header, *terminal.events]) + '\n')
    finally:
        try:
            if terminal is not None:
                terminal.close()
        finally:
            termios.tcsetattr(1, termios.TCSANOW, original)
    output.with_suffix('.json').write_text(json.dumps(evidence, indent=2) + '\n')
    pathlib.Path(os.environ['DEMO_STATUS_FILE']).write_text('0\n')
    print('\nTerminal replay | synthetic data\n$ ', end='', flush=True)
    time.sleep(2)


if __name__ == '__main__':
    main()
