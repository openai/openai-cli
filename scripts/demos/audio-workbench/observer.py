"""Verified combining-aware viewport and scrollback observation for fixed-size cases."""

import hashlib
import json
import os
from pathlib import Path
import sys

MANIFEST_SHA256 = "9d048014421425452323fff126d2b460d33357aef64692d07f9e142b6b9888ad"
COMMITS = {
    "pyte": "b106bb7bd23ab31ec7709c5e6788117dcc07203c",
    "wcwidth": "36a625179ed2675287fe6b61c2ad319406449e60",
}
UNICODE_VERSION = "15.1.0"
HISTORY_ROWS = 100000


def default_source_directory():
    cache = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache"))
    return cache / "openai-cli" / "audio-observer" / MANIFEST_SHA256


def load_manifest():
    data = Path(__file__).with_name("observer_sources.json").read_bytes()
    if hashlib.sha256(data).hexdigest() != MANIFEST_SHA256:
        raise ValueError("Navigation observer source manifest changed")
    manifest = json.loads(data)
    if {name: item["commit"] for name, item in manifest["packages"].items()} != COMMITS:
        raise ValueError("Navigation observer package commits changed")
    return manifest


def verify_sources(package_directory=None):
    directory = Path(package_directory or default_source_directory()).resolve()
    manifest = load_manifest()
    expected = set()
    for package in manifest["packages"].values():
        for entry in package["files"].values():
            path = directory / entry["stored_path"]
            if not path.resolve().is_relative_to(directory) or path.is_symlink():
                raise ValueError("Navigation observer source escaped its installation directory")
            try:
                with path.open("rb") as source:
                    data = source.read(entry["bytes"] + 1)
            except FileNotFoundError as error:
                raise ValueError("Observer sources are missing. Run scripts/demos/audio-workbench/setup_observer.py first.") from error
            if len(data) != entry["bytes"] or hashlib.sha256(data).hexdigest() != entry["sha256"]:
                raise ValueError("Pinned navigation observer source changed")
            if entry["stored_path"].startswith("sources/"):
                expected.add(entry["stored_path"])
    sources = directory / "sources"
    actual = {str(path.relative_to(directory)) for path in sources.rglob("*.py")}
    if actual != expected:
        raise ValueError("Navigation observer import directory contains unreviewed Python files")
    return sources


def _verified_packages(package_directory=None):
    sources = verify_sources(package_directory)
    os.environ["UNICODE_VERSION"] = UNICODE_VERSION
    sys.dont_write_bytecode = True
    for name, module in list(sys.modules.items()):
        if any(name == package or name.startswith(package + ".") for package in COMMITS):
            path = getattr(module, "__file__", None)
            if path is None or not Path(path).resolve().is_relative_to(sources):
                raise ValueError("An unpinned observer package was already imported")
    if str(sources) not in sys.path:
        sys.path.insert(0, str(sources))
    import pyte
    import wcwidth
    for module, name in ((pyte, "pyte"), (wcwidth, "wcwidth")):
        if Path(module.__file__).resolve() != sources / name / "__init__.py":
            raise ValueError("Navigation observer imported an unpinned package")
    return pyte


class NavigationObserver:
    def __init__(self, columns, rows, package_directory=None):
        if (columns, rows) not in ((90, 34), (40, 12)):
            raise ValueError("Navigation observer supports only fixed 90x34 or 40x12 cases")
        pyte = _verified_packages(package_directory)
        self.columns, self.rows = columns, rows
        self._screen = pyte.Screen(columns, rows)
        self._history = pyte.HistoryScreen(columns, rows, history=HISTORY_ROWS)
        self._stream = pyte.ByteStream(self._screen)
        self._history_stream = pyte.ByteStream(self._history)
        self._bytes = 0

    def _check_history(self):
        if len(self._history.history.top) >= HISTORY_ROWS or len(self._history.history.bottom) >= HISTORY_ROWS:
            raise AssertionError("Observer scrollback reached its retention bound; cleanup coverage is incomplete")

    def feed(self, data):
        if not isinstance(data, bytes):
            raise TypeError("Navigation observer requires unchanged byte chunks")
        self._stream.feed(data)
        self._history_stream.feed(data)
        self._bytes += len(data)
        self._check_history()

    def resize(self, *_dimensions):
        raise ValueError("Navigation observer does not support resize; use the existing resize workflow")

    def viewport_text(self):
        if self._history.display != self._screen.display:
            raise AssertionError("Navigation viewport observers disagree")
        return "\n".join(self._screen.display)

    def scrollback_lines(self):
        self._check_history()
        def render(queue):
            return ["".join(line.get(column, self._history.default_char).data for column in range(self.columns))
                    for line in queue]
        return {"top": render(self._history.history.top), "bottom": render(self._history.history.bottom)}

    def provenance(self):
        return {
            "adapter_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            "source_manifest_sha256": MANIFEST_SHA256,
            "package_commits": dict(COMMITS),
            "unicode_version": UNICODE_VERSION,
            "columns": self.columns,
            "rows": self.rows,
            "bytes_observed": self._bytes,
            "resize_supported": False,
            "history_limit_rows": HISTORY_ROWS,
            "limitations": ["Offline observer; no native-terminal acceptance claim",
                            "pyte has a combining-at-right-margin limitation",
                            "Reaching the scrollback bound fails; it cannot produce a cleanup pass"],
        }


def create_observer(columns, rows, package_directory=None):
    return NavigationObserver(columns, rows, package_directory)
