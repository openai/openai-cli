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
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/files-workflow-demo-api}"
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: Files upload, metadata, and download workflow'
  echo "comparison base / before commit: $demo_before_sha"
  echo "candidate / after commit: $demo_after_sha"
  echo 'capture: synthetic loopback API; isolated Bash PTYs; separate temporary homes'
  echo 'scope: asciinema/agg replay; no graphical terminal or live API validation'
  echo 'render: 110 columns x 46 rows; Menlo 18px; asciinema theme'
  echo 'files: generated synthetic text and binary sources; uploads checked by the fixture'
  echo 'checks: real command exits; all four downloads compared against original bytes'
  demo_capture_metadata
  shasum -a 256 "$demo_source/main.go" "$demo_source/record.sh" "$demo_source/scene.sh" "$demo_source/validate.py"
  go version -m "$demo_before"
  go version -m "$demo_after"
} > "$demo_output/metadata.txt"
if [ -n "${DEMO_SOURCE_MANIFEST:-}" ]; then
  cp "$DEMO_SOURCE_MANIFEST" "$demo_output/source-manifest.txt"
fi
demo_window_size=110x46
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home" "$demo_output/$demo_scene-files"
  "$demo_python" - "$demo_output/$demo_scene-files" <<'PY'
import pathlib, sys
root = pathlib.Path(sys.argv[1])
(root / 'upload space.txt').write_bytes(b'hello files!\n')
(root / 'source.bin').write_bytes(bytes([0,255,13,10,27,65,128,0,7,8,9,10]))
PY
  demo_label='BEFORE: API command names'
  if [ "$demo_scene" = after ]; then demo_label='AFTER: upload, get, download'; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_label" \
    "HOME=$demo_runtime/$demo_scene-home" NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 \
    "DEMO_SCENE=$demo_scene" "DEMO_INPUT_DIR=$demo_output/$demo_scene-files"
done
demo_stop_api
"$demo_python" "$demo_source/validate.py" "$demo_output" | tee "$demo_output/validation.txt"
demo_assemble_capture 300 before after
printf 'Recorded Files workflow in %s\n' "$demo_output"
