#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd -P)"
demo_root="${DEMO_REPO_ROOT:-$(cd "$demo_source/../../.." && pwd -P)}"
test -f "$demo_root/scripts/demos/capture_and_render.sh"
demo_python="$(command -v python3)"
source "$demo_root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/fixture.py"
test "$("$demo_asciinema" --version)" = 'asciinema 3.2.1'
test "$("$demo_agg" --version)" = 'agg 1.9.0'
demo_before_digest="$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
demo_after_digest="$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
demo_start_api "$demo_output/before-requests.jsonl"
mkdir -p "$demo_runtime/home" "$demo_runtime/cache" "$demo_runtime/config"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_FIXTURE_DIR" || exit 98
printf '\033[2J\033[H\033[?25h%s\n' 'Terminal replay | synthetic two-second audio | fake player'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
if [ "$DEMO_SCENE" = before ]; then
  demo_text=$'Hello from the audio workbench.\nThis clip is synthetic.'
  demo_instructions=$'Read clearly. Keep @literal and \\@literal unchanged.'
  printf '%s\n' '$ openai audio speak --model gpt-4o-mini-tts-2025-12-15 --voice ash --response-format wav --speed 1.25 --input "$text" --instructions "$instructions" --output result.wav'
  if openai audio speak --model gpt-4o-mini-tts-2025-12-15 --voice ash \
    --response-format wav --speed 1.25 --input "$demo_text" \
    --instructions "$demo_instructions" --output result.wav; then
    demo_status=0
  else
    demo_status=$?
  fi
else
  printf '%s\n' '$ openai audio speak'
  if "$DEMO_PYTHON" -I -B "$DEMO_DRIVER" --binary "$(command -v openai)" \
    --source "$DEMO_AFTER_SHA" --repo-root "$DEMO_REPO_ROOT" \
    --asciinema "$DEMO_ASCIINEMA" --case workflow --theme dark --emit --no-resize \
    --output "$DEMO_DRIVER_OUTPUT"; then
    demo_status=0
  else
    demo_status=$?
  fi
fi
printf '%s\t%s\n' "$DEMO_SCENE" "$demo_status" >> "$DEMO_STATUS_LOG" || exit 98
printf '\033[0m\033[?25h\n$ '
sleep 2
exit "$demo_status"
SCENE

{
  echo 'feature: audio workbench, private history, measured waveform and local playback'
  echo "before commit: $demo_before_sha"
  echo "after runtime commit: $demo_after_sha"
  echo 'fixture: mono PCM16, 24000 samples/second, 48000 frames, varying amplitude, exactly 2 seconds'
  echo 'before: existing explicit generation and save command'
  echo 'after: actual form input, choices, generation, measured result, save, history, audition, clear, cancellation'
  echo 'playback: fake FFplay consumes verified audio; this replay does not establish audible playback'
  echo 'capture: owned PTYs; isolated fake credentials; owned caches; no remote API or microphone'
  echo 'scope: terminal replay; no graphical Terminal, native Windows/Linux, or live API acceptance claim'
  echo 'render: asciinema 3.2.1, agg 1.9.0, 22px, dark palette'
  echo 'font chain: Menlo,Apple Symbols; Menlo text metrics with Apple Symbols glyph fallback'
  echo 'geometry: fixed 90x34 comparison; separate actual 40x12 compact PTY capture'
  echo 'resize coverage: separate ordinary workflow; agg 1.9.0 does not render resize events'
  demo_capture_metadata
  "$demo_python" --version
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_source/fixture.py" "$demo_source/check.py" "$demo_source/record.sh" \
  "$demo_root/scripts/image_picker_harness.py" "$demo_root/scripts/demos/capture_and_render.sh" \
  > "$demo_output/source-sha256.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
demo_window_size=90x34
demo_render_options=(--renderer resvg --font-family 'Menlo,Apple Symbols' --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
demo_capture_scene before 0 "$demo_runtime/before" "$demo_api_url" 'Before: explicit speech generation and file save' \
  DEMO_SCENE=before "DEMO_STATUS_LOG=$demo_output/statuses.tsv" "DEMO_FIXTURE_DIR=$demo_runtime" \
  "HOME=$demo_runtime/home" "XDG_CACHE_HOME=$demo_runtime/cache" "XDG_CONFIG_HOME=$demo_runtime/config" \
  OPENAI_PICKER_SHELL=off GOMAXPROCS=2 NO_COLOR=1 FORCE_COLOR=0
demo_capture_scene after 130 "$demo_runtime/after" "$demo_api_url" 'After: speech controls and retained audio results' \
  DEMO_SCENE=after "DEMO_STATUS_LOG=$demo_output/statuses.tsv" "DEMO_FIXTURE_DIR=$demo_runtime" \
  "DEMO_PYTHON=$demo_python" "DEMO_DRIVER=$demo_source/check.py" "DEMO_REPO_ROOT=$demo_root" \
  "DEMO_AFTER_SHA=$demo_after_sha" "DEMO_ASCIINEMA=$demo_asciinema" "DEMO_DRIVER_OUTPUT=$demo_output/after-harness" \
  "HOME=$demo_runtime/home" "XDG_CACHE_HOME=$demo_runtime/cache" "XDG_CONFIG_HOME=$demo_runtime/config" \
  OPENAI_PICKER_SHELL=off GOMAXPROCS=2 COLORTERM=truecolor 'COLORFGBG=15;0'
demo_stop_api
"$demo_python" -I -B "$demo_source/check.py" --binary "$demo_after" \
  --source "$demo_after_sha" --repo-root "$demo_root" --asciinema "$demo_asciinema" \
  --case compact --theme dark --output "$demo_output/compact-harness" &
demo_capture_pid=$!
if wait "$demo_capture_pid"; then demo_status=0; else demo_status=$?; fi
demo_capture_pid=""
echo "compact harness exit status: $demo_status (expected 0; CLI cancellation 130)" >> "$demo_output/metadata.txt"
test "$demo_status" -eq 0
test "$demo_before_digest" = "$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
test "$demo_after_digest" = "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
for demo_state in initial-form model-choices voice-choices format-choices selected-settings generated-result save-validation saved-result history voice-audition cleared-form; do
  "$demo_agg" --quiet "${demo_render_options[@]}" --select 100% \
    "$demo_output/after-harness/$demo_state.cast" "$demo_output/after-$demo_state-frame.gif"
  "$demo_ffmpeg" -nostdin -hide_banner -loglevel error -y \
    -i "$demo_output/after-$demo_state-frame.gif" -frames:v 1 "$demo_output/after-$demo_state.png"
done
"$demo_agg" --quiet "${demo_render_options[@]}" --select 100% \
  "$demo_output/compact-harness/compact-40x12.cast" "$demo_output/after-compact-40x12-frame.gif"
"$demo_ffmpeg" -nostdin -hide_banner -loglevel error -y \
  -i "$demo_output/after-compact-40x12-frame.gif" -frames:v 1 "$demo_output/after-compact-40x12.png"
demo_assemble_capture 200 before after
printf 'Recorded audio workbench terminal replay in %s\n' "$demo_output"
