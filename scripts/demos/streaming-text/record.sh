#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo "usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR" >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/streaming-demo-api}"
demo_python="$(command -v python3)"
test "$("$demo_asciinema" --version)" = 'asciinema 3.2.1'
test "$("$demo_agg" --version)" = 'agg 1.9.0'
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/streaming-text.tape" "$demo_output/comparison.tape"
cp "$demo_source/events.json" "$demo_output/events.json"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | asciinema + agg | synthetic loopback API'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.25
demo_args=(responses create --model demo-model --stream=true --input 'Say hello')
if [ "$DEMO_FORMAT" = jsonl ]; then demo_args=(--format jsonl "${demo_args[@]}"); fi
printf '$ openai'
if [ "$DEMO_FORMAT" = jsonl ]; then printf ' --format jsonl'; fi
printf " responses create --model demo-model --stream=true --input 'Say hello'\n"
sleep 0.25
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 2
exit "$demo_status"
SCENE

{
  echo "feature: streaming text"
  echo "captured before commit: $demo_before_sha"
  echo "captured after commit: $demo_after_sha"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo "data: four fixed synthetic SSE events, 650 ms apart; fake key; loopback only"
  echo "capture: real PTY, stdin/stdout/stderr checked, isolated environment, bash --noprofile --norc"
  echo "render: asciinema 3.2.1 + agg 1.9.0 resvg; Menlo 22px, Dracula, 90 columns x 40 rows, line height 1.2, 20 fps cap"
  echo "scope: terminal replay, no native Apple Terminal, PowerShell, or cmd.exe capture"
  echo "recipe: record.sh; comparison.tape is the alternative VHS recipe, not the renderer used"
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record.sh" "$demo_source/main.go" \
    "$demo_source/events.json" "$demo_source/validate.py"
} > "$demo_output/metadata.txt"

demo_window_size=90x40
demo_render_options=(--renderer resvg --font-family Menlo --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after explicit-jsonl; do
  demo_command_dir="$demo_runtime/after"
  demo_format=default
  case "$demo_scene" in
    before) demo_command_dir="$demo_runtime/before"; demo_label='Before: main | delayed synthetic stream';;
    after) demo_label='After: streaming text | identical delayed events';;
    explicit-jsonl) demo_format=jsonl; demo_label='Explicit JSONL: every original event remains available';;
  esac
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_api_url" "$demo_label" \
    FORCE_COLOR=0 NO_COLOR=1 "DEMO_FORMAT=$demo_format"
done
# Finish the request log before validating it or reporting a successful recording.
demo_stop_api
"$demo_python" "$demo_source/validate.py" "$demo_output" "$demo_source/events.json" \
  > "$demo_output/validation.txt"
demo_assemble_capture 200 before after explicit-jsonl
printf 'Recorded streaming terminal replay in %s\n' "$demo_output"
