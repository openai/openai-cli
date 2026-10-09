#!/bin/bash
set -euo pipefail
if [ "$#" -ne 6 ]; then
  echo 'usage: record.sh REPO BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$1" && pwd)"
demo_python="$(command -v python3)"
source "$demo_root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$2" "$3" "$4" "$5" "$6" "$demo_source/api.py"
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: HTTP attempt timing in existing --debug diagnostics'
  echo "before source commit: $demo_before_sha"
  echo "after source commit: $demo_after_sha"
  echo 'command: openai --debug models retrieve model_synthetic'
  echo 'fixture: identical synthetic model; 180 ms before headers; two further 240 ms body delays'
  echo 'capture: shared lifecycle; isolated Bash PTYs; synthetic credentials'
  echo 'scope: terminal replay; no native Terminal appearance or live API claim'
  echo 'render: 112 columns x 43 rows; Menlo 18px; asciinema theme'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record.sh" "$demo_source/scene.sh" "$demo_source/validate.py"
} > "$demo_output/metadata.txt"
demo_window_size=112x43
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_label='Before: redacted headers without timing'
  if [ "$demo_scene" = after ]; then demo_label='After: headers, first response data, and complete body timing'; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_label" \
    "HOME=$demo_runtime/$demo_scene-home" "XDG_CONFIG_HOME=$demo_runtime/$demo_scene-home" \
    NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2
done
demo_stop_api
"$demo_python" -I -B "$demo_source/validate.py" "$demo_output" | tee "$demo_output/validation.txt"
demo_assemble_capture 300 before after
printf 'Recorded HTTP timing in %s\n' "$demo_output"
