#!/usr/bin/env python3
"""Install pinned development observer sources without executing packaging hooks."""

import argparse
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import time
import urllib.request


def observer_module():
    spec = importlib.util.spec_from_file_location("audio_observer", Path(__file__).with_name("observer.py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def install(output, source_directory=None):
    observer = observer_module()
    manifest = observer.load_manifest()
    output = Path(output).absolute()
    if output.exists() or output.is_symlink():
        raise ValueError("Output already exists. Verify or select a fresh observer directory.")
    output.parent.mkdir(parents=True, exist_ok=True)
    deadline = time.monotonic() + 120
    with tempfile.TemporaryDirectory(prefix=".audio-observer-", dir=output.parent) as temporary:
        staging = Path(temporary) / "install"
        staging.mkdir()
        for package in manifest["packages"].values():
            for entry in package["files"].values():
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError("Observer setup exceeded its 120-second deadline")
                if source_directory is not None:
                    with (Path(source_directory) / entry["stored_path"]).open("rb") as source:
                        data = source.read(entry["bytes"] + 1)
                else:
                    with urllib.request.urlopen(entry["url"], timeout=min(10, remaining)) as response:
                        data = response.read(entry["bytes"] + 1)
                if len(data) != entry["bytes"] or hashlib.sha256(data).hexdigest() != entry["sha256"]:
                    raise ValueError("Downloaded observer source did not match its immutable pin")
                target = staging / entry["stored_path"]
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(data)
        observer.verify_sources(staging)
        # Publish only after every source and license has passed verification.
        staging.rename(output)
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=observer_module().default_source_directory())
    parser.add_argument("--source-directory", type=Path,
                        help="Copy an existing hash-verified source bundle without network access")
    args = parser.parse_args()
    try:
        destination = install(args.output, args.source_directory)
    except (OSError, ValueError) as error:
        parser.exit(1, str(error) + "\n")
    print("Verified observer sources: " + str(destination))


if __name__ == "__main__":
    main()
