"""Regression checks for terminal image evidence parsing, without a terminal."""
import base64
import unittest

from terminal_test import kitty_images


def frame(options, payload):
    return b'\x1b_G' + options + b';' + payload + b'\x1b\\'


class KittyImagesTest(unittest.TestCase):
    def test_complete_images_keep_their_bytes_and_order(self):
        first, second = b'synthetic first PNG', b'synthetic second PNG'
        encoded = base64.b64encode(first)
        whole = frame(b'a=T,f=100,q=2,m=0', encoded)
        split = (frame(b'a=T,f=100,q=2,m=1', encoded[:8])
                 + frame(b'q=2,m=1', encoded[8:16])
                 + frame(b'q=2,m=0', encoded[16:]))
        last = frame(b'a=T,f=100,q=2,m=0', base64.b64encode(second))
        for transmission in (whole, split):
            with self.subTest(transmission=transmission):
                self.assertEqual(kitty_images(b'before' + transmission + b'between' + last), [first, second])
        self.assertEqual(kitty_images(b'no graphics'), [])

    def test_invalid_transmissions_cannot_count_as_images(self):
        first = frame(b'a=T,f=100,q=2,m=1', b'YWJj')
        for data in (
            first, first[:-1], first + b'\x1b_Gq=2,m=0;ZA==',
            frame(b'q=2,m=0', b'ZA=='),
            first + frame(b'a=T,f=100,q=2,m=0', b'ZA=='),
            first + frame(b'q=2,m=0', b''),
            first + frame(b'q=2,m=0', b'!'),
            frame(b'a=T,f=100,q=1,m=0', b'ZA=='),
            frame(b'a=T,f=100,q=2,m=2', b'ZA=='),
            frame(b'a=T,f=100,q=2,m=0,m=1', b'ZA=='),
            frame(b'a=T,f=100,q=2,m=0,broken', b'ZA=='),
            frame(b'a=T,f=24,q=2,m=0', b'ZA=='),
            frame(b'a=T,f=100,q=2,m=1', b'Y') + frame(b'q=2,m=0', b'WJj'),
            frame(b'a=T,f=100,q=2,m=0', b'YWJj' * 1025),
        ):
            with self.subTest(data=data), self.assertRaises((AssertionError, ValueError)):
                kitty_images(data)


if __name__ == '__main__':
    unittest.main()
