#!/usr/bin/env python3
"""Compare raw PTY recipe bytes, before terminal replay applies visual wrapping."""
import hashlib
import json
from pathlib import Path
import sys


def captured_recipe(root, scene, topic, width, height):
    records = [json.loads(line) for line in (root / (scene + ".cast")).read_text().splitlines()]
    header = records[0]
    if (header.get("width"), header.get("height")) != (width, height):
        raise SystemExit("Unexpected PTY dimensions: " + scene)
    output = "".join(event[2] for event in records[1:] if event[1] == "o")
    marker = "$ openai examples " + topic + "\r\n"
    if output.count(marker) != 1 or not output.endswith("\r\n$ "):
        raise SystemExit("Missing command boundary: " + scene)
    recipe = output.split(marker, 1)[1][:-len("\r\n$ ")]
    recipe = recipe.replace("\r\n", "\n").encode()
    if any(value < 32 and value not in (9, 10) for value in recipe) or b"\x7f" in recipe:
        raise SystemExit("Unexpected terminal controls in recipe: " + scene)
    return recipe


def main():
    root = Path(sys.argv[1])
    home = Path(sys.argv[2])
    before = captured_recipe(root, "before", "files", 80, 28)
    if b"Unknown help topic." not in before:
        raise SystemExit("Baseline did not reproduce the missing command")
    recipes = {}
    for topic in ("files", "audio", "models"):
        recipe = (root / (topic + ".stdout")).read_bytes()
        scene = "after" if topic == "files" else "after-" + topic
        if captured_recipe(root, scene, topic, 80, 28) != recipe:
            raise SystemExit("PTY and pipe recipe bytes differ: " + topic)
        if not recipe.startswith(b"# POSIX shell.") or not recipe.endswith(b"\n"):
            raise SystemExit("Recipe lacks its shell marker or final newline: " + topic)
        if (root / (topic + ".stderr")).read_bytes():
            raise SystemExit("Unexpected pipe diagnostics: " + topic)
        recipes[topic] = recipe
    if captured_recipe(root, "narrow", "files", 40, 34) != recipes["files"]:
        raise SystemExit("40-column and 80-column recipe bytes differ")
    expected = {
        "files": (b'files upload "./upload sample.txt" --purpose user_data',
                  b'files get "$file_id"',
                  b'files download "$file_id" --output "./downloaded copy.txt"'),
        "audio": (b'audio transcribe --file "./speech sample.wav"',
                  b'audio translate --file "./speech sample.wav"',
                  b'--response-format text', b'--response-format srt'),
        "models": (b'--transform id --raw-output models list --max-items -1',),
    }
    for topic, fragments in expected.items():
        for fragment in fragments:
            if fragment not in recipes[topic]:
                raise SystemExit("Missing workflow operation: " + topic)
    if list(home.iterdir()):
        raise SystemExit("Examples created files in the isolated home")
    metadata = (root / "metadata.txt").read_text()
    for scene, status in (("before", 3), ("after", 0), ("after-audio", 0), ("after-models", 0), ("narrow", 0)):
        if f"{scene} exit status: {status} (expected {status})" not in metadata:
            raise SystemExit("Missing verified exit status: " + scene)
    result = {
        "result": "pass",
        "assertions": [
            "Baseline files command exits 3 with Unknown help topic.",
            "Candidate files, audio, and models commands exit 0 in 80-column PTYs.",
            "All candidate recipes match pipe output bytes after PTY CRLF normalization.",
            "Files recipe bytes remain identical at 40 and 80 columns with NO_COLOR.",
            "Candidate commands ignore invalid remote and missing mTLS configuration.",
            "Candidate pipe stderr is empty; recipes contain no terminal controls.",
            "Candidate commands create no files in their isolated working directory.",
        ],
        "recipe_sha256": {topic: hashlib.sha256(recipe).hexdigest() for topic, recipe in recipes.items()},
        "limits": [
            "Printed workflow commands are not executed by this demo.",
            "Invalid remote configuration prevents live requests; no request-counting fixture runs.",
            "Terminal replay does not establish graphical terminal or font behavior.",
            "Visual inspection remains required before publication.",
        ],
    }
    (root / "validation.json").write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
