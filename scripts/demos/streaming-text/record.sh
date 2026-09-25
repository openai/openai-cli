#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo "usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR" >&2
  exit 2
fi
demo_before="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
demo_after="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
demo_before_sha="$3"
demo_after_sha="$4"
demo_output="$5"
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_api_binary="${DEMO_API_BINARY:-$demo_root/dist/demos/bin/streaming-demo-api}"
[[ "$demo_before_sha" =~ ^[0-9a-f]{40}$ ]]
[[ "$demo_after_sha" =~ ^[0-9a-f]{40}$ ]]
test -x "$demo_before"
test -x "$demo_after"
test -x "$demo_api_binary"
demo_asciinema="$(command -v asciinema)"
demo_agg="$(command -v agg)"
demo_ffmpeg="$(command -v ffmpeg)"
demo_ffprobe="$(command -v ffprobe)"
demo_python="$(command -v python3)"
test "$("$demo_asciinema" --version)" = 'asciinema 3.2.1'
test "$("$demo_agg" --version)" = 'agg 1.9.0'
mkdir -p "$demo_output"
demo_output="$(cd "$demo_output" && pwd)"
# A new evidence directory keeps an earlier reviewed capture recoverable.
test ! -e "$demo_output/metadata.txt"
test ! -e "$demo_output/requests.jsonl"
demo_runtime="$(mktemp -d "${TMPDIR:-/tmp}/cli-stream-demo.XXXXXX")"
demo_server_pid=""
cleanup_demo() {
  if [ -n "$demo_server_pid" ]; then
    kill "$demo_server_pid" 2>/dev/null || true
    wait "$demo_server_pid" 2>/dev/null || true
  fi
  rm -rf "$demo_runtime"
}
trap cleanup_demo EXIT
mkdir -p "$demo_runtime/before" "$demo_runtime/after" \
  "$demo_runtime/asciinema-config" "$demo_runtime/asciinema-state"
ln -s "$demo_before" "$demo_runtime/before/openai"
ln -s "$demo_after" "$demo_runtime/after/openai"
"$demo_api_binary" "$demo_runtime/address" "$demo_output/requests.jsonl" &
demo_server_pid=$!
for ((demo_attempt=0; demo_attempt<100; demo_attempt++)); do
  [ -s "$demo_runtime/address" ] && break
  sleep 0.05
done
test -s "$demo_runtime/address"
demo_api_url="$(cat "$demo_runtime/address")"
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
  echo "render: asciinema 3.2.1 + agg 1.9.0; Menlo 18px, Dracula, 108 columns x 40 rows, line height 1.2, 20 fps cap"
  echo "scope: terminal replay, no native Apple Terminal, PowerShell, or cmd.exe capture"
  echo "recipe: record.sh; comparison.tape is the alternative VHS recipe, not the renderer used"
  uname -sm
  if command -v sw_vers >/dev/null; then sw_vers; fi
  /bin/bash --version | sed -n '1p'
  "$demo_asciinema" --version
  "$demo_agg" --version
  "$demo_ffmpeg" -version | sed -n '1p'
  "$demo_python" --version
  shasum -a 256 "$demo_before" "$demo_after" "$demo_api_binary" \
    "$demo_asciinema" "$demo_agg" "$demo_source/record.sh" \
    "$demo_source/main.go" "$demo_source/events.json" "$demo_source/validate.py"
} > "$demo_output/metadata.txt"

demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after explicit-jsonl; do
  demo_command_dir="$demo_runtime/after"
  demo_format=default
  case "$demo_scene" in
    before) demo_command_dir="$demo_runtime/before"; demo_label='Before: main | delayed synthetic stream';;
    after) demo_label='After: streaming text | identical delayed events';;
    explicit-jsonl) demo_format=jsonl; demo_label='Explicit JSONL: every original event remains available';;
  esac
  if env -i PATH="$demo_command_dir:/usr/bin:/bin" LANG=en_US.UTF-8 \
    TERM=xterm-256color SHELL=/bin/bash FORCE_COLOR=0 NO_COLOR=1 \
    ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" \
    ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
    OPENAI_API_KEY=synthetic-demo-key OPENAI_BASE_URL="$demo_api_url" \
    DEMO_SCENE_SCRIPT="$demo_runtime/scene.sh" DEMO_SCENE_LABEL="$demo_label" \
    DEMO_FORMAT="$demo_format" \
    "$demo_asciinema" rec --headless --return --overwrite --quiet \
      --window-size 108x40 --capture-env SHELL,TERM --output-format asciicast-v2 \
      --title "$demo_label" --command '/bin/bash --noprofile --norc "$DEMO_SCENE_SCRIPT"' \
      "$demo_output/$demo_scene.cast"; then demo_status=0; else demo_status=$?; fi
  echo "$demo_scene exit status: $demo_status (expected 0)" >> "$demo_output/metadata.txt"
  test "$demo_status" -eq 0
  "$demo_agg" --quiet "${demo_render_options[@]}" \
    "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene.gif"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select 100% \
    "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene-frame.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y \
    -i "$demo_output/$demo_scene-frame.gif" -frames:v 1 "$demo_output/$demo_scene.png"
  ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" \
  ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
    "$demo_asciinema" convert --overwrite -f txt \
      "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene.txt"
done
"$demo_python" "$demo_source/validate.py" "$demo_output" "$demo_source/events.json" \
  > "$demo_output/validation.txt"
ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" \
ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
  "$demo_asciinema" cat "$demo_output/before.cast" "$demo_output/after.cast" \
    "$demo_output/explicit-jsonl.cast" > "$demo_output/comparison.cast"
# Join separately rendered scenes to avoid stale glyphs across screen clears.
(
  cd "$demo_output"
  printf "file '%s.gif'\n" before after explicit-jsonl > comparison-scenes.txt
  "$demo_ffmpeg" -hide_banner -loglevel error -y \
    -f concat -safe 1 -i comparison-scenes.txt \
    -filter_complex '[0:v]split[a][b];[a]palettegen[p];[b][p]paletteuse' \
    -vsync 0 -gifflags 0 -loop 0 -final_delay 200 comparison.gif
)
cat "$demo_output/before.txt" "$demo_output/after.txt" \
  "$demo_output/explicit-jsonl.txt" > "$demo_output/comparison.txt"
"$demo_ffprobe" -v error -select_streams v:0 \
  -show_entries stream=width,height,nb_frames,duration -of json \
  "$demo_output/comparison.gif" > "$demo_output/media.json"
printf 'Recorded streaming terminal replay in %s\n' "$demo_output"
