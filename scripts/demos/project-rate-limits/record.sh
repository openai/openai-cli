#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
demo_start_api
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo "before: $demo_before_sha"
  echo "after: $demo_after_sha"
  echo 'Synthetic API; macOS Bash PTY replay; no live mutation or native graphical claim.'
  demo_capture_metadata
  shasum -a 256 "$demo_source/server.py" "$demo_source/scene.sh" "$demo_source/record.sh"
} > "$demo_output/metadata.txt"
demo_window_size="${DEMO_SIZE:-100x30}"
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2
  --theme "${DEMO_THEME:-asciinema}" --fps-cap 10 --last-frame-duration 2)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "${demo_scene}: project rate limits" \
    "HOME=$demo_runtime/$demo_scene-home" "DEMO_SCENE=$demo_scene" NO_COLOR=1 FORCE_COLOR=0
done
demo_stop_api
"$demo_python" - "$demo_output" <<'PY'
import pathlib, sys
p = pathlib.Path(sys.argv[1])
before, after = ((p / (name + '.txt')).read_text() for name in ('before', 'after'))
assert 'An option is not recognized.' in before, before
assert 'No rate limits returned for proj_empty.' in after, after
assert 'Max tokens per 1 minute: 1000' in before, before
assert 'Max tokens per minute: 1000' in after, after
assert 'Max batch input tokens per day: 20000' in after, after
assert 'Model: model_demo' in before and 'Model: model_demo' in after
PY
demo_assemble_capture 200 before after
printf 'Recorded project rate limits in %s\n' "$demo_output"
