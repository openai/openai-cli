#!/bin/bash
# Shared lifecycle for demo recorders. Source from a caller using set -euo pipefail.
# Callers provide scene.sh, demo_window_size, demo_render_options and assertions.

demo_prepare_capture() {
  demo_root="$(cd "$1" && pwd -P)"
  demo_before="$(cd "$(dirname "$2")" && pwd -P)/$(basename "$2")"
  demo_after="$(cd "$(dirname "$3")" && pwd -P)/$(basename "$3")"
  demo_before_sha="$4"
  demo_after_sha="$5"
  demo_output="$6"
  demo_api_binary="$7"
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
  demo_output="$(cd "$demo_output" && pwd -P)"
  case "$demo_output/" in "$demo_root/"*) echo 'Keep recorded media outside the repository.' >&2; return 2;; esac
  # A fresh directory preserves previously reviewed recordings.
  if [ -n "$(ls -A "$demo_output")" ]; then
    echo 'Use an empty output directory for the recording.' >&2
    return 2
  fi
  demo_runtime="$(mktemp -d "${TMPDIR:-/tmp}/cli-demo.XXXXXX")"
  demo_server_pid=""
  demo_capture_pid=""
  trap demo_cleanup_capture EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  mkdir -p "$demo_runtime/before" "$demo_runtime/after" \
    "$demo_runtime/asciinema-config" "$demo_runtime/asciinema-state"
  ln -s "$demo_before" "$demo_runtime/before/openai"
  ln -s "$demo_after" "$demo_runtime/after/openai"
}

demo_cleanup_capture() {
  local demo_exit_status=$?
  trap - EXIT INT TERM
  if [ -n "$demo_capture_pid" ]; then
    kill "$demo_capture_pid" 2>/dev/null || true
    wait "$demo_capture_pid" 2>/dev/null || true
  fi
  if [ -n "$demo_server_pid" ]; then
    kill "$demo_server_pid" 2>/dev/null || true
    wait "$demo_server_pid" 2>/dev/null || true
  fi
  rm -rf "$demo_runtime"
  return "$demo_exit_status"
}

demo_start_api() {
  "$demo_api_binary" "$demo_runtime/address" "$@" &
  demo_server_pid=$!
  local demo_attempt
  for ((demo_attempt=0; demo_attempt<100; demo_attempt++)); do
    [ -s "$demo_runtime/address" ] && break
    if ! kill -0 "$demo_server_pid" 2>/dev/null; then
      wait "$demo_server_pid" || true
      echo 'Demo API exited before becoming ready.' >&2
      return 1
    fi
    sleep 0.05
  done
  test -s "$demo_runtime/address"
  demo_api_url="$(cat "$demo_runtime/address")"
}

demo_stop_api() {
  local demo_shutdown_failed=0
  kill "$demo_server_pid" 2>/dev/null || demo_shutdown_failed=1
  wait "$demo_server_pid" || demo_shutdown_failed=1
  demo_server_pid=""
  if [ "$demo_shutdown_failed" -ne 0 ]; then
    echo 'Demo API failed during shutdown; recording is incomplete.' >&2
    return 1
  fi
}

demo_capture_metadata() {
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
    "$demo_asciinema" "$demo_agg" "$demo_root/scripts/demos/capture_and_render.sh"
}

demo_capture_scene() {
  local demo_scene="$1" demo_expected_status="$2" demo_command_dir="$3"
  local demo_endpoint="$4" demo_label="$5" demo_status
  shift 5
  # Only explicit fixture/scene settings enter the PTY, never personal shell hooks or keys.
  env -i PATH="$demo_command_dir:/usr/bin:/bin" LANG=en_US.UTF-8 \
    TERM=xterm-256color SHELL=/bin/bash \
    ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" \
    ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
    OPENAI_API_KEY=synthetic-demo-key OPENAI_BASE_URL="$demo_endpoint" \
    DEMO_SCENE_SCRIPT="$demo_runtime/scene.sh" DEMO_SCENE_LABEL="$demo_label" "$@" \
    "$demo_asciinema" rec --headless --return --overwrite --quiet \
      --window-size "$demo_window_size" --capture-env SHELL,TERM --output-format asciicast-v2 \
      --title "$demo_label" --command '/bin/bash --noprofile --norc "$DEMO_SCENE_SCRIPT"' \
      "$demo_output/$demo_scene.cast" &
  demo_capture_pid=$!
  # wait is interruptible; a foreground capture would defer the shell's traps.
  if wait "$demo_capture_pid"; then demo_status=0; else demo_status=$?; fi
  demo_capture_pid=""
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
}

demo_assemble_capture() {
  local demo_final_delay="$1" demo_scene
  shift
  local demo_casts=() demo_transcripts=()
  for demo_scene in "$@"; do
    demo_casts+=("$demo_output/$demo_scene.cast")
    demo_transcripts+=("$demo_output/$demo_scene.txt")
  done
  ASCIINEMA_CONFIG_HOME="$demo_runtime/asciinema-config" \
  ASCIINEMA_STATE_HOME="$demo_runtime/asciinema-state" \
    "$demo_asciinema" cat "${demo_casts[@]}" > "$demo_output/comparison.cast"
  # Joining separately rendered scenes avoids agg retaining glyphs across screen clears.
  (
    cd "$demo_output"
    printf "file '%s.gif'\n" "$@" > comparison-scenes.txt
    "$demo_ffmpeg" -hide_banner -loglevel error -y \
      -f concat -safe 1 -i comparison-scenes.txt \
      -filter_complex '[0:v]split[a][b];[a]palettegen[p];[b][p]paletteuse' \
      -vsync 0 -gifflags 0 -loop 0 -final_delay "$demo_final_delay" comparison.gif
  )
  # Preserve earlier scenes even when later scenes clear the terminal screen.
  cat "${demo_transcripts[@]}" > "$demo_output/comparison.txt"
  "$demo_ffprobe" -v error -select_streams v:0 \
    -show_entries stream=width,height,nb_frames,duration -of json \
    "$demo_output/comparison.gif" > "$demo_output/media.json"
}
