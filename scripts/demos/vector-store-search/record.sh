#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
demo_start_api
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
demo_window_size="${DEMO_WINDOW_SIZE:-80x14}"
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme "${DEMO_THEME:-asciinema}" --fps-cap 20 --last-frame-duration 2)
{
  echo 'Feature: vector-store empty search'
  echo "Before commit: $demo_before_sha"
  echo "After commit: $demo_after_sha"
  echo "Window: $demo_window_size; theme: ${DEMO_THEME:-asciinema}"
  echo 'Synthetic loopback API; isolated Bash PTY; terminal replay, not graphical terminal acceptance.'
  demo_capture_metadata
  shasum -a 256 "$demo_source/server.py" "$demo_source/scene.sh" "$demo_source/record.sh"
} > "$demo_output/metadata.txt"
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_scene: empty vector-store search" \
    "HOME=$demo_runtime/$demo_scene-home" NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2
done
demo_stop_api
python3 - "$demo_output" <<'PY'
import pathlib, sys
root = pathlib.Path(sys.argv[1])
before = (root / "before.txt").read_text()
after = (root / "after.txt").read_text()
assert "No results." in before
assert "No matches returned." in after
assert "Indexing state was not checked." in after
assert "No results." not in after
PY
demo_assemble_capture 200 before after
printf 'Recorded vector-store search in %s\n' "$demo_output"
