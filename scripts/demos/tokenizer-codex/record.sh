#!/bin/bash
set -euo pipefail

if [ "$#" -ne 6 ]; then
  echo 'usage: record.sh MODE BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  echo 'MODE: count, inspect, codex, guide, editor, or details' >&2
  exit 2
fi
demo_mode="$1"
case "$demo_mode" in
  count|inspect|codex|guide|editor|details) ;;
  *) echo 'MODE must be count, inspect, codex, guide, editor, or details.' >&2; exit 2;;
esac
shift
demo_interactive=0
if [ "$demo_mode" = editor ] || [ "$demo_mode" = details ]; then demo_interactive=1; fi
demo_editor_before="${DEMO_EDITOR_BEFORE:-help}"
case "$demo_editor_before" in
  help|legacy|options) ;;
  *) echo 'DEMO_EDITOR_BEFORE must be help, legacy, or options.' >&2; exit 2;;
esac
if [ "$demo_mode" = details ]; then demo_editor_before=options; fi
demo_editor_linked="${DEMO_EDITOR_LINKED:-0}"
case "$demo_editor_linked" in
  0|1) ;;
  *) echo 'DEMO_EDITOR_LINKED must be 0 or 1.' >&2; exit 2;;
esac
if [ "$demo_editor_linked" = 1 ] && [ "$demo_mode" != editor ]; then
  echo 'DEMO_EDITOR_LINKED requires editor mode.' >&2
  exit 2
fi
demo_editor_presentation="${DEMO_EDITOR_PRESENTATION:-encodings}"
case "$demo_editor_presentation" in
  encodings|models) ;;
  *) echo 'DEMO_EDITOR_PRESENTATION must be encodings or models.' >&2; exit 2;;
esac
if [ "$demo_editor_presentation" = models ] && [ "$demo_editor_linked" != 1 ]; then
  echo 'Model presentation requires DEMO_EDITOR_LINKED=1 in editor mode.' >&2
  exit 2
fi
demo_columns="${DEMO_COLUMNS:-80}"
case "$demo_columns" in
  40|80) ;;
  *) echo 'DEMO_COLUMNS must be 40 or 80.' >&2; exit 2;;
esac
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
demo_theme="${DEMO_THEME:-no-color}"
if [ "$demo_interactive" = 1 ]; then demo_theme="${DEMO_THEME:-dark}"; fi
case "$demo_theme" in
  dark) demo_palette=asciinema; demo_theme_environment=('COLORFGBG=15;0' COLORTERM=truecolor);;
  light) demo_palette=github-light; demo_theme_environment=('COLORFGBG=0;15' COLORTERM=truecolor);;
  no-color) demo_palette=asciinema; demo_theme_environment=(NO_COLOR=1 'COLORFGBG=15;0');;
  *) echo 'DEMO_THEME must be dark, light, or no-color.' >&2; exit 2;;
esac
source "$demo_source/../capture_and_render.sh"
# The shared lifecycle requires an executable fixture argument. It is unused here.
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$2"
demo_rows=24
if [ "$demo_mode" = guide ]; then demo_rows=40; fi
if [ "$demo_columns" = 40 ]; then demo_rows=$((demo_rows + 20)); fi
if [ "$demo_mode" = editor ]; then
  demo_rows=32
  if [ "$demo_columns" = 40 ]; then demo_rows=44; fi
fi
if [ "$demo_mode" = details ]; then demo_rows=12; fi
demo_window_size="${demo_columns}x${demo_rows}"
demo_render_options=(--renderer resvg --font-family 'Menlo,Apple Color Emoji' --font-size 22 --line-height 1.2 \
  --theme "$demo_palette" --fps-cap 20 --last-frame-duration 3)

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
  editor|details)
    printf '%s\n' '$ openai tokenizer'
    demo_args=(tokenizer)
    ;;
  *) exit 2;;
esac
sleep 0.4
if [ "$DEMO_MODE" = details ] || { [ "$DEMO_MODE" = editor ] && [ "$DEMO_EDITOR_LAYOUT" != help ]; }; then
  if "$DEMO_PYTHON" "$DEMO_EDITOR_DRIVER"; then demo_status=0; else demo_status=$?; fi
else
  if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
fi
printf '%s\t%s\n' "$DEMO_SCENE" "$demo_status" >> "$DEMO_STATUS_LOG" || exit 98
printf '\033[?25h\n$ '
sleep 3
exit "$demo_status"
SCENE

{
  echo 'feature: local tokenizer and Codex instructions'
  echo "mode: $demo_mode"
  if [ "$demo_mode" = editor ]; then
    echo "before editor layout: $demo_editor_before"
    echo 'after editor layout: options'
    echo "after linked cursor and legacy tokenizers: $demo_editor_linked"
    echo "after editor presentation: $demo_editor_presentation"
  fi
  if [ "$demo_mode" = details ]; then echo 'details: six-token source, cursor, Up navigation, exact fields, overflow rows, Home, and recovery'; fi
  echo "before commit: $demo_before_sha"
  echo "candidate commit: $demo_after_sha (check source manifest for uncommitted changes)"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo 'data: synthetic local text; no API credentials'
  echo 'API fixture: not started; required executable argument is the unused after binary'
  echo 'API configuration: rejecting loopback address http://127.0.0.1:1; no live endpoint'
  echo 'capture: actual commands in isolated Bash PTYs; actual errors and statuses retained'
  echo "terminal dimensions: $demo_window_size"
  echo "render: asciinema and agg; Menlo/Apple Color Emoji 22px; $demo_palette theme; 20 fps cap"
  echo "application theme: $demo_theme"
  echo 'color capability: explicit truecolor for dark/light; NO_COLOR for no-color scenes'
  echo 'scope: terminal replay, not graphical terminal or native Windows/Linux validation'
  echo 'browser: no --open flag; installation commands are printed only'
  echo 'recipe: scripts/demos/tokenizer-codex/record.sh'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record.sh" "$demo_source/validate.py" "$demo_source/editor_driver.py" "$demo_source/README.md"
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
if [ "$demo_mode" = editor ]; then demo_before_status=0; fi
if [ "$demo_mode" = editor ] && [ "$demo_editor_before" != help ]; then demo_before_status=130; fi
if [ "$demo_mode" = details ]; then demo_before_status=130; fi
demo_before_status="${DEMO_BEFORE_STATUS:-$demo_before_status}"
case "$demo_before_status" in
  0|1|3) ;;
  130)
    if [ "$demo_interactive" != 1 ] || [ "$demo_editor_before" = help ]; then
      echo 'Status 130 requires an interactive editor baseline.' >&2
      exit 2
    fi
    ;;
  *) echo 'DEMO_BEFORE_STATUS must be 0, 1, 3, or 130 for an interactive editor.' >&2; exit 2;;
esac
if [ "$demo_mode" = editor ]; then
  demo_editor_status=0
  if [ "$demo_editor_before" != help ]; then demo_editor_status=130; fi
  if [ "$demo_before_status" != "$demo_editor_status" ]; then
    echo 'The editor baseline status must match its selected layout.' >&2
    exit 2
  fi
  printf 'before\t%s\nafter\toptions\n' "$demo_editor_before" > "$demo_output/editor-layouts.tsv"
  printf 'before\t0\nafter\t%s\n' "$demo_editor_linked" > "$demo_output/editor-linked.tsv"
  printf 'before\tencodings\nafter\t%s\n' "$demo_editor_presentation" > "$demo_output/editor-presentation.tsv"
fi
if [ "$demo_mode" = details ] && [ "$demo_before_status" != 130 ]; then
  echo 'Details comparison requires an interactive baseline with status 130.' >&2
  exit 2
fi
demo_after_status=0
if [ "$demo_interactive" = 1 ]; then demo_after_status=130; fi
printf '%s\t%s\n' before "$demo_before_status" after "$demo_after_status" > "$demo_output/expected-statuses.tsv"
demo_capture_scene before "$demo_before_status" "$demo_runtime/before" 'http://127.0.0.1:1' 'BEFORE' \
  "DEMO_MODE=$demo_mode" 'DEMO_SCENE=before' "DEMO_STATUS_LOG=$demo_output/statuses.tsv" \
  "DEMO_PYTHON=$demo_python" "DEMO_EDITOR_DRIVER=$demo_source/editor_driver.py" \
  "DEMO_EDITOR_LAYOUT=$demo_editor_before" "DEMO_EDITOR_REPORT=$demo_output/before-editor-input.json" \
  "DEMO_COLUMNS=$demo_columns" "DEMO_ROWS=$demo_rows" "DEMO_THEME=$demo_theme" "${demo_theme_environment[@]}"
demo_capture_scene after "$demo_after_status" "$demo_runtime/after" 'http://127.0.0.1:1' 'AFTER' \
  "DEMO_MODE=$demo_mode" 'DEMO_SCENE=after' "DEMO_STATUS_LOG=$demo_output/statuses.tsv" \
  "DEMO_PYTHON=$demo_python" "DEMO_EDITOR_DRIVER=$demo_source/editor_driver.py" \
  'DEMO_EDITOR_LAYOUT=options' "DEMO_EDITOR_REPORT=$demo_output/editor-input.json" "DEMO_COLUMNS=$demo_columns" "DEMO_ROWS=$demo_rows" \
  "DEMO_EDITOR_LINKED=$demo_editor_linked" "DEMO_EDITOR_PRESENTATION=$demo_editor_presentation" \
  "DEMO_THEME=$demo_theme" "${demo_theme_environment[@]}"
"$demo_python" "$demo_source/validate.py" "$demo_output" "$demo_mode" > "$demo_output/validation.txt"
if [ "$demo_interactive" = 1 ]; then
  demo_editor_scenes=(after)
  if [ "$demo_editor_before" != help ]; then demo_editor_scenes=(before after); fi
  for demo_scene in "${demo_editor_scenes[@]}"; do
    demo_snapshots="$demo_output/editor-snapshots.tsv"
    if [ "$demo_scene" = before ]; then demo_snapshots="$demo_output/before-editor-snapshots.tsv"; fi
    mv "$demo_output/$demo_scene.png" "$demo_output/$demo_scene-exit.png"
    while IFS=$'\t' read -r demo_state demo_time; do
      "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_time" \
        "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene-$demo_state.gif"
      "$demo_ffmpeg" -nostdin -hide_banner -loglevel error -y -i "$demo_output/$demo_scene-$demo_state.gif" \
        -frames:v 1 "$demo_output/$demo_scene-$demo_state.png"
    done < "$demo_snapshots"
    demo_states=(text ids bytes results details encoding controls)
    if [ "$demo_mode" = details ]; then demo_states=(source cursor up-navigation ordinary partial overflow-start overflow-end overflow-home recovery); fi
    for demo_state in "${demo_states[@]}"; do
      test -s "$demo_output/$demo_scene-$demo_state.png"
    done
    if [ "$demo_mode" = editor ] && { [ "$demo_scene" = after ] || [ "$demo_editor_before" = options ]; }; then
      for demo_state in view-choice tokenizer-choice; do
        test -s "$demo_output/$demo_scene-$demo_state.png"
      done
    fi
    if [ "$demo_mode" = editor ] && [ "$demo_scene" = after ] && [ "$demo_editor_linked" = 1 ]; then
      for demo_state in caret r50k p50k; do
        test -s "$demo_output/$demo_scene-$demo_state.png"
      done
    fi
    demo_cover=text
    if [ "$demo_mode" = details ]; then demo_cover=ordinary; fi
    cp "$demo_output/$demo_scene-$demo_cover.png" "$demo_output/$demo_scene.png"
  done
fi
demo_assemble_capture 300 before after
printf 'Recorded %s terminal replay in %s\n' "$demo_mode" "$demo_output"
