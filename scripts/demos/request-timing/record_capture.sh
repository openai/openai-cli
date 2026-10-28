#!/bin/bash
set -euo pipefail
if [ "$#" -ne 7 ]; then
  echo 'usage: record_capture.sh REPO BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR normal|narrow' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$1" && pwd)"
demo_python="${DEMO_PYTHON:-$(command -v python3)}"
demo_viewport="$7"
case "$demo_viewport" in
  normal) demo_window_size=112x68;;
  narrow) demo_window_size=72x84;;
  *) echo 'viewport must be normal or narrow' >&2; exit 2;;
esac
source "$demo_root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$2" "$3" "$4" "$5" "$6" "$demo_python"
printf '#!/bin/bash\nexec %q -I -S -B %q "$@"\n' "$demo_python" "$demo_source/capture_api.py" > "$demo_runtime/api"
chmod 700 "$demo_runtime/api"
demo_api_binary="$demo_runtime/api"
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/capture_scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: explicitly selected redacted body and SSE diagnostics with event timing'
  echo "before source commit: $demo_before_sha"
  echo "after source commit: $demo_after_sha"
  echo 'fixture: identical synthetic Responses requests within each body or streaming comparison'
  echo 'capture: shared lifecycle; isolated Bash PTYs; synthetic credentials; monochrome'
  echo 'scope: terminal replay; no native Terminal appearance or live API claim'
  echo "render: $demo_viewport; $demo_window_size; Menlo 18px; line height 1.4; background #171717; foreground #eeeeee"
  demo_capture_metadata
  shasum -a 256 "$demo_source/record_capture.sh" "$demo_source/capture_scene.sh" \
    "$demo_source/capture_api.py" "$demo_source/validate_capture.py" "$demo_python"
} > "$demo_output/metadata.txt"
demo_palette='171717,eeeeee,000000,cd3131,0dbc79,e5e510,2472c8,bc3fbc,11a8cd,e5e5e5,666666,f14c4c,23d18b,f5f543,3b8eea,d670d6,29b8db,ffffff'
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 \
  --line-height 1.4 --theme "$demo_palette" --fps-cap 20 --last-frame-duration 2)
demo_scenes=(before-body after-body before-stream after-stream after-limits)
for demo_scene in "${demo_scenes[@]}"; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  case "$demo_scene" in
    before-body) demo_kind=before; demo_label='Before: ordinary --debug; no JSON body preview';;
    after-body) demo_kind=after; demo_label='After: redacted JSON structure and safe control values';;
    before-stream) demo_kind=before; demo_label='Before: ordinary --debug; existing parsed stream output';;
    after-stream) demo_kind=after; demo_label='After: redacted SSE frames and measured event timing';;
    after-limits) demo_kind=after; demo_label='After: bounded diagnostics; complete stream output';;
  esac
  demo_source_sha="$demo_before_sha"
  if [ "$demo_kind" = after ]; then demo_source_sha="$demo_after_sha"; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_kind" "$demo_api_url" "$demo_label" \
    "HOME=$demo_runtime/$demo_scene-home" "XDG_CONFIG_HOME=$demo_runtime/$demo_scene-home" \
    "XDG_CACHE_HOME=$demo_runtime/$demo_scene-home/cache" "DEMO_CAPTURE_KIND=$demo_scene" \
    "DEMO_SOURCE_SHA=$demo_source_sha" \
    OPENAI_CLI_NO_UPDATE_CHECK=1 NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2
done
demo_stop_api
"$demo_python" -I -S -B "$demo_source/validate_capture.py" "$demo_output" | tee "$demo_output/validation.txt"
demo_assemble_capture 200 "${demo_scenes[@]}"
printf 'Recorded redacted HTTP capture in %s\n' "$demo_output"
