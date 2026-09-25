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
demo_api_binary="${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
[[ "$demo_before_sha" =~ ^[0-9a-f]{40}$ ]]
[[ "$demo_after_sha" =~ ^[0-9a-f]{40}$ ]]
test -x "$demo_before" && test -x "$demo_after" && test -x "$demo_api_binary"
demo_asciinema="$(command -v asciinema)"
demo_agg="$(command -v agg)"
demo_ffmpeg="$(command -v ffmpeg)"
demo_ffprobe="$(command -v ffprobe)"
mkdir -p "$demo_output"
demo_output="$(cd "$demo_output" && pwd)"
case "$demo_output/" in "$demo_root/"*) echo 'Keep recorded media outside the repository.' >&2; exit 2;; esac
demo_runtime="$(mktemp -d "${TMPDIR:-/tmp}/image-model-demo.XXXXXX")"
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
"$demo_api_binary" "$demo_runtime/address" "$demo_runtime/requests.txt" &
demo_server_pid=$!
for ((demo_attempt=0; demo_attempt<100; demo_attempt++)); do
  [ -s "$demo_runtime/address" ] && break
  sleep 0.05
done
test -s "$demo_runtime/address"
demo_api_url="$(cat "$demo_runtime/address")"
cp "$demo_source/image-models.tape" "$demo_output/comparison.tape"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | asciinema + agg | synthetic metadata only'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.5
demo_args=(images models --all)
if [ "$DEMO_SCENE" = offline ]; then
  unset OPENAI_API_KEY
  demo_args=(images models --offline)
fi
printf '$ openai'
printf ' %s' "${demo_args[@]}"
printf '\n'
sleep 0.4
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n[exit %s]\n$ ' "$demo_status"
sleep 4
exit "$demo_status"
SCENE

{
  echo 'feature: exact image model names'
  echo "comparison base / captured before commit: $demo_before_sha"
  echo "proposed feature / captured after commit: $demo_after_sha"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo 'data: fixed loopback metadata; synthetic key; no image generation or live requests'
  echo 'capture: real PTY, stdin/stdout/stderr verified as terminals, bash --noprofile --norc'
  echo 'render: asciinema + agg, Menlo 22px, Dracula, 110 columns x 34 rows, line height 1.2, maximum 20 fps'
  echo 'scope: terminal replay; not native Apple Terminal, PowerShell, or cmd.exe validation'
  echo 'comparison.gif includes before and after; offline and partial are supplemental scenes'
  echo 'recipe: record.sh; comparison.tape is a retained VHS recipe, not the renderer used'
  uname -sm
  if command -v sw_vers >/dev/null; then sw_vers; fi
  /bin/bash --version | sed -n '1p'
  "$demo_asciinema" --version
  "$demo_agg" --version
  "$demo_ffmpeg" -version | sed -n '1p'
  shasum -a 256 "$demo_before" "$demo_after" "$demo_api_binary" "$demo_asciinema" "$demo_agg" \
    "$demo_source/record.sh" "$demo_source/main.go" "$demo_source/image-models.tape"
} > "$demo_output/metadata.txt"

demo_render_options=(--font-family Menlo --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 4)
for demo_scene in before after offline partial; do
  demo_command_dir="$demo_runtime/after"
  demo_endpoint="$demo_api_url/normal/v1"
  demo_expected_status=0
  demo_expected_requests=12
  case "$demo_scene" in
    before)
      demo_command_dir="$demo_runtime/before"
      demo_label='Before: main | image model discovery is not available'
      demo_expected_status=1; demo_expected_requests=0;;
    after) demo_label='After: exact names, visibility, retirement and dated versions';;
    offline)
      demo_label='Offline: known model names | no API key and zero requests'
      demo_expected_requests=0;;
    partial)
      demo_label='Partial results: one synthetic 503 | completed checks are kept'
      demo_endpoint="$demo_api_url/partial/v1"; demo_expected_status=1;;
  esac
  demo_requests_before="$(wc -l < "$demo_runtime/requests.txt")"
  if env -i PATH="$demo_command_dir:/usr/bin:/bin" LANG=en_US.UTF-8 \
    TERM=xterm-256color SHELL=/bin/bash \
    ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" \
    ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
    OPENAI_API_KEY=synthetic-demo-key OPENAI_BASE_URL="$demo_endpoint" \
    DEMO_SCENE_SCRIPT="$demo_runtime/scene.sh" DEMO_SCENE_LABEL="$demo_label" DEMO_SCENE="$demo_scene" \
    "$demo_asciinema" rec --headless --return --overwrite --quiet \
      --window-size 110x34 --capture-env SHELL,TERM --output-format asciicast-v2 \
      --title "$demo_label" --command '/bin/bash --noprofile --norc "$DEMO_SCENE_SCRIPT"' \
      "$demo_output/$demo_scene.cast"; then demo_status=0; else demo_status=$?; fi
  demo_requests_after="$(wc -l < "$demo_runtime/requests.txt")"
  demo_request_count=$((demo_requests_after - demo_requests_before))
  echo "$demo_scene exit status: $demo_status (expected $demo_expected_status); metadata requests: $demo_request_count (expected $demo_expected_requests)" >> "$demo_output/metadata.txt"
  test "$demo_status" -eq "$demo_expected_status"
  test "$demo_request_count" -eq "$demo_expected_requests"
  "$demo_agg" --quiet "${demo_render_options[@]}" "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene.gif"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select 100% \
    "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene-frame.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_scene-frame.gif" \
    -frames:v 1 "$demo_output/$demo_scene.png"
  ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
    "$demo_asciinema" convert --overwrite -f txt "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene.txt"
  case "$demo_scene" in
    before) demo_expected_text='models';;
    after) demo_expected_text='Retired 2000-01-01';;
    offline) demo_expected_text='Not checked';;
    partial) demo_expected_text='Could not check';;
  esac
  if ! /usr/bin/grep -Fq "$demo_expected_text" "$demo_output/$demo_scene.txt"; then
    echo "Unexpected $demo_scene output: missing $demo_expected_text" >&2
    exit 1
  fi
done
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
  "$demo_asciinema" cat "$demo_output/before.cast" "$demo_output/after.cast" > "$demo_output/comparison.cast"
# Join separately rendered GIFs to avoid agg retaining partial glyphs across clears.
(
  cd "$demo_output"
  printf "file '%s.gif'\n" before after > comparison-scenes.txt
  "$demo_ffmpeg" -hide_banner -loglevel error -y -f concat -safe 1 -i comparison-scenes.txt \
    -filter_complex '[0:v]split[a][b];[a]palettegen[p];[b][p]paletteuse' \
    -vsync 0 -gifflags 0 -loop 0 -final_delay 400 comparison.gif
)
cat "$demo_output/before.txt" "$demo_output/after.txt" > "$demo_output/comparison.txt"
"$demo_ffprobe" -v error -select_streams v:0 -show_entries stream=width,height,nb_frames,duration \
  -of json "$demo_output/comparison.gif" > "$demo_output/media.json"
printf 'Recorded image-model terminal replays in %s\n' "$demo_output"
