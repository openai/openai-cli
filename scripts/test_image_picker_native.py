"""Check native picker transcript validation without processes or terminals."""
import base64
import binascii
from collections import deque
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
import unittest.mock

spec = importlib.util.spec_from_file_location(
    'check_image_picker_native', Path(__file__).with_name('check-image-picker-native.py'))
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)
kitty_images = native.compatibility.kitty_images


def frame(header, chunk=b''):
    return b'\x1b_G' + header + b';' + chunk + b'\x1b\\'


def image(data=b'abcdef'):
    return frame(b'a=T,f=100,q=2,m=0', base64.b64encode(data))


DELETE = frame(b'a=d,d=C,q=2')
FIRST = frame(b'a=T,f=100,q=2,m=1', b'YWJj')
LAST = frame(b'q=2,m=0', b'ZGVm')


class KittyTranscriptTest(unittest.TestCase):
    def test_complete_single_and_chunked_transmissions(self):
        for raw in (image(), FIRST + LAST):
            with self.subTest(raw=raw):
                self.assertEqual(kitty_images(raw), [b'abcdef'])

    def test_default_parser_rejects_cursor_deletes(self):
        with self.assertRaises(AssertionError):
            kitty_images(image() + DELETE)

    def test_opt_in_accepts_deletes_between_complete_images(self):
        raw = DELETE + FIRST + LAST + DELETE + image(b'second') + DELETE
        self.assertEqual(kitty_images(raw, allow_cursor_deletes=True), [b'abcdef', b'second'])

    def test_cursor_delete_cannot_complete_an_interrupted_image(self):
        with self.assertRaisesRegex(AssertionError, 'interrupted'):
            kitty_images(FIRST + DELETE + LAST, allow_cursor_deletes=True)

    def test_cursor_delete_rejects_other_options(self):
        for header in (b'a=d,d=A,q=2', b'a=d,d=c,q=2', b'a=d,q=2',
                       b'a=d,d=C,q=1', b'a=d,d=C,q=2,m=0', b'a=d,d=C,q=2,i=1',
                       b'a=d,d=C,q=2,q=2'):
            with self.subTest(header=header), self.assertRaises(AssertionError):
                kitty_images(frame(header), allow_cursor_deletes=True)

    def test_cursor_delete_rejects_payload(self):
        with self.assertRaises(AssertionError):
            kitty_images(frame(b'a=d,d=C,q=2', b'YWJj'), allow_cursor_deletes=True)

    def test_malformed_transmissions_remain_rejected(self):
        bad = [frame(b'a=T,f=100,q=1,m=0', b'YWJj'),
               frame(b'a=T,f=100,q=2,m=2', b'YWJj'),
               frame(b'a=T,f=100,q=2', b'YWJj'),
               frame(b'a=T,f=24,q=2,m=0', b'YWJj'),
               frame(b'a=t,f=100,q=2,m=0', b'YWJj'),
               frame(b'a=T,f=100,q=2,q=2,m=0', b'YWJj'),
               frame(b'a=T,f=100,q=2,m=0'),
               frame(b'a=T,f=100,q=2,m=0', b'!!!?'),
               frame(b'a=T,f=100,q=2,m=0', b'Y'),
               frame(b'a=T,f=100,q=2,m=0', b'YWJj' * 1025),
               FIRST + frame(b'a=T,q=2,m=0', b'ZGVm')]
        for enabled in (False, True):
            for raw in bad:
                with self.subTest(enabled=enabled, raw=raw[:60]):
                    with self.assertRaises((AssertionError, binascii.Error)):
                        kitty_images(raw, allow_cursor_deletes=enabled)

    def test_incomplete_frames_and_images_remain_rejected(self):
        for enabled in (False, True):
            for raw in (FIRST, image()[:-1], image() + b'\x1b_G', DELETE[:-2]):
                with self.subTest(enabled=enabled, raw=raw), self.assertRaises(AssertionError):
                    kitty_images(raw, allow_cursor_deletes=enabled)


class TranscriptTerminal:
    def __init__(self, raw, chunks=()):
        self.raw = bytearray(raw)
        self.chunks = deque(chunks)
        self.child = SimpleNamespace(poll=lambda: None)

    def size(self):
        return 80, 24

    def read(self):
        assert self.chunks, 'transcript ended before preview completion'
        self.raw.extend(self.chunks.popleft())


class NativePreviewWaitTest(unittest.TestCase):
    def test_every_byte_split_waits_for_complete_preview_and_caption(self):
        raw = b'Image 1 synthetic.png\r\n' + image(native.fixture_png())
        for cut in range(len(raw) + 1):
            terminal = TranscriptTerminal(b'Generating image\r\n' + raw[:cut], [raw[cut:]])
            with self.subTest(cut=cut), unittest.mock.patch.object(native.time, 'monotonic', return_value=0):
                self.assertEqual(native.wait_previews(terminal, 0, 1), 1)

    def test_prior_preview_cannot_satisfy_fresh_busy_cycle(self):
        raw = image(native.fixture_png()) + b'\r\nImage 1 synthetic.png\r\n'
        terminal = TranscriptTerminal(raw + b'Generating image')
        with self.assertRaisesRegex(AssertionError, 'transcript ended'):
            native.wait_previews(terminal, len(raw), 1)

    def test_preview_without_gallery_growth_is_rejected(self):
        terminal = TranscriptTerminal(b'Generating image\r\nImage 1 first.png\r\n' + image(native.fixture_png()))
        with self.assertRaisesRegex(AssertionError, 'transcript ended'):
            native.wait_previews(terminal, 0, 2)

    def test_old_image_redraw_cannot_satisfy_new_preview(self):
        redraw = b'Image 1 first.png\r\n' + image(native.fixture_png()) + b'\r\n'
        for repeats in (1, 2):
            terminal = TranscriptTerminal(b'Generating image\r\n' + redraw * repeats + b'Image 2 second.png')
            with self.subTest(repeats=repeats), self.assertRaisesRegex(AssertionError, 'transcript ended'):
                native.wait_previews(terminal, 0, 2)

    def test_later_old_image_redraw_cannot_fill_new_preview(self):
        redraw = b'Image 1 first.png\r\n' + image(native.fixture_png()) + b'\r\n'
        terminal = TranscriptTerminal(b'Generating image\r\n' + redraw + b'Image 2 second.png\r\n' + redraw)
        with self.assertRaisesRegex(AssertionError, 'transcript ended'):
            native.wait_previews(terminal, 0, 2)

    def test_every_byte_split_waits_for_new_gallery_preview(self):
        encoded = base64.b64encode(native.fixture_png())
        preview = frame(b'a=T,f=100,q=2,m=1', encoded[:32]) + frame(b'q=2,m=0', encoded[32:])
        raw = b'Image 1 first.png\r\n' + preview + b'\r\nImage 2 second.png\r\n' + preview
        for cut in range(len(raw) + 1):
            terminal = TranscriptTerminal(b'Generating image\r\n' + raw[:cut], [raw[cut:]])
            with self.subTest(cut=cut), unittest.mock.patch.object(native.time, 'monotonic', return_value=0):
                self.assertEqual(native.wait_previews(terminal, 0, 2), 2)
                self.assertEqual(bytes(terminal.raw), b'Generating image\r\n' + raw)

    def test_corrupt_preview_is_rejected(self):
        terminal = TranscriptTerminal(b'Generating image\r\nImage 1 first.png\r\n' + image(b'not a PNG'))
        with self.assertRaisesRegex(RuntimeError, 'not PNG'):
            native.wait_previews(terminal, 0, 1)


if __name__ == '__main__':
    unittest.main()
