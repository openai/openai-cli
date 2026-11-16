#!/bin/bash
set -euo pipefail
if [ "$#" -ne 3 ]; then
  echo 'usage: record-workflow.sh CANDIDATE_BINARY CANDIDATE_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$1" "$2" "$2" "$3" "$demo_source/server.py"
demo_start_api "$demo_output/requests.jsonl" "$demo_runtime/confirmed"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
mkdir -p "$demo_runtime/home"
{
  echo 'feature: combined webhook form, event selection, endpoint browser, and confirmation'
  echo "candidate source commit: $demo_after_sha"
  echo 'data: synthetic loopback API; no webhook receiver call'
  echo 'settings: explicit environment, temporary HOME, truecolor dark palette, GOMAXPROCS=2'
  echo 'render: agg swash, Menlo 18px, Dracula, outer 80x28, child 80x24'
  echo 'scope: terminal replay; no native graphical or live API claim'
  demo_capture_metadata
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_after" "$demo_source/record-workflow.sh" "$demo_source/workflow_scene.py" \
  "$demo_source/guided_scene.py" "$demo_source/server.py" "$demo_source/scene.sh" \
  "$demo_source/validate-workflow.py" "$demo_root/scripts/demos/capture_and_render.sh" \
  "$demo_root/scripts/image_picker_harness.py" > "$demo_output/source-sha256.txt"
demo_window_size='80x28'
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
demo_capture_scene workflow 0 "$demo_runtime/after" "$demo_api_url/workflow/v1" \
  'Create and inspect webhook endpoints' \
  "HOME=$demo_runtime/home" "XDG_CONFIG_HOME=$demo_runtime/home" \
  FORCE_COLOR=1 COLORTERM=truecolor COLORFGBG='15;0' GOMAXPROCS=2 DEMO_SCENE=workflow \
  "DEMO_STATUS_FILE=$demo_output/workflow.status" "DEMO_PYTHON=$demo_python" \
  "DEMO_ASCIINEMA=$demo_asciinema" "DEMO_DRIVER=$demo_source/workflow_scene.py" \
  "DEMO_EVIDENCE_BASE=$demo_output/workflow-evidence" "DEMO_CONFIRMATION_FILE=$demo_runtime/confirmed"
demo_stop_api
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
"$demo_python" -I -B "$demo_source/validate-workflow.py" "$demo_output" | tee "$demo_output/validation.txt"
for demo_stage in form category search selection events endpoints endpoint-details draft-return confirmation confirm-yes; do
  demo_stage_time="$("$demo_python" -I -B -c \
    'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' \
    "$demo_output/workflow-frame-times.json" "$demo_stage")"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_stage_time" \
    "$demo_output/workflow.cast" "$demo_output/$demo_stage-frame.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_stage-frame.gif" \
    -frames:v 1 "$demo_output/$demo_stage.png"
done
shasum -a 256 "$demo_output/"*.png "$demo_output/workflow.gif" > "$demo_output/media-sha256.txt"
printf 'Recorded synthetic webhook workflow in %s\n' "$demo_output"
