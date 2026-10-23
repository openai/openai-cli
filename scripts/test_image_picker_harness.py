"""Check picker fixture contracts with transcripts and mocks, without a terminal."""
from collections import deque
from pathlib import Path
from types import SimpleNamespace
import struct
import unittest
import unittest.mock

import image_picker_harness as picker


def paint(*rows):
    """Replace the visible workspace at the saved cursor origin."""
    return b'\x1b[H\x1b[J' + '\r\n'.join(rows).encode()


def workspace(prompt='first request', busy=False):
    rows = ['Create image', *prompt.splitlines(), 'Settings', 'Command']
    if busy:
        rows += ['Generating image', 'Ctrl+C cancel generation']
    else:
        rows += ['Images', 'Saved in pictures with spaces 雪', 'Ctrl+C exit']
    return paint(*rows)


class TranscriptTerminal:
    """Feed bounded chunks through the real marker wait without process access."""
    wait = picker.Terminal.wait
    text = picker.Terminal.text

    def __init__(self, raw=b'', chunks=(), dimensions=(96, 28)):
        self.raw = bytearray(raw)
        self.chunks = deque(chunks)
        self.dimensions = dimensions
        self.child = SimpleNamespace(returncode=None)

    def read(self):
        if not self.chunks:
            raise AssertionError('synthetic transcript ended before the requested marker')
        self.raw.extend(self.chunks.popleft())

    def running(self):
        return True

    def size(self):
        return self.dimensions


class WorkspaceLifecycleTest(unittest.TestCase):
    def test_fresh_request_accepts_completed_retained_form(self):
        previous = workspace()
        terminal = TranscriptTerminal(previous, (
            workspace(busy=True), workspace(),
        ))
        picker.wait_resumed(terminal, len(previous), 'first request')

    def test_stale_idle_screen_cannot_satisfy_a_new_request(self):
        previous = workspace()
        terminal = TranscriptTerminal(previous)
        with self.assertRaises(AssertionError):
            picker.wait_resumed(terminal, len(previous), 'first request')

    def test_previous_busy_marker_cannot_satisfy_a_new_request(self):
        previous = workspace(busy=True) + workspace()
        terminal = TranscriptTerminal(previous, (workspace(),))
        with self.assertRaises(AssertionError):
            picker.wait_resumed(terminal, len(previous), 'first request')

    def test_busy_request_without_completion_is_rejected(self):
        previous = workspace()
        terminal = TranscriptTerminal(previous, (workspace(busy=True),))
        with self.assertRaises(AssertionError):
            picker.wait_resumed(terminal, len(previous), 'first request')

    def test_old_completion_markers_cannot_hide_current_busy_screen(self):
        raw = workspace(busy=True) + workspace() + workspace(busy=True)
        terminal = TranscriptTerminal(raw)
        with self.assertRaises(AssertionError):
            picker.wait_resumed(terminal, 0, 'first request')

    def test_receipt_before_workspace_closure_is_rejected(self):
        premature = b'\r\nSaved image: "/tmp/image.png"\r\n'
        terminal = TranscriptTerminal(workspace(busy=True) + premature + workspace())
        with self.assertRaisesRegex(AssertionError, 'receipt escaped'):
            picker.wait_resumed(terminal, 0, 'first request')

    def test_previous_closed_workspace_receipt_does_not_taint_new_request(self):
        previous = b'Saved image: "/tmp/previous.png"\r\n'
        terminal = TranscriptTerminal(previous, (
            workspace(busy=True), workspace(),
        ))
        picker.wait_resumed(terminal, len(previous), 'first request')

    def test_completed_request_requires_each_visible_form_section(self):
        for marker in (b'Create image', b'Settings', b'Command', b'Ctrl+C exit'):
            with self.subTest(marker=marker):
                complete = workspace().replace(marker, b' ' * len(marker))
                terminal = TranscriptTerminal(workspace(busy=True) + complete)
                with self.assertRaises(AssertionError):
                    picker.wait_resumed(terminal, 0, 'first request')

    def test_multiline_unicode_prompt_survives_incremental_reads(self):
        prompt = 'caf\u00e9 雪\nsecond line: "quoted" \\ path'
        complete = workspace(prompt)
        split = complete.index('雪'.encode()) + 1
        terminal = TranscriptTerminal(chunks=(
            workspace(prompt, busy=True), complete[:split], complete[split:],
        ))
        picker.wait_resumed(terminal, 0, prompt)

    def test_old_prompt_text_cannot_replace_a_missing_retained_line(self):
        prompt = 'first request\nretained second line'
        complete = workspace(prompt).replace(b'retained second line', b'replaced second line')
        terminal = TranscriptTerminal(workspace(prompt, busy=True) + complete)
        with self.assertRaises(AssertionError):
            picker.wait_resumed(terminal, 0, prompt)

    def test_receiver_uses_live_dimensions_after_a_resize(self):
        terminal = TranscriptTerminal(workspace(busy=True) + workspace())
        terminal.initial_size = (140, 28)
        terminal.dimensions = (80, 24)
        with unittest.mock.patch.object(picker, 'screen', wraps=picker.screen) as receiver:
            picker.wait_resumed(terminal, 0, 'first request')
        self.assertEqual(receiver.call_args_list[0].args[1:], (80, 24))


class ReceiptTest(unittest.TestCase):
    def test_receipts_accept_each_exact_path_once_in_any_order(self):
        terminal = TranscriptTerminal(
            b'openai images generate --prompt synthetic\r\n'
            b'Saved image: "/tmp/second.png"\r\n'
            b'Saved image: "/tmp/first.png"\r\n'
        )
        picker.check_receipts(terminal, iter((Path('/tmp/first.png'), Path('/tmp/second.png'))))

    def test_receipts_after_cursor_restoration_accept_terminal_line_boundaries(self):
        # The real close path retains DEC cursor controls before a bare CR.
        prefix = (b'completed workspace\x1b[m\x1b[140G \r\x1b[5A'
                  b'\x1b7\x1b8\x1b[6n\x1b8')
        teardown = b'\x1b[J\x1b[0 q\x1b[?25h'
        receipt = b'Saved image: "/tmp/pictures with spaces \\u96ea/image.png"\n'
        for boundary in (b'\r', b'\r\n', b'\n'):
            with self.subTest(boundary=boundary):
                terminal = TranscriptTerminal(
                    prefix + boundary + teardown + receipt
                    + b'\x1b[?2004lopenai images generate --prompt synthetic\r\n'
                )
                picker.check_receipts(terminal, [Path('/tmp/pictures with spaces 雪/image.png')])

    def test_receipts_follow_go_ascii_quoting(self):
        # image_picker_workspace.go prints %+q. fmt delegates to QuoteToASCII.
        # strconv uses \U for astral runes, \a/\v, and \x for other ASCII controls.
        cases = (
            ('/tmp/pictures with spaces 雪.png', r'"/tmp/pictures with spaces \u96ea.png"'),
            ('/tmp/horse 🐎.png', r'"/tmp/horse \U0001f40e.png"'),
            ('/tmp/bell\a-and-vertical\v.png', r'"/tmp/bell\a-and-vertical\v.png"'),
            ('/tmp/escape\x1b-and-delete\x7f.png', r'"/tmp/escape\x1b-and-delete\x7f.png"'),
            ('/tmp/tab\t-and-newline\n.png', r'"/tmp/tab\t-and-newline\n.png"'),
            ('/tmp/carriage\rreturn.png', r'"/tmp/carriage\rreturn.png"'),
            ('/tmp/quote"-and-backslash\\.png', r'"/tmp/quote\"-and-backslash\\.png"'),
        )
        for path, literal in cases:
            with self.subTest(path=path):
                terminal = TranscriptTerminal(('Saved image: ' + literal + '\r\n').encode('ascii'))
                picker.check_receipts(terminal, [Path(path)])

    def test_missing_or_duplicate_receipts_are_rejected(self):
        first = b'Saved image: "/tmp/first.png"\r\n'
        second = b'Saved image: "/tmp/second.png"\r\n'
        for raw in (b'', first, first + first, first + second + second):
            with self.subTest(raw=raw), self.assertRaises(AssertionError):
                picker.check_receipts(TranscriptTerminal(raw), ['/tmp/first.png', '/tmp/second.png'])

    def test_same_count_with_wrong_paths_is_rejected(self):
        for literal in (
            '"/tmp/image.png.bak"', '"/tmp/other/image.png"',
            '"image.png"', '"/tmp/IMAGE.png"',
        ):
            with self.subTest(literal=literal), self.assertRaises(AssertionError):
                raw = ('Saved image: ' + literal + '\n').encode()
                picker.check_receipts(TranscriptTerminal(raw), ['/tmp/image.png'])

    def test_malformed_receipts_cannot_count_as_saved_paths(self):
        for line in (
            'Saved image: /tmp/image.png',
            'Saved image: "/tmp/image.png',
            'Saved image: "/tmp/image.png" unexpected suffix',
        ):
            with self.subTest(line=line), self.assertRaises(AssertionError):
                picker.check_receipts(TranscriptTerminal((line + '\n').encode()), ['/tmp/image.png'])

    def test_no_saved_files_requires_no_receipts(self):
        picker.check_receipts(TranscriptTerminal(b'cancelled\r\n'), [])
        with self.assertRaises(AssertionError):
            picker.check_receipts(TranscriptTerminal(b'Saved image: "/tmp/image.png"\n'), [])


class TerminalReceiverTest(unittest.TestCase):
    def test_size_queries_current_dimensions_without_cached_geometry(self):
        terminal = picker.Terminal.__new__(picker.Terminal)
        terminal.master = 73
        terminal.initial_size = (140, 28)
        dimensions = (struct.pack('HHHH', 24, 80, 0, 0),
                      struct.pack('HHHH', 40, 120, 0, 0))
        with unittest.mock.patch.object(picker.fcntl, 'ioctl', side_effect=dimensions) as ioctl:
            self.assertEqual(terminal.size(), (80, 24))
            self.assertEqual(terminal.size(), (120, 40))
        self.assertEqual(ioctl.call_args_list, [
            unittest.mock.call(73, picker.termios.TIOCGWINSZ, bytes(8)),
            unittest.mock.call(73, picker.termios.TIOCGWINSZ, bytes(8)),
        ])

    def test_wrap_and_scroll_keep_current_rows_and_history(self):
        history = []
        result = picker.screen(b'abcdef\r\n1234\r\nlast', width=4, height=2, history=history)
        self.assertEqual(result, b'1234\nlast')
        self.assertEqual(history, ['abcd', 'ef'])

    def test_saved_cursor_replaces_busy_line_without_stale_tail(self):
        raw = (b'Create image\r\n\x1b7Generating image\r\nCtrl+C cancel generation'
               b'\x1b8\x1b[JImages\r\nSaved in pictures\r\nCtrl+C exit')
        self.assertEqual(picker.screen(raw, 40, 5),
                         b'Create image\nImages\nSaved in pictures\nCtrl+C exit\n')

    def test_unicode_cells_and_multiline_text_preserve_receiver_behavior(self):
        # The receiver models cells, not grapheme clusters. Keep its existing behavior.
        raw = 'caf\u00e9 雪\r\ne\u0301x'.encode()
        self.assertEqual(picker.screen(raw, 12, 2), 'caf\u00e9 雪\nex'.encode())

    def test_osc_and_apc_payloads_do_not_become_screen_text(self):
        for hidden in (
            b'\x1b]0;Generating image Saved in title\x07',
            b'\x1b]0;Generating image Saved in title\x1b\\',
            b'\x1b_Ga=T;Generating image\r\nSaved in payload\x1b\\',
        ):
            with self.subTest(hidden=hidden):
                raw = b'Create image' + hidden + b'\r\nCtrl+C exit'
                self.assertEqual(picker.screen(raw, 24, 2), b'Create image\nCtrl+C exit')


if __name__ == '__main__':
    unittest.main()
