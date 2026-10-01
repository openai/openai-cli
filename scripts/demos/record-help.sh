#!/bin/bash
set -euo pipefail

if [ "$#" -lt 5 ]; then
  echo 'usage: record-help.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR [--all] [COMMAND ...]' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
shift 5
demo_args=()
if [ "${1:-}" = --all ]; then
  demo_args=(help --all)
  shift
fi
for demo_part in "$@"; do
  # Accept command paths only: no request options, positional data or shell code.
  [[ "$demo_part" =~ ^[a-z][a-z0-9:-]*$ ]] || { echo 'Expected a command name.' >&2; exit 2; }
done
if [ "${#demo_args[@]}" -eq 0 ]; then demo_args=("$@" --help); else demo_args+=("$@"); fi
demo_window_size="90x${DEMO_ROWS:-48}"
[[ "${DEMO_ROWS:-48}" =~ ^[1-9][0-9]*$ ]] || { echo 'DEMO_ROWS must be positive.' >&2; exit 2; }
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 4)
demo_start_api "$demo_runtime/requests.txt"
{
  cat <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY
printf '\033[2J\033[H'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.4
SCENE
  # Bash quoting preserves the same argument array in both scenes, without eval.
  printf 'demo_args=('
  printf ' %q' "${demo_args[@]}"
  printf ' )\n'
  cat <<'SCENE'
printf '$ openai'
printf ' %s' "${demo_args[@]}"
printf '\n'
sleep 0.3
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 4
exit "$demo_status"
SCENE
} > "$demo_runtime/scene.sh"
{
  echo 'feature: CLI help comparison'
  echo "comparison parent commit: $demo_before_sha"
  echo "proposed commit: $demo_after_sha"
  printf 'command: openai'; printf ' %s' "${demo_args[@]}"; printf '\n'
  echo 'data: no API key; loopback fixture rejects requests and records any unexpected access'
  echo 'capture: real binaries in an isolated bash PTY; no personal shell hooks or environment'
  echo "render: asciinema + agg, Menlo 18px, Dracula, $demo_window_size, line height 1.2"
  echo 'scope: terminal replay; not native Apple Terminal, Windows or graphical validation'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record-help.sh" "$demo_source/image-models/main.go"
} > "$demo_output/metadata.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
for demo_scene in before after; do
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" \
    "$demo_api_url/help-only/v1" "$demo_scene: $([ "$demo_scene" = before ] && echo 'parent' || echo 'proposed change')"
  /usr/bin/grep -Fq 'openai' "$demo_output/$demo_scene.txt"
done
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
test ! -s "$demo_output/requests.txt"
echo 'API requests: 0 (required)' >> "$demo_output/metadata.txt"
demo_assemble_capture 400 before after
printf 'Recorded help comparison in %s\n' "$demo_output"
