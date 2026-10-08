#!/bin/bash
set -euo pipefail

if [ "$#" -ne 6 ]; then
  echo 'usage: record.sh MODE BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  echo 'MODE: count, inspect, codex, or guide' >&2
  exit 2
fi
demo_mode="$1"
case "$demo_mode" in
  count|inspect|codex|guide) ;;
  *) echo 'MODE must be count, inspect, codex, or guide.' >&2; exit 2;;
esac
shift
demo_columns="${DEMO_COLUMNS:-80}"
case "$demo_columns" in
  40|80) ;;
  *) echo 'DEMO_COLUMNS must be 40 or 80.' >&2; exit 2;;
esac
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/../capture_and_render.sh"
# The shared lifecycle requires an executable fixture argument. It is unused here.
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$2"
demo_rows=24
if [ "$demo_mode" = guide ]; then demo_rows=40; fi
if [ "$demo_columns" = 40 ]; then demo_rows=$((demo_rows + 20)); fi
demo_window_size="${demo_columns}x${demo_rows}"
demo_render_options=(--renderer resvg --font-family Menlo --font-size 22 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
# Remove the synthetic credential inserted by the shared capture helper.
unset OPENAI_API_KEY OPENAI_ADMIN_KEY OPENAI_WEBHOOK_SECRET
printf '\033[2J\033[H\033[?25l%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.4
case "$DEMO_MODE" in
  count)
    printf '%s\n' '$ openai tokenizer count --text "Hello, world!"'
    demo_args=(tokenizer count --text 'Hello, world!')
    ;;
  inspect)
    printf '%s\n' '$ openai tokenizer inspect --text "Hi!"'
    demo_args=(tokenizer inspect --text 'Hi!')
    ;;
  codex)
    printf '%s\n' '$ openai codex --destination config'
    demo_args=(codex --destination config)
    ;;
  guide)
    printf '%s\n' '$ openai codex'
    demo_args=(codex)
    ;;
  *) exit 2;;
esac
sleep 0.4
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '%s\t%s\n' "$DEMO_SCENE" "$demo_status" >> "$DEMO_STATUS_LOG" || exit 98
printf '\n$ '
sleep 3
exit "$demo_status"
SCENE

{
  echo 'feature: local tokenizer and Codex instructions'
  echo "mode: $demo_mode"
  echo "before commit: $demo_before_sha"
  echo "candidate commit: $demo_after_sha (check source manifest for uncommitted changes)"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo 'data: synthetic local text; no API credentials'
  echo 'API fixture: not started; required executable argument is the unused after binary'
  echo 'API configuration: rejecting loopback address http://127.0.0.1:1; no live endpoint'
  echo 'capture: actual commands in isolated Bash PTYs; actual errors and statuses retained'
  echo "terminal dimensions: $demo_window_size"
  echo 'render: asciinema and agg; Menlo 22px; asciinema theme; 20 fps cap'
  echo 'scope: terminal replay, not graphical terminal or native Windows/Linux validation'
  echo 'browser: no --open flag; installation commands are printed only'
  echo 'recipe: scripts/demos/tokenizer-codex/record.sh'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record.sh" "$demo_source/validate.py" "$demo_source/README.md"
} > "$demo_output/metadata.txt"
if command -v go >/dev/null; then
  go version -m "$demo_before" > "$demo_output/before-build-info.txt"
  go version -m "$demo_after" > "$demo_output/after-build-info.txt"
fi
if [ -n "${DEMO_SOURCE_MANIFEST:-}" ]; then
  cp "$DEMO_SOURCE_MANIFEST" "$demo_output/candidate-source-sha256.txt"
  shasum -a 256 "$demo_output/candidate-source-sha256.txt" >> "$demo_output/metadata.txt"
fi
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
demo_before_status=1
if [ "$demo_mode" = guide ]; then demo_before_status=3; fi
demo_capture_scene before "$demo_before_status" "$demo_runtime/before" 'http://127.0.0.1:1' 'BEFORE' \
  "DEMO_MODE=$demo_mode" 'DEMO_SCENE=before' "DEMO_STATUS_LOG=$demo_output/statuses.tsv" NO_COLOR=1
demo_capture_scene after 0 "$demo_runtime/after" 'http://127.0.0.1:1' 'AFTER' \
  "DEMO_MODE=$demo_mode" 'DEMO_SCENE=after' "DEMO_STATUS_LOG=$demo_output/statuses.tsv" NO_COLOR=1
"$demo_python" "$demo_source/validate.py" "$demo_output" "$demo_mode" > "$demo_output/validation.txt"
demo_assemble_capture 300 before after
printf 'Recorded %s terminal replay in %s\n' "$demo_mode" "$demo_output"
