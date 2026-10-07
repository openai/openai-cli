#!/usr/bin/env python3
"""Build the comparison prototype without changing production sources."""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("output", type=Path)
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
source = root / "pkg/custom/image_picker_view.go"
patch = Path(__file__).with_name("two-column.patch")
with tempfile.TemporaryDirectory(prefix="image-layout-prototype-") as temporary:
    directory = Path(temporary)
    prototype = directory / "image_picker_view.go"
    prototype.write_text(source.read_text())
    subprocess.run(["patch", str(prototype), str(patch)], check=True)
    subprocess.run(["gofmt", "-w", str(prototype)], check=True)
    overlay = directory / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {str(source): str(prototype)}}))
    subprocess.run(["go", "build", "-overlay", str(overlay), "-o",
                    str(args.output.resolve()), "./cmd/openai"], cwd=root, check=True)
