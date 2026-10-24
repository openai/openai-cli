"""Check VSCode picker row validation without processes or terminals."""
import base64
import binascii
from collections import deque
import importlib.util
from pathlib import Path
import struct
from types import SimpleNamespace
import unittest
import unittest.mock
import zlib

spec = importlib.util.spec_from_file_location(
    'check_image_vscode', Path(__file__).with_name('check-image-vscode.py'))
vscode = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vscode)
NORMAL, PIXELS = vscode.fixture_png(4, 3)
FIXTURE = (4, 3, PIXELS)


def png(pixels, width=4, height=1):
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', binascii.crc32(kind + data) & 0xffffffff)
    rows = b''.join(b'\0' + pixels[y * width * 4:(y + 1) * width * 4] for y in range(height))
    return (b'\x89PNG\r\n\x1a\n'
            + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 6, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b''))


def frame(image, width=b'21', height=b'1', extra=b'', ending=b'\x07'):
    header = b'size=' + str(len(image)).encode() + b';width=' + width + b';height=' + height + b';inline=1'
    return b'\x1b]1337;File=' + header + extra + b':' + base64.b64encode(image) + ending


def tile(index):
    return frame(png(PIXELS[index * 16:(index + 1) * 16]))


ROWS = b''.join(tile(index) + b'\r\n' for index in [0, 0, 0, 1, 1, 1, 2, 2])


class PickerRowsTest(unittest.TestCase):
    def test_complete_rows_and_partial_redraws(self):
        self.assertEqual(vscode.check_picker_rows(ROWS + tile(2), FIXTURE), [0, 0, 0, 1, 1, 1, 2, 2, 2])

    def test_direct_frames_preserve_auto_size_and_exact_count(self):
        direct = frame(NORMAL, b'auto', b'auto')
        vscode.check_frames(direct, 1, FIXTURE)
        for raw, count in [(direct, 2), (direct * 2, 1), (tile(0), 1), (direct + b'\x1b[6n', 1)]:
            with self.subTest(raw=raw[:60], count=count), self.assertRaises(AssertionError):
                vscode.check_frames(raw, count, FIXTURE)

    def test_picker_rejects_whole_image_and_changed_geometry(self):
        bad = [frame(NORMAL), frame(NORMAL, b'auto', b'auto'),
               frame(png(PIXELS[:16]), b'20'), frame(png(PIXELS[:16]), height=b'2'),
               frame(png(PIXELS[:12], width=3))]
        for raw in bad:
            with self.subTest(raw=raw[:80]), self.assertRaises(AssertionError):
                vscode.check_picker_rows(raw, FIXTURE)

    def test_picker_rejects_changed_pixels_and_alpha(self):
        for index in (0, 3):
            pixels = bytearray(PIXELS[:16]); pixels[index] ^= 1
            with self.subTest(index=index), self.assertRaisesRegex(AssertionError, 'pixels or alpha'):
                vscode.check_picker_rows(frame(png(bytes(pixels))), FIXTURE)

    def test_picker_rejects_bad_headers_and_payloads(self):
        valid = tile(0)
        bad = [valid.replace(b';width=21', b';width'),
               frame(png(PIXELS[:16]), extra=b';width=21'),
               valid.replace(b'size=', b'size=0'),
               valid.replace(b':', b':!', 1), frame(b'not a PNG')]
        for raw in bad:
            with self.subTest(raw=raw[:80]), self.assertRaises((AssertionError, binascii.Error)):
                vscode.check_picker_rows(raw, FIXTURE)

    def test_picker_rejects_wrong_protocol_and_clear_commands(self):
        for raw in [ROWS + b'\x1b_Ga=d,d=C,q=2;\x1b\\', ROWS + b'\x1b]1337;Clear\x07']:
            with self.subTest(raw=raw[-30:]), self.assertRaisesRegex(AssertionError, 'graphics command'):
                vscode.check_picker_rows(raw, FIXTURE)

    def test_picker_rejects_incomplete_frame(self):
        for raw in [tile(0)[:-1], ROWS + b'\x1b]1337;File=']:
            with self.subTest(raw=raw[-30:]), self.assertRaisesRegex(AssertionError, 'incomplete'):
                vscode.check_picker_rows(raw, FIXTURE)

    def test_picker_accepts_st_terminator(self):
        self.assertEqual(vscode.check_picker_rows(frame(png(PIXELS[:16]), ending=b'\x1b\\'), FIXTURE), [0])


class TranscriptTerminal:
    def __init__(self, raw, chunks=()):
        self.raw = bytearray(raw)
        self.chunks = deque(chunks)
        self.child = SimpleNamespace(poll=lambda: None)

    def size(self):
        return 120, 32

    def read(self):
        assert self.chunks, 'transcript ended before row completion'
        self.raw.extend(self.chunks.popleft())


class PickerRowsWaitTest(unittest.TestCase):
    def test_every_byte_split_waits_for_all_rows(self):
        raw = b'Image 1 synthetic.png\r\n' + ROWS
        for cut in range(len(raw) + 1):
            terminal = TranscriptTerminal(b'Generating image\r\n' + raw[:cut], [raw[cut:]])
            with self.subTest(cut=cut), unittest.mock.patch.object(vscode.time, 'monotonic', return_value=0):
                self.assertEqual(vscode.wait_picker_rows(terminal, 0, 1, FIXTURE), 8)

    def test_prior_rows_do_not_satisfy_new_generation(self):
        prior = b'Image 1 prior.png\r\n' + ROWS
        terminal = TranscriptTerminal(prior + b'Generating image\r\nImage 2 new.png\r\n')
        with self.assertRaisesRegex(AssertionError, 'transcript ended'):
            vscode.wait_picker_rows(terminal, len(prior), 2, FIXTURE)

    def test_rows_before_new_caption_do_not_satisfy_new_image(self):
        terminal = TranscriptTerminal(b'Generating image\r\n' + ROWS + b'Image 2 new.png\r\n')
        with self.assertRaisesRegex(AssertionError, 'transcript ended'):
            vscode.wait_picker_rows(terminal, 0, 2, FIXTURE)

    def test_later_old_redraw_cannot_satisfy_new_image(self):
        for partial in (b'', ROWS[:-len(tile(2) + b'\r\n')]):
            raw = b'Generating image\r\nImage 2 new.png\r\n' + partial + b'Image 1 old.png\r\n' + ROWS
            with self.subTest(partial=bool(partial)), self.assertRaisesRegex(AssertionError, 'transcript ended'):
                vscode.wait_picker_rows(TranscriptTerminal(raw), 0, 2, FIXTURE)

    def test_complete_new_redraw_after_partial_section_is_accepted(self):
        raw = (b'Generating image\r\nImage 2 new.png\r\n' + tile(0)
               + b'Image 1 old.png\r\n' + ROWS + b'Image 2 new.png\r\n' + ROWS)
        self.assertEqual(vscode.wait_picker_rows(TranscriptTerminal(raw), 0, 2, FIXTURE), 17)

    def test_missing_last_row_remains_incomplete(self):
        raw = b'Generating image\r\nImage 1 new.png\r\n' + ROWS[:-len(tile(2) + b'\r\n')]
        with self.assertRaisesRegex(AssertionError, 'transcript ended'):
            vscode.wait_picker_rows(TranscriptTerminal(raw), 0, 1, FIXTURE)

    def test_complete_wrong_order_is_fatal(self):
        raw = b'Generating image\r\nImage 1 new.png\r\n' + b''.join(tile(i) for i in [2, 0, 0, 1, 1, 1, 2, 2])
        with self.assertRaisesRegex(AssertionError, 'row order changed'):
            vscode.wait_picker_rows(TranscriptTerminal(raw), 0, 1, FIXTURE)

    def test_complete_invalid_frame_is_fatal(self):
        raw = b'Generating image\r\nImage 1 new.png\r\n' + frame(b'not a PNG')
        with self.assertRaisesRegex(AssertionError, 'not a PNG'):
            vscode.wait_picker_rows(TranscriptTerminal(raw), 0, 1, FIXTURE)

    def test_clipped_earlier_rows_and_later_redraws_are_valid(self):
        raw = b'Generating image\r\n' + tile(2) + b'Image 2 new.png\r\n' + ROWS + tile(0)
        self.assertEqual(vscode.wait_picker_rows(TranscriptTerminal(raw), 0, 2, FIXTURE), 10)

    def test_previous_caption_does_not_replace_current_caption(self):
        raw = b'Generating image\r\nImage 1 prior.png\r\n' + ROWS
        with self.assertRaisesRegex(AssertionError, 'transcript ended'):
            vscode.wait_picker_rows(TranscriptTerminal(raw), 0, 2, FIXTURE)


if __name__ == '__main__':
    unittest.main()
