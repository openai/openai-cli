#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/output-demo-api}"
demo_python="$(command -v python3)"
test "$("$demo_asciinema" --version)" = 'asciinema 3.2.1'
test "$("$demo_agg" --version)" = 'agg 1.9.0'
ln -s "$demo_python" "$demo_runtime/before/python3"
ln -s "$demo_python" "$demo_runtime/after/python3"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
demo_start_api "$demo_output/requests.jsonl"

{
  echo 'feature: deterministic output and quiet/verbose'
  echo "captured before commit: $demo_before_sha"
  echo "captured after commit: $demo_after_sha"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo 'fixtures: one model and two fixed events, separated by two seconds; loopback only; fake key'
  echo 'capture: real PTY; isolated environment and HOME; bash --noprofile --norc'
  echo 'consumer: Python json.tool parses exact piped bytes; arrival.py reports actual event arrival times'
  echo 'statuses: every CLI, tee, and Python consumer result is retained in statuses.tsv'
  echo 'render: asciinema 3.2.1 + agg 1.9.0 resvg; Menlo 24px; asciinema theme; 104x26; 20fps'
  echo 'scope: terminal replay; no native Apple Terminal, PowerShell, or cmd.exe validation'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record.sh" "$demo_source/scene.sh" "$demo_source/main.go" \
    "$demo_source/fixtures.json" "$demo_source/arrival.py" "$demo_source/validate.py" \
    "$demo_ffmpeg" "$demo_ffprobe" "$demo_python"
} > "$demo_output/metadata.txt"

demo_window_size=104x26
demo_render_options=(--renderer resvg --font-family Menlo --font-size 24 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after controls; do
  demo_command_dir="$demo_runtime/after"
  demo_label='AFTER | plain pipes and immediate events'
  if [ "$demo_scene" = before ]; then
    demo_command_dir="$demo_runtime/before"
    demo_label='BEFORE | forced color and buffered events'
  elif [ "$demo_scene" = controls ]; then
    demo_label='AFTER | quiet and verbose'
  fi
  demo_home="$demo_runtime/home-$demo_scene"
  demo_data="$demo_output/data/$demo_scene"
  mkdir -p "$demo_home" "$demo_data"
  cp "$demo_source/arrival.py" "$demo_data/arrival.py"
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_api_url" "$demo_label" \
    FORCE_COLOR=0 "HOME=$demo_home" "XDG_CONFIG_HOME=$demo_home/config" \
    "DEMO_SCENE=$demo_scene" "DEMO_DATA_DIR=$demo_data" "DEMO_STATUS_LOG=$demo_output/statuses.tsv"
done
demo_stop_api
"$demo_python" "$demo_source/validate.py" "$demo_output" "$demo_source/fixtures.json" \
  | tee "$demo_output/validation.txt"
demo_assemble_capture 300 before after controls
printf 'Recorded deterministic-output terminal replays in %s\n' "$demo_output"
