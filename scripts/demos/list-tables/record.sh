#!/bin/bash
set -euo pipefail

case "$#" in
  5|6|8) ;;
  *) echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR [110|40] [--resource RESOURCE]' >&2; exit 2;;
esac
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_columns="${6:-110}"
case "$demo_columns" in 110|40) ;; *) echo 'Use width 110 or 40.' >&2; exit 2;; esac
demo_color="${DEMO_COLOR:-0}"
case "$demo_color" in
  0) demo_scene_color_env=(NO_COLOR=1 FORCE_COLOR=0) ;;
  1) demo_scene_color_env=(COLORTERM=truecolor 'COLORFGBG=15;0' TERM_PROGRAM=Apple_Terminal) ;;
  *) echo 'Use DEMO_COLOR=0 or DEMO_COLOR=1.' >&2; exit 2;;
esac
demo_resources=(files batches projects)
demo_validation_args=()
if [ "$#" -eq 8 ]; then
  test "$7" = --resource
  case "$8" in files|batches|projects) ;; *) echo 'Unknown resource.' >&2; exit 2;; esac
  demo_resources=("$8")
  demo_validation_args=(--resource "$8")
fi
demo_rows=28
if [ "$demo_columns" = 40 ]; then demo_rows=36; fi
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/list-table-demo-api}"
demo_python="$(command -v python3)"
demo_start_api "$demo_output/requests.jsonl"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf 'Synthetic loopback API\nTerminal replay | %s columns\n%s\n\n' "$DEMO_COLUMNS" "$DEMO_SCENE_LABEL"
demo_args=("$DEMO_RESOURCE" list)
if [ "$DEMO_RESOURCE" = projects ]; then demo_args=(admin organization projects list); fi
printf '$ openai'
printf ' %s' "${demo_args[@]}"
printf '\n'
sleep 0.2
if "$DEMO_PYTHON" "$DEMO_SCENE_DRIVER" openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '$ '
sleep 2
exit "$demo_status"
SCENE

{
  echo 'feature: compact default terminal list tables'
  echo 'activation: handwritten application hooks connect Files, Batches, and Projects to shared list presentation'
  echo "captured resources: ${demo_resources[*]}"
  echo "captured before commit: $demo_before_sha"
  echo "captured after source commit: $demo_after_sha"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo 'data: fixed synthetic list pages; fake API/admin keys; loopback only'
  echo 'capture: real PTYs, stdin/stdout/stderr verified; isolated bash --noprofile --norc'
  echo "scene color mode: DEMO_COLOR=$demo_color; ${demo_scene_color_env[*]}"
  echo 'navigation: pager remains open; q restores the original screen; screenshots show the loaded page'
  echo 'relay: outer PTY output translation disabled; recorded CLI bytes match their SHA256 digest'
  echo "render: asciinema + agg swash; Menlo 18px; Dracula; $demo_columns columns x $demo_rows rows"
  echo 'scope: terminal replay; no native Apple Terminal, PowerShell, or cmd.exe appearance claim'
  echo 'each scene: exactly one API request and exit status zero'
  echo 'machine checks: exact before/after stdout, stderr, exit status, and request-count comparisons'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record.sh" "$demo_source/main.go" "$demo_source/validate.py" "$demo_source/scene.py" \
    "$demo_source/check_pages.py" "$demo_root/scripts/image_picker_harness.py"
} > "$demo_output/metadata.txt"

demo_window_size="${demo_columns}x${demo_rows}"
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
demo_scenes=()
for demo_resource in "${demo_resources[@]}"; do
  for demo_version in before after; do
    demo_scene="$demo_resource-$demo_version"
    demo_scenes+=("$demo_scene")
    demo_requests_before="$(wc -l < "$demo_output/requests.jsonl")"
    demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_version" "$demo_api_url/normal/v1" \
      "$demo_version: $demo_resource list" \
      "DEMO_RESOURCE=$demo_resource" "DEMO_COLUMNS=$demo_columns" \
      "DEMO_PYTHON=$demo_python" "DEMO_SCENE_DRIVER=$demo_source/scene.py" \
      "DEMO_RELAY_EVIDENCE=$demo_output/$demo_scene.relay.json" \
      OPENAI_ADMIN_KEY=synthetic-demo-admin-key GOMAXPROCS=2 "${demo_scene_color_env[@]}"
    demo_requests_after="$(wc -l < "$demo_output/requests.jsonl")"
    demo_request_count=$((demo_requests_after - demo_requests_before))
    echo "$demo_scene API requests: $demo_request_count (expected 1)" >> "$demo_output/metadata.txt"
    test "$demo_request_count" -eq 1
    demo_visible_time="$("$demo_python" -I -B - "$demo_output" "$demo_scene" <<'VISIBLE'
import json
from pathlib import Path
import sys
output, scene = Path(sys.argv[1]), sys.argv[2]
cast = [json.loads(line) for line in (output/f'{scene}.cast').read_text().splitlines()]
relay = json.loads((output/f'{scene}.relay.json').read_text())
command_time = next(event[0] for event in cast[1:] if event[1] == 'o' and '$ openai' in event[2])
cutoff = command_time + relay['inputs'][0]['time'] if relay['inputs'] else cast[-1][0]
prefix = [cast[0], *[event for event in cast[1:] if event[0] <= cutoff]]
(output/f'{scene}-visible.cast').write_text('\n'.join(json.dumps(item) for item in prefix)+'\n')
print(cutoff)
VISIBLE
)"
    "$demo_asciinema" convert -f txt "$demo_output/$demo_scene-visible.cast" "$demo_output/$demo_scene-visible.txt"
    "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_visible_time" \
      "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene-visible.gif"
    "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_scene-visible.gif" \
      -frames:v 1 "$demo_output/$demo_scene-visible.png"
  done
done
"$demo_python" "$demo_source/validate.py" "$demo_before" "$demo_after" "$demo_api_url" \
  "$demo_output" ${demo_validation_args[@]+"${demo_validation_args[@]}"} > "$demo_output/validation.txt"
"$demo_python" "$demo_source/check_pages.py" "$demo_after" "$demo_api_url" "$demo_output" \
  --width "$demo_columns" ${demo_validation_args[@]+"${demo_validation_args[@]}"} > "$demo_output/page-validation.txt"
# Shutdown checks response-write and request-log failures before reporting success.
demo_stop_api
demo_assemble_capture 200 "${demo_scenes[@]}"
printf 'Recorded list-table terminal replays in %s\n' "$demo_output"
