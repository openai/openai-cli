#!/bin/bash
set -euo pipefail

if [ "$#" -ne 6 ]; then
  echo 'usage: record-command-shortcuts.sh MODE BEFORE_BINARY AFTER_BINARY BEFORE_SHA CANDIDATE_SHA OUTPUT_DIR' >&2
  echo 'MODE: audio, root (first 32 help lines), or transcribe' >&2
  exit 2
fi
demo_mode="$1"
case "$demo_mode" in audio|root|transcribe) ;; *) echo 'MODE must be audio, root, or transcribe.' >&2; exit 2;; esac
shift
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
demo_window_size=105x28
if [ "$demo_mode" = root ]; then demo_window_size=105x40; fi
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3)
demo_start_api "$demo_runtime/requests.txt"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY
printf '\033[2J\033[H%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.4
case "$DEMO_MODE" in
  audio)
    printf '$ openai audio --help\n'
    sleep 0.4
    if openai audio --help; then demo_status=0; else demo_status=$?; fi
    ;;
  root)
    printf '$ openai --help | sed -n '\''1,32p'\''\n'
    sleep 0.4
    if openai --help | sed -n '1,32p'; then demo_status=0; else demo_status=$?; fi
    ;;
  transcribe)
    printf '$ openai transcribe --help\n'
    sleep 0.4
    if openai transcribe --help; then demo_status=0; else demo_status=$?; fi
    ;;
  *) exit 2;;
esac
printf '\n$ '
sleep 3
exit "$demo_status"
SCENE
{
  echo 'feature: grouped actions and root shortcuts'
  echo "mode: $demo_mode"
  echo "before commit: $demo_before_sha (inspect build info for dirty state)"
  echo "candidate commit: $demo_after_sha (inspect build info for dirty state)"
  echo 'capture: actual binaries in isolated bash PTYs; no API key or live requests'
  echo 'scope: asciinema/agg terminal replay, not a graphical terminal inspection'
  echo "terminal dimensions: $demo_window_size"
  demo_capture_metadata
  shasum -a 256 "$demo_source/record-command-shortcuts.sh"
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
demo_before_status=0
demo_excerpt=''
case "$demo_mode" in
  audio) demo_command='openai audio --help';;
  root) demo_command="openai --help | sed -n '1,32p'"; demo_excerpt=' (first 32 help lines)';;
  transcribe) demo_command='openai transcribe --help'; demo_before_status=3;;
esac
echo "command: $demo_command" >> "$demo_output/metadata.txt"
demo_capture_scene before "$demo_before_status" "$demo_runtime/before" "$demo_api_url" \
  "Before: reviewed CLI$demo_excerpt" "DEMO_MODE=$demo_mode"
demo_capture_scene after 0 "$demo_runtime/after" "$demo_api_url" \
  "After: grouped actions and shortcuts$demo_excerpt" "DEMO_MODE=$demo_mode"
case "$demo_mode" in
  audio)
    /usr/bin/grep -Fq 'transcriptions' "$demo_output/before.txt"
    /usr/bin/grep -Fq 'transcribe' "$demo_output/after.txt"
    /usr/bin/grep -Fq 'translate' "$demo_output/after.txt"
    /usr/bin/grep -Fq 'speak' "$demo_output/after.txt"
    /usr/bin/grep -Fq 'voices' "$demo_output/after.txt"
    ;;
  root)
    if /usr/bin/grep -Fq 'SHORTCUTS' "$demo_output/before.txt"; then exit 1; fi
    /usr/bin/grep -Fq 'SHORTCUTS' "$demo_output/after.txt"
    /usr/bin/grep -Fq 'Shortcut for audio transcribe.' "$demo_output/after.txt"
    /usr/bin/grep -Fq 'projects' "$demo_output/after.txt"
    /usr/bin/grep -Fq 'GENERATE CONTENT' "$demo_output/after.txt"
    ;;
  transcribe)
    /usr/bin/grep -Fq 'Unknown help topic' "$demo_output/before.txt"
    /usr/bin/grep -Fq 'Shortcut for audio transcribe.' "$demo_output/after.txt"
    /usr/bin/grep -Fq -- '--file' "$demo_output/after.txt"
    /usr/bin/grep -Fq -- '--model' "$demo_output/after.txt"
    ;;
esac
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
test ! -s "$demo_output/requests.txt"
echo 'API requests: 0' >> "$demo_output/metadata.txt"
demo_assemble_capture 300 before after
