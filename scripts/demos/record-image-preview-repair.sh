#!/bin/bash
# Real PTY command/help comparison. This does not simulate native Apple Terminal.
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record-image-preview-repair.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
TMPDIR=/tmp demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/demo-api}"
demo_python="$(command -v python3)"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Real PTY | local commands only | no native Apple Terminal validation'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.3
printf '$ openai images inline status\n'
if openai images inline status; then demo_status=0; else demo_status=$?; fi
printf '[exit %s]\n' "$demo_status"
if [ "$DEMO_STAGE" = before ]; then
  test "$demo_status" -ne 0 || exit 98
else
  test "$demo_status" -eq 0 || exit 98
  printf '\n$ openai images inline repair --help\n'
  openai images inline repair --help || exit 98
fi
printf '\n$ '
sleep 2
SCENE
{
  echo 'feature: explicit preview setup, repair and read-only status'
  echo "before: $demo_before_sha"
  echo "after: $demo_after_sha"
  echo 'capture: real PTY, isolated HOME; TERM=xterm-256color; bash without startup files'
  echo 'data: local commands, synthetic key, unused loopback endpoint; no API server started'
  echo 'scope: command availability/help and unsupported-terminal guidance only'
  echo 'limit: native Apple Terminal activation and appearance are not visually verified'
  demo_capture_metadata
} > "$demo_output/metadata.txt"
demo_window_size=108x38
demo_render_options=(--renderer resvg --font-family Menlo --font-size 20 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after; do
  demo_label='Before: no explicit setup, repair or status commands'
  [ "$demo_scene" = before ] || demo_label='After: read-only status and documented recovery commands'
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" \
    'http://127.0.0.1:1/v1' "$demo_label" HOME="$demo_runtime/$demo_scene" "DEMO_STAGE=$demo_scene"
done
"$demo_python" - "$demo_output" "$demo_runtime" <<'VALIDATE' > "$demo_output/validation.txt"
import pathlib,sys
output,runtime=map(pathlib.Path,sys.argv[1:])
before=(output/'before.txt').read_text();after=(output/'after.txt').read_text()
assert 'Unknown help topic' in before
assert 'Sharp preview setup is unavailable here.' in after
assert 'Automation permission' in after
assert '--inline on' in after
assert 'No cache reset or removal of committed previews' in after
for stage in ('before','after'):
 assert not (runtime/stage/'Library/Caches/openai').exists()
 assert not (runtime/stage/'.cache/openai').exists()
 assert not (runtime/stage/'Downloads').exists()
print('PASS: before lacks commands; after reports unsupported host and explains repair')
print('PASS: no preview cache, saved images or native activation from these scenes')
print('LIMIT: this PTY replay does not verify native Apple Terminal appearance')
VALIDATE
demo_assemble_capture 200 before after
printf 'Recorded image preview repair commands in %s\n' "$demo_output"
