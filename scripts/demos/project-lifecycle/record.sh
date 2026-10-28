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
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
demo_window_size="${DEMO_WINDOW_SIZE:-80x36}"
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme "${DEMO_THEME:-asciinema}" --fps-cap 20 --last-frame-duration 3)
{
  echo 'feature: project lifecycle receipts'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo "window: $demo_window_size; theme: ${DEMO_THEME:-asciinema}; Menlo 18px"
  echo 'synthetic loopback API; isolated Bash PTYs; asciinema/agg replay'
  echo 'no graphical terminal or live API validation'
  demo_capture_metadata
  shasum -a 256 "$demo_source/server.py" "$demo_source/scene.sh" "$demo_source/record.sh" "$demo_source/validate.py"
} > "$demo_output/metadata.txt"
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_scene: project lifecycle" \
    "HOME=$demo_runtime/$demo_scene-home" OPENAI_ADMIN_KEY=synthetic-admin-key NO_COLOR=1 GOMAXPROCS=2
done
demo_stop_api
"$demo_python" "$demo_source/validate.py" "$demo_output" | tee "$demo_output/validation.txt"
demo_assemble_capture 300 before after
