"""Observer integrity and evidence-boundary checks without third-party imports."""

import hashlib
import importlib.util
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("audio_observer", Path(__file__).with_name("observer.py"))
observer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(observer)


class ObserverTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.source = self.directory / "sources/pyte/__init__.py"
        self.source.parent.mkdir(parents=True)
        self.source.write_bytes(b"# Synthetic source; never imported\n")
        data = self.source.read_bytes()
        self.manifest = {"packages": {"pyte": {"files": {"__init__.py": {
            "stored_path": "sources/pyte/__init__.py", "bytes": len(data),
            "sha256": hashlib.sha256(data).hexdigest(),
        }}}}}

    def test_repository_manifest_retains_both_immutable_commits_and_licenses(self):
        manifest = observer.load_manifest()
        self.assertEqual({name: entry["commit"] for name, entry in manifest["packages"].items()}, observer.COMMITS)
        for entry in manifest["packages"].values():
            self.assertIn("LICENSE", entry["files"])
            for source in entry["files"].values():
                self.assertIn("/" + entry["commit"] + "/", source["url"])

    def test_changed_dependency_is_rejected_before_import(self):
        self.source.write_bytes(b"raise RuntimeError('must not run')\n")
        with patch.object(observer, "load_manifest", return_value=self.manifest):
            with self.assertRaisesRegex(ValueError, "Pinned navigation observer source changed"):
                observer.verify_sources(self.directory)

    def test_extra_importable_source_is_rejected(self):
        self.source.with_name("unreviewed.py").write_text("raise RuntimeError('must not run')\n")
        with patch.object(observer, "load_manifest", return_value=self.manifest):
            with self.assertRaisesRegex(ValueError, "unreviewed Python files"):
                observer.verify_sources(self.directory)

    def test_preloaded_unpinned_submodule_is_rejected(self):
        rogue = SimpleNamespace(__file__="/untrusted/pyte/screens.py")
        with patch.object(observer, "verify_sources", return_value=self.directory / "sources"):
            with patch.dict(sys.modules, {"pyte.screens": rogue}):
                with self.assertRaisesRegex(ValueError, "unpinned observer package"):
                    observer._verified_packages(self.directory)

    def test_scrollback_saturation_cannot_pass_cleanup(self):
        screen = observer.NavigationObserver.__new__(observer.NavigationObserver)
        screen._history = SimpleNamespace(history=SimpleNamespace(top=[None] * observer.HISTORY_ROWS, bottom=[]))
        with self.assertRaisesRegex(AssertionError, "retention bound"):
            screen.scrollback_lines()


if __name__ == "__main__":
    unittest.main()
