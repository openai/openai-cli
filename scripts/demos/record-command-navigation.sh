#!/bin/bash
set -euo pipefail

if [ "$#" -ne 6 ]; then
  echo 'usage: record-command-navigation.sh MODE BEFORE_BINARY AFTER_BINARY BEFORE_SHA CANDIDATE_SHA OUTPUT_DIR' >&2
  echo 'MODE: root (first 38 help lines) or projects (complete project group help)' >&2
  exit 2
fi
demo_mode="$1"
case "$demo_mode" in root|projects) ;; *) echo 'MODE must be root or projects.' >&2; exit 2;; esac
shift
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
demo_window_size=105x45
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
  root)
    printf '$ openai help --all | sed -n '\''1,38p'\''\n'
    sleep 0.4
    if openai help --all | sed -n '1,38p'; then demo_status=0; else demo_status=$?; fi
    ;;
  projects)
    printf '$ openai admin organization projects --help\n'
    sleep 0.4
    if openai admin organization projects --help; then demo_status=0; else demo_status=$?; fi
    ;;
  *) exit 2;;
esac
printf '\n$ '
sleep 3
exit "$demo_status"
SCENE
{
  echo 'feature: complete, described command navigation'
  echo "mode: $demo_mode"
  echo "before base commit: $demo_before_sha (inspect build info for dirty state)"
  echo "candidate commit: $demo_after_sha (inspect build info for dirty state)"
  echo 'capture: actual binaries in isolated bash PTYs; no API key or live requests'
  echo 'scope: asciinema/agg terminal replay, not a graphical terminal inspection'
  echo "terminal dimensions: $demo_window_size"
  demo_capture_metadata
  shasum -a 256 "$demo_source/record-command-navigation.sh"
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
case "$demo_mode" in
  root)
    echo "command: openai help --all | sed -n '1,38p'" >> "$demo_output/metadata.txt"
    demo_capture_scene before 0 "$demo_runtime/before" "$demo_api_url" \
      'Before: main branch (help excerpt, first 38 lines)' DEMO_MODE=root
    demo_capture_scene after 0 "$demo_runtime/after" "$demo_api_url" \
      'After: command sections (help excerpt, first 38 lines)' DEMO_MODE=root
    /usr/bin/grep -Fq 'API RESOURCE:' "$demo_output/before.txt"
    /usr/bin/grep -Fq 'GENERATE CONTENT' "$demo_output/after.txt"
    ;;
  projects)
    echo 'command: openai admin organization projects --help' >> "$demo_output/metadata.txt"
    demo_capture_scene before 0 "$demo_runtime/before" "$demo_api_url" \
      'Before: main branch project help' DEMO_MODE=projects
    demo_capture_scene after 0 "$demo_runtime/after" "$demo_api_url" \
      'After: all project actions and command groups' DEMO_MODE=projects
    /usr/bin/grep -Fq 'more in full help' "$demo_output/before.txt"
    /usr/bin/grep -Fq 'ACTIONS' "$demo_output/after.txt"
    /usr/bin/grep -Fq 'SPENDING' "$demo_output/after.txt"
    if /usr/bin/grep -Fq 'more in full help' "$demo_output/after.txt"; then exit 1; fi
    ;;
esac
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
test ! -s "$demo_output/requests.txt"
echo 'API requests: 0' >> "$demo_output/metadata.txt"
demo_assemble_capture 300 before after
