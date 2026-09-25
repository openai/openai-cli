#!/bin/bash
set -euo pipefail

if [ "$#" -ne 6 ]; then
  echo "usage: record.sh FEATURE BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR" >&2
  exit 2
fi
demo_feature="$1"
demo_before="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
demo_after="$(cd "$(dirname "$3")" && pwd)/$(basename "$3")"
demo_before_sha="$4"
demo_after_sha="$5"
demo_output="$6"
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
demo_api_binary="${DEMO_API_BINARY:-$demo_root/dist/demos/bin/demo-api}"
case "$demo_feature" in readable-output|helpful-errors|list-get-results) ;; *) exit 2;; esac
[[ "$demo_before_sha" =~ ^[0-9a-f]{40}$ ]]
[[ "$demo_after_sha" =~ ^[0-9a-f]{40}$ ]]
test -x "$demo_before"
test -x "$demo_after"
test -x "$demo_api_binary"
demo_asciinema="$(command -v asciinema)"
demo_agg="$(command -v agg)"
demo_ffmpeg="$(command -v ffmpeg)"
demo_ffprobe="$(command -v ffprobe)"
mkdir -p "$demo_output"
demo_output="$(cd "$demo_output" && pwd)"
demo_runtime="$(mktemp -d "${TMPDIR:-/tmp}/cli-demo.XXXXXX")"
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
"$demo_api_binary" "$demo_runtime/address" &
demo_server_pid=$!
for ((demo_attempt=0; demo_attempt<100; demo_attempt++)); do
  [ -s "$demo_runtime/address" ] && break
  sleep 0.05
done
test -s "$demo_runtime/address"
demo_api_url="$(cat "$demo_runtime/address")"
# Retain the original VHS recipe for later use in a permitted environment.
cp "$demo_source/$demo_feature.tape" "$demo_output/comparison.tape"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | asciinema + agg'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.5
case "$DEMO_REQUEST" in
  files-list) demo_args=(files list --max-items 2);;
  files-retrieve) demo_args=(files retrieve file_training);;
  *) demo_args=(models retrieve --model "$DEMO_MODEL");;
esac
case "$DEMO_FORMAT" in
  json) demo_args=(--format json "${demo_args[@]}");;
  error-json) demo_args=(--format-error json "${demo_args[@]}");;
esac
printf '$ openai'
printf ' %s' "${demo_args[@]}"
printf '\n'
sleep 0.4
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 3.5
exit "$demo_status"
SCENE

demo_scenes=(before after explicit-json)
demo_request=model
if [ "$demo_feature" = readable-output ]; then
  demo_model=demo-chat
  demo_expected_status=0
  demo_before_label='before: main | synthetic local response'
  demo_after_label='after: readable output | same synthetic response'
  demo_json_format=json
elif [ "$demo_feature" = helpful-errors ]; then
  demo_model=demo-auth
  demo_expected_status=1
  demo_before_label='before: readable-output parent | synthetic 401 error'
  demo_after_label='after: helpful errors | same synthetic 401 error'
  demo_json_format=error-json
else
  demo_model=""
  demo_request=files-list
  demo_expected_status=0
  demo_before_label='Before: main | two synthetic files'
  demo_after_label='After: list/get summaries | same two files'
  demo_json_format=json
  demo_scenes=(before after before-retrieve after-retrieve explicit-json)
fi
{
  echo "feature: $demo_feature"
  echo "comparison base / captured before commit: $demo_before_sha"
  echo "proposed feature / captured after commit: $demo_after_sha"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo "data: fixed synthetic responses from local demo-api; fake key; no live OpenAI requests"
  echo "capture: real PTY, stdin/stdout/stderr verified as terminals, bash --noprofile --norc"
  echo "render: each scene replayed with asciinema + agg swash, then full GIF frames joined by ffmpeg; Menlo 22px, Dracula, 90 columns x 24 rows, line height 1.2, maximum 20 fps"
  echo "scope: no native Apple Terminal, PowerShell, or cmd.exe capture"
  echo "recipe: record.sh; comparison.tape is the preserved VHS recipe, not the renderer used"
  uname -sm
  if command -v sw_vers >/dev/null; then sw_vers; fi
  /bin/bash --version | sed -n '1p'
  echo "asciinema path: $demo_asciinema"
  "$demo_asciinema" --version
  echo "agg path: $demo_agg"
  "$demo_agg" --version
  echo "ffmpeg path: $demo_ffmpeg"
  "$demo_ffmpeg" -version | sed -n '1p'
  shasum -a 256 "$demo_before" "$demo_after" "$demo_api_binary" \
    "$demo_asciinema" "$demo_agg" "$demo_source/record.sh" "$demo_source/main.go"
} > "$demo_output/metadata.txt"

demo_render_options=(--font-family Menlo --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3.5)
for demo_scene in "${demo_scenes[@]}"; do
  demo_command_dir="$demo_runtime/after"
  demo_format=default
  demo_scene_request="$demo_request"
  case "$demo_scene" in
    before) demo_command_dir="$demo_runtime/before"; demo_label="$demo_before_label";;
    after) demo_label="$demo_after_label";;
    before-retrieve) demo_command_dir="$demo_runtime/before"; demo_scene_request=files-retrieve; demo_label='Before: main | retrieve the first file';;
    after-retrieve) demo_scene_request=files-retrieve; demo_label='After: list/get summaries | same file';;
    explicit-json) demo_format="$demo_json_format"; demo_label='explicit JSON | same synthetic API response';;
  esac
  if [ "$demo_feature" = list-get-results ] && [ "$demo_scene" = explicit-json ]; then
    demo_scene_request=files-retrieve
    demo_label='Explicit JSON | full data for the same file'
  fi
  # The CLI runs directly on the PTY. No personal environment or shell hooks are inherited.
  if env -i PATH="$demo_command_dir:/usr/bin:/bin" LANG=en_US.UTF-8 \
    TERM=xterm-256color SHELL=/bin/bash \
    ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" \
    ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
    OPENAI_API_KEY=synthetic-demo-key OPENAI_BASE_URL="$demo_api_url" \
    DEMO_SCENE_SCRIPT="$demo_runtime/scene.sh" DEMO_SCENE_LABEL="$demo_label" \
    DEMO_MODEL="$demo_model" DEMO_FORMAT="$demo_format" DEMO_REQUEST="$demo_scene_request" \
    "$demo_asciinema" rec --headless --return --overwrite --quiet \
      --window-size 90x24 --capture-env SHELL,TERM --output-format asciicast-v2 \
      --title "$demo_label" --command '/bin/bash --noprofile --norc "$DEMO_SCENE_SCRIPT"' \
      "$demo_output/$demo_scene.cast"; then demo_status=0; else demo_status=$?; fi
  echo "$demo_scene exit status: $demo_status (expected $demo_expected_status)" >> "$demo_output/metadata.txt"
  test "$demo_status" -eq "$demo_expected_status"
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
  demo_expected_text=demo-project
  if [ "$demo_feature" = helpful-errors ]; then
    demo_expected_text=invalid_api_key
    [ "$demo_scene" != after ] || demo_expected_text='help setup'
  elif [ "$demo_feature" = list-get-results ]; then
    demo_expected_text=file_training
  fi
  if ! /usr/bin/grep -Fq "$demo_expected_text" "$demo_output/$demo_scene.txt"; then
    echo "Unexpected $demo_scene output: missing $demo_expected_text" >&2
    exit 1
  fi
  if [ "$demo_feature" = list-get-results ]; then
    case "$demo_scene" in
      before|after) /usr/bin/grep -Fq file_reference "$demo_output/$demo_scene.txt";;
    esac
    case "$demo_scene" in
      before|before-retrieve) /usr/bin/grep -Fq 'Created at: 1704067200' "$demo_output/$demo_scene.txt";;
      after|after-retrieve)
        /usr/bin/grep -Fq 'Summary; use --format json for full data.' "$demo_output/$demo_scene.txt"
        if /usr/bin/grep -Fq 'Created at:' "$demo_output/$demo_scene.txt"; then
          echo "Unexpected $demo_scene output: creation timestamp was not summarized" >&2
          exit 1
        fi
        ;;
      explicit-json) /usr/bin/grep -Fq '"created_at": 1704067200' "$demo_output/$demo_scene.txt";;
    esac
  fi
done
demo_casts=()
demo_transcripts=()
for demo_scene in "${demo_scenes[@]}"; do
  demo_casts+=("$demo_output/$demo_scene.cast")
  demo_transcripts+=("$demo_output/$demo_scene.txt")
done
ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" \
ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
  "$demo_asciinema" cat "${demo_casts[@]}" > "$demo_output/comparison.cast"
# Rendering a merged cast can retain partial glyphs across screen clears in
# agg 1.9.0. Join the independently rendered replays, retaining their timing.
# Fixed relative names also avoid escaping output paths in ffmpeg's file list.
(
  cd "$demo_output"
  printf "file '%s.gif'\n" "${demo_scenes[@]}" > comparison-scenes.txt
  "$demo_ffmpeg" -hide_banner -loglevel error -y \
    -f concat -safe 1 -i comparison-scenes.txt \
    -filter_complex '[0:v]split[a][b];[a]palettegen[p];[b][p]paletteuse' \
    -vsync 0 -gifflags 0 -loop 0 -final_delay 350 comparison.gif
)
# Convert each scene separately so clearing the screen between scenes does not
# erase earlier scenes from the combined plain-text transcript.
cat "${demo_transcripts[@]}" > "$demo_output/comparison.txt"
"$demo_ffprobe" -v error -select_streams v:0 \
  -show_entries stream=width,height,nb_frames,duration -of json \
  "$demo_output/comparison.gif" > "$demo_output/media.json"
printf 'Recorded %s terminal replay in %s\n' "$demo_feature" "$demo_output"
