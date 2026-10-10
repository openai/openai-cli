#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
if [ "$3" != 5351ddb021263896da3a6871b621e19d38e8ffe9 ]; then
  echo 'Use published 5351ddb021263896da3a6871b621e19d38e8ffe9 as the comparison baseline.' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
demo_width="${DEMO_WIDTH:-80}"
case "$demo_width" in 80|40) ;; *) echo 'DEMO_WIDTH must be 80 or 40.' >&2; exit 2;; esac
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
demo_start_api "$demo_output/requests.jsonl" "$demo_runtime/confirmed"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: webhook recovery advice, grouped event discovery, and guided creation'
  echo "before source commit: $demo_before_sha"
  echo "after source commit: $demo_after_sha"
  echo "after source state: ${DEMO_AFTER_SOURCE_STATE:-committed source supplied by caller}"
  echo 'data: synthetic loopback API, event names, endpoint, and signing secret; no webhook receiver call'
  echo 'fixture: identical API HTTP 200 / receiver HTTP 500 before and after; four catalog events'
  echo 'capture: terminal replay; isolated Bash, temporary HOME, explicit environment'
  echo 'probe: separate stdout/stderr with explicit --format text; expected exit status zero'
  echo 'settings: NO_COLOR=1, FORCE_COLOR=0, GOMAXPROCS=2, no proxy or personal CLI configuration'
  echo "render: agg swash, Menlo 18px, Dracula, outer ${demo_width}x40; guided child ${demo_width}x24"
  echo 'scope: terminal replay; no native terminal appearance or live API claim'
  demo_capture_metadata
  "$demo_python" --version
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_before" "$demo_after" "$demo_source/record.sh" "$demo_source/scene.sh" \
  "$demo_source/server.py" "$demo_source/validate.py" "$demo_source/guided_scene.py" \
  "$demo_root/scripts/demos/capture_and_render.sh" "$demo_root/scripts/image_picker_harness.py" \
  > "$demo_output/source-sha256.txt"
(
  cd "$demo_root"
  git ls-files -z --cached --others --exclude-standard -- '*.go' ':!:*_test.go' go.mod go.sum | xargs -0 shasum -a 256
) > "$demo_output/runtime-source-sha256.txt"
demo_window_size="${demo_width}x40"
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after discovery guided; do
  demo_command_dir="$demo_runtime/after"
  demo_label='After: result and recovery advice'
  if [ "$demo_scene" = before ]; then
    demo_command_dir="$demo_runtime/before"
    demo_label='Before: result only'
  elif [ "$demo_scene" = discovery ]; then
    demo_label='After: discover webhook events'
  elif [ "$demo_scene" = guided ]; then
    demo_label='After: guided webhook creation'
  fi
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_endpoint="$demo_api_url/$demo_scene/v1"
  if [ "$demo_scene" != guided ]; then
    demo_args=(webhooks test --webhook-endpoint-id wh_demo --event-type response.completed)
    if [ "$demo_scene" = discovery ]; then demo_args=(webhooks event-types list); fi
    if env -i PATH="$demo_command_dir:/usr/bin:/bin" HOME="$demo_runtime/$demo_scene-home" \
      XDG_CONFIG_HOME="$demo_runtime/$demo_scene-home" LANG=en_US.UTF-8 TERM=xterm-256color \
      NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 OPENAI_API_KEY=synthetic-demo-key OPENAI_BASE_URL="$demo_endpoint" \
      openai "${demo_args[@]}" --format text \
      > "$demo_output/$demo_scene-probe.stdout" 2> "$demo_output/$demo_scene-probe.stderr"; then
      demo_status=0
    else
      demo_status=$?
    fi
    printf '%s\n' "$demo_status" > "$demo_output/$demo_scene-probe.status"
    test "$demo_status" -eq 0
  fi
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_endpoint" "$demo_label" \
    "HOME=$demo_runtime/$demo_scene-home" "XDG_CONFIG_HOME=$demo_runtime/$demo_scene-home" \
    NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 "DEMO_SCENE=$demo_scene" "DEMO_WIDTH=$demo_width" \
    "DEMO_STATUS_FILE=$demo_output/$demo_scene.status" "DEMO_PYTHON=$demo_python" \
    "DEMO_ASCIINEMA=$demo_asciinema" \
    "DEMO_DRIVER=$demo_source/guided_scene.py" "DEMO_EVIDENCE_BASE=$demo_output/guided-evidence" \
    "DEMO_CONFIRMATION_FILE=$demo_runtime/confirmed"
done
demo_stop_api
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
(
  cd "$demo_root"
  shasum -a 256 -c "$demo_output/runtime-source-sha256.txt"
) > "$demo_output/runtime-source-validation.txt"
"$demo_python" -I -B "$demo_source/validate.py" "$demo_output" "$demo_width" | tee "$demo_output/validation.txt"
demo_assemble_capture 200 before after discovery guided
for demo_stage in name url events no-matches restored-events review back review-return confirm; do
  demo_stage_time="$("$demo_python" -I -B -c \
    'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' \
    "$demo_output/guided-frame-times.json" "$demo_stage")"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_stage_time" \
    "$demo_output/guided.cast" "$demo_output/guided-$demo_stage-frame.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/guided-$demo_stage-frame.gif" \
    -frames:v 1 "$demo_output/guided-$demo_stage.png"
done
shasum -a 256 "$demo_output/"*.png "$demo_output/comparison.gif" > "$demo_output/media-sha256.txt"
printf 'Recorded webhook test terminal replays in %s\n' "$demo_output"
