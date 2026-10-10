#!/bin/bash
set -euo pipefail

if [ "$#" -ne 6 ] || [ "$1" != --run-authorized-pty-slot ]; then
  echo 'usage: record-spend-thresholds.sh --run-authorized-pty-slot BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
shift
record_source="$(cd "$(dirname "$0")" && pwd -P)"
record_repo="$(cd "$record_source/../.." && pwd -P)"
record_recipe="$record_source/spend-thresholds"
source "$record_source/capture_and_render.sh"
demo_prepare_capture "$record_repo" "$1" "$2" "$3" "$4" "$5" "$record_recipe/server.py"
demo_python="$(command -v python3)"
demo_start_api "$demo_output/requests.jsonl" "$demo_output/fixtures.json"
cp "$record_recipe/scene.sh" "$demo_runtime/scene.sh"
cp "$record_recipe/scene.sh" "$demo_output/scene.sh"
{
  echo 'feature: spend threshold units and notification semantics'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'scope: Bash PTY terminal replay; synthetic loopback API; no native graphical terminal validation'
  echo 'render: 100x22 and 40x24; Menlo 18px; asciinema theme; NO_COLOR=1'
  echo 'data: synthetic USD amounts in integer cents; fake admin credentials; isolated HOME'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_python" "$record_source/record-spend-thresholds.sh" \
    "$record_recipe/scene.sh" "$record_recipe/validate.py" "$record_recipe/README.md" \
    "$demo_output/fixtures.json"
} > "$demo_output/metadata.txt"
if [ -n "${DEMO_SOURCE_MANIFEST:-}" ]; then
  cp "$DEMO_SOURCE_MANIFEST" "$demo_output/source-manifest.txt"
  shasum -a 256 "$demo_output/source-manifest.txt" >> "$demo_output/metadata.txt"
fi
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 15 --last-frame-duration 2)
for record_width in 100 40; do
  demo_window_size="${record_width}x22"
  if [ "$record_width" -eq 40 ]; then demo_window_size=40x24; fi
  record_scenes=()
  for record_case in limit alerts json missing; do
    for record_phase in before after; do
      record_scene="$record_phase-$record_case-$record_width"
      record_scenes+=("$record_scene")
      record_endpoint="$demo_api_url/v1"
      if [ "$record_case" = missing ]; then record_endpoint="$demo_api_url/missing/v1"; fi
      mkdir -p "$demo_runtime/$record_scene-home"
      demo_capture_scene "$record_scene" 0 "$demo_runtime/$record_phase" "$record_endpoint" \
        "$record_phase: $record_case ($record_width columns)" \
        "HOME=$demo_runtime/$record_scene-home" "DEMO_CASE=$record_case" \
        OPENAI_ADMIN_KEY=synthetic-demo-admin-key NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2
    done
  done
  demo_assemble_capture 200 "${record_scenes[@]}"
  for record_suffix in cast gif txt; do
    mv "$demo_output/comparison.$record_suffix" "$demo_output/comparison-$record_width.$record_suffix"
  done
  mv "$demo_output/comparison-scenes.txt" "$demo_output/comparison-scenes-$record_width.txt"
  mv "$demo_output/media.json" "$demo_output/media-$record_width.json"
done
demo_stop_api
"$demo_python" "$record_recipe/validate.py" "$demo_output" > "$demo_output/validation.txt"
(
  cd "$demo_output"
  shasum -a 256 -- *.cast *.gif *.png *.txt *.json *.jsonl *.sh > SHA256SUMS
)
printf 'Recorded spend threshold terminal replays in %s\n' "$demo_output"
