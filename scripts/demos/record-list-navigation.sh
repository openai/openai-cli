#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-list-navigation.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/list-navigation-demo-api}"
demo_python="$(command -v python3)"
demo_start_api "$demo_output/requests.jsonl"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' 'Terminal replay | synthetic loopback API | two pages of files'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
exec "$DEMO_PYTHON" -I -B "$DEMO_DRIVER" --scene "$DEMO_SCENE_NAME" \
  --counts-url "$DEMO_COUNTS_URL" --evidence "$DEMO_EVIDENCE_FILE"
SCENE

{
  echo 'feature: demand-driven terminal list navigation'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'command in both scenes: openai files list --limit 2 --max-items -1'
  echo 'data: four synthetic files, two per API page; loopback only; fake key'
  echo 'capture: shared asciinema lifecycle; nested private PTY forwards exact CLI output'
  echo 'input: observe 1.5 seconds without keys; after receives Space, then q after 1.5 seconds'
  echo 'evidence: server counts exclude count-observation requests; every CLI request is recorded'
  echo 'environment: temporary HOME; no inherited credentials, CI setting, or shell startup files'
  echo 'render: asciinema + agg swash, Menlo 20px, Dracula, 90x30, line height 1.2'
  echo 'scope: terminal replay; not native Apple Terminal, Windows, or Linux appearance'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record-list-navigation.sh" "$demo_source/list-navigation/main.go" \
    "$demo_source/list-navigation/drive.py" "$demo_source/list-navigation/validate.py" \
    "$demo_root/scripts/image_picker_harness.py"
} > "$demo_output/metadata.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
demo_window_size=90x30
demo_render_options=(--renderer swash --font-family Menlo --font-size 20 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after; do
  if [ "$demo_scene" = before ]; then
    demo_label='Before: pages arrive without keyboard input'
  else
    demo_label='After: Space requests the next page; q quits'
  fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" \
    "$demo_api_url/$demo_scene/v1" "$demo_label" \
    "DEMO_PYTHON=$demo_python" "DEMO_ASCIINEMA=$demo_asciinema" "DEMO_DRIVER=$demo_source/list-navigation/drive.py" \
    "DEMO_SCENE_NAME=$demo_scene" "DEMO_COUNTS_URL=$demo_api_url/_counts/$demo_scene" \
    "DEMO_EVIDENCE_FILE=$demo_output/$demo_scene-evidence.json"
done
demo_stop_api
"$demo_python" -I -B "$demo_source/list-navigation/validate.py" "$demo_output" > "$demo_output/validation.txt"
demo_assemble_capture 200 before after
printf 'Recorded list navigation comparison in %s\n' "$demo_output"
