#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
demo_width="${DEMO_WIDTH:-80}"
demo_theme="${DEMO_THEME:-dracula}"
case "$demo_width" in 40|80) ;; *) exit 2;; esac
case "$demo_theme" in dracula|github) ;; *) exit 2;; esac
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: fine-tuning successful empty results and preserved access failure'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'capture: macOS Bash PTY, synthetic loopback responses, isolated HOME, no live API'
  echo 'scope: terminal replay; native graphical terminal and alternate fonts remain unverified'
  echo "render: ${demo_width}x18, Menlo 18px, $demo_theme; NO_COLOR=1"
  demo_capture_metadata
  shasum -a 256 "$demo_source/record.sh" "$demo_source/scene.sh" "$demo_source/server.py"
} > "$demo_output/metadata.txt"
demo_window_size="${demo_width}x18"
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme "$demo_theme" --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_scene" \
    "HOME=$demo_runtime/$demo_scene-home" NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2
done
demo_stop_api
"$demo_python" -I -B - "$demo_output" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
requests = [json.loads(line) for line in (root / "requests.jsonl").read_text().splitlines()]
assert [item["status"] for item in requests] == [200, 403, 200, 403], requests
for scene in ("before", "after"):
    cast = [json.loads(line) for line in (root / (scene + ".cast")).read_text().splitlines()]
    text = "".join(item[2] for item in cast[1:] if item[1] == "o")
    assert "HTTP 403: Forbidden." in text, text
    assert "Check project access to this resource." in text, text
    assert text.count("Training eligibility was not checked.") == (1 if scene == "after" else 0), text
    assert ("No results." if scene == "before" else "No fine-tuning jobs returned.") in text, text
(root / "validation.txt").write_text("PASS: identical fixtures; successful empty result; denied request exits 1 without empty wording.\n")
PY
demo_assemble_capture 200 before after
printf 'Recorded fine-tuning comparison in %s\n' "$demo_output"
