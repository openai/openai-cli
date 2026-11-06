"""Exercise cross-link consumption with a UTF-8 GitHub event under a legacy locale."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import zipfile

SCRIPT = Path(__file__).with_name("sdk_cross_link.py")
spec = importlib.util.spec_from_file_location("sdk_cross_link", SCRIPT)
cross_link = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cross_link)


class EventEncodingTest(unittest.TestCase):
    def test_consumer_reads_utf8_event_with_utf8_mode_disabled(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repository = {"id": cross_link.REPOSITORY_ID,
                          "full_name": cross_link.REPOSITORY, "private": True}
            event = {"number": 1, "repository": repository, "pull_request": {
                "head": {"repo": repository, "sha": "a" * 40, "ref": "fixture"},
                "base": {"repo": repository, "ref": "main"},
                "updated_at": "2026-01-01T00:00:00Z",
                "body": "Preserve \u201cUnicode\u201d in the PR description.",
            }}
            event_path = root / "event.json"
            event_path.write_text(json.dumps(event, ensure_ascii=False), encoding="utf-8")
            manifest = {"schema": 1, "context": cross_link.identity(event),
                        "run_id": 1, "go_repository": cross_link.GO_REPOSITORY,
                        "mode": "released"}
            inputs = root / "ci-inputs"
            inputs.mkdir()
            with zipfile.ZipFile(inputs / "ci-sdk.zip", "w") as artifact:
                artifact.writestr("manifest.json", json.dumps(manifest))
            env = dict(os.environ, LC_ALL="C", PYTHONCOERCECLOCALE="0",
                       GITHUB_EVENT_NAME="pull_request", GITHUB_EVENT_PATH=str(event_path),
                       RUNNER_TEMP=str(root), GITHUB_STEP_SUMMARY=str(root / "summary.md"))
            result = subprocess.run(
                [sys.executable, "-X", "utf8=0", str(SCRIPT), "consume"],
                env=env, capture_output=True, text=True, encoding="utf-8", check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("using the committed released dependency", result.stdout)


if __name__ == "__main__":
    unittest.main()
