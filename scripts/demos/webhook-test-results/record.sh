#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
if [ "$3" != e68939820415144d769ed02de6aa72d5b7d32948 ]; then
  echo 'Use the pinned e68939820415144d769ed02de6aa72d5b7d32948 baseline.' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
demo_width="${DEMO_WIDTH:-80}"
case "$demo_width" in 80|40) ;; *) echo 'DEMO_WIDTH must be 80 or 40.' >&2; exit 2;; esac
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: webhook test completion and receiver response'
  echo "before source commit: $demo_before_sha"
  echo "after source commit: $demo_after_sha"
  echo "after source state: ${DEMO_AFTER_SOURCE_STATE:-committed source supplied by caller}"
  echo 'data: synthetic loopback test API only; no webhook receiver call'
  echo 'fixture: API HTTP 200; receiver HTTP 500 before/after, receiver HTTP 200 in accepted scene'
  echo 'capture: terminal replay; isolated Bash, temporary HOME, explicit environment'
  echo 'probe: separate stdout/stderr with explicit --format text; expected exit status zero'
  echo 'settings: NO_COLOR=1, FORCE_COLOR=0, GOMAXPROCS=2, no proxy or personal CLI configuration'
  echo "render: agg swash, Menlo 18px, Dracula, ${demo_width} columns x 24 rows"
  echo 'scope: terminal replay; no native terminal appearance or live API claim'
  demo_capture_metadata
  "$demo_python" --version
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_before" "$demo_after" "$demo_source/record.sh" "$demo_source/scene.sh" \
  "$demo_source/server.py" "$demo_source/validate.py" "$demo_root/scripts/demos/capture_and_render.sh" \
  > "$demo_output/source-sha256.txt"
demo_window_size="${demo_width}x24"
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after accepted; do
  demo_command_dir="$demo_runtime/after"
  demo_label='After: receiver HTTP 500'
  if [ "$demo_scene" = before ]; then
    demo_command_dir="$demo_runtime/before"
    demo_label='Before: receiver HTTP 500'
  elif [ "$demo_scene" = accepted ]; then
    demo_label='After: receiver HTTP 200'
  fi
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_endpoint="$demo_api_url/$demo_scene/v1"
  if env -i PATH="$demo_command_dir:/usr/bin:/bin" HOME="$demo_runtime/$demo_scene-home" \
    LANG=en_US.UTF-8 TERM=xterm-256color NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 \
    OPENAI_API_KEY=synthetic-demo-key OPENAI_BASE_URL="$demo_endpoint" \
    openai webhooks test --webhook-endpoint-id wh_demo --event-type response.completed --format text \
    > "$demo_output/$demo_scene-probe.stdout" 2> "$demo_output/$demo_scene-probe.stderr"; then
    demo_status=0
  else
    demo_status=$?
  fi
  printf '%s\n' "$demo_status" > "$demo_output/$demo_scene-probe.status"
  test "$demo_status" -eq 0
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_endpoint" "$demo_label" \
    "HOME=$demo_runtime/$demo_scene-home" NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 \
    "DEMO_STATUS_FILE=$demo_output/$demo_scene.status"
done
demo_stop_api
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
"$demo_python" -I -B "$demo_source/validate.py" "$demo_output" "$demo_width" | tee "$demo_output/validation.txt"
demo_assemble_capture 200 before after
shasum -a 256 "$demo_output/"*.png "$demo_output/comparison.gif" > "$demo_output/media-sha256.txt"
printf 'Recorded webhook test terminal replays in %s\n' "$demo_output"
