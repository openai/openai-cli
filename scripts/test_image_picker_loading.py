"""Check quoted prompts in synthetic loading transcripts."""

from types import SimpleNamespace
import unittest

from demos import image_friendly_loading_check as loading


def check_prompt(quoted):
    output = b"/ Generating image " + quoted + b"\r\n"
    output += b"- Generating image " + quoted + b"\r\n"
    output += b"0s elapsed\r\n2s elapsed\r\n"
    process = SimpleNamespace(output=output, pump=lambda duration: None)
    loading.loading_check(process, 0, False, 40, prompt="synthetic")


class LoadingPromptTest(unittest.TestCase):
    def test_accepts_quoted_prompts(self):
        for quoted in (b"''", b"'robot'", rb"'O\'neil'", rb"'C:\\images\\'",
                       rb"'line\nnext\t'", "'雪 🦉'".encode()):
            with self.subTest(quoted=quoted):
                check_prompt(quoted)

    def test_rejects_incomplete_quotes(self):
        for quoted in (b"'robot", b"'robot\\", rb"'robot\'", b"'line\nnext'", b"'line\rnext'"):
            with self.subTest(quoted=quoted):
                with self.assertRaisesRegex(AssertionError, "prompt quotation is missing or broken"):
                    check_prompt(quoted)


if __name__ == "__main__":
    unittest.main()
