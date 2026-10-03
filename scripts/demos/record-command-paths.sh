#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-command-paths.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
# No fixture is started: both scenes only display the welcome page.
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" /usr/bin/true
mkdir "$demo_runtime/installed"
demo_invoked="$demo_runtime/installed/openai"
demo_window_size=130x24
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 15 --last-frame-duration 3)

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY
printf '\033[2J\033[H'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.4
printf '$ %q\n' "$DEMO_EXECUTABLE"
sleep 0.3
if "$DEMO_EXECUTABLE"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 3
exit "$demo_status"
SCENE

{
  echo 'feature: concise installed-command paths in welcome help'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'absolute invocation and PATH are identical; only the symlink target changes'
  echo 'data: help only, no API key or requests; no fixture is started'
  echo 'scope: Bash PTY terminal replay; not native graphical terminal validation'
  echo 'render: asciinema + agg, Menlo 18px, Dracula, 130x24, line height 1.2'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record-command-paths.sh"
} > "$demo_output/metadata.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
for demo_scene in before after; do
  demo_binary="$demo_before"
  demo_label=Before
  if [ "$demo_scene" = after ]; then demo_binary="$demo_after"; demo_label=After; fi
  ln -sf "$demo_binary" "$demo_invoked"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/installed" http://127.0.0.1:1 \
    "$demo_label" "DEMO_EXECUTABLE=$demo_invoked"
done
/usr/bin/grep -Fq "$demo_invoked help setup" "$demo_output/before.txt"
/usr/bin/grep -Eq '^ +openai help setup +Set up' "$demo_output/after.txt"
if /usr/bin/grep -Fq "$demo_invoked help" "$demo_output/after.txt"; then
  echo 'After still contains a full executable path in help.' >&2
  exit 1
fi
demo_assemble_capture 300 before after
printf 'Recorded command-path comparison in %s\n' "$demo_output"
