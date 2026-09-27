#!/bin/bash
# Local-file preview and preference demo; no API requests or font activation.
set -euo pipefail
if [ "$#" -ne 6 ]; then
  echo 'usage: record-saved-image-previews.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR PNG_FIXTURE' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
demo_fixture="$(cd "$(dirname "$6")" && pwd)/$(basename "$6")"
test -f "$demo_fixture"
source "$demo_source/capture_and_render.sh"
TMPDIR=/tmp demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/demo-api}"
demo_python="$(command -v python3)"
mkdir -p "$demo_runtime/home" "$demo_runtime/files"
cp "$demo_fixture" "$demo_runtime/files/robot.png"
: > "$demo_output/requests.jsonl"
DEMO_IMAGE_REQUEST_LOG="$demo_output/requests.jsonl" demo_start_api
cp "$demo_source/saved-image-previews.tape" "$demo_output/comparison.tape"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_FILES" || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | local PNG | no API key or requests'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.3
case "$DEMO_SCENE" in
  after)
    printf '$ openai images inline off\n'
    openai images inline off || exit $?
    printf '\n'
    ;;
  resized)
    # Change the actual PTY geometry. Existing rows do not magically reflow.
    stty cols 40 rows 38 || exit $?
    ;;
  preference)
    printf '$ openai images inline on\n'
    openai images inline on || exit $?
    printf '\n$ '
    sleep 2
    exit 0
    ;;
esac
printf '$ openai images preview robot.png\n'
sleep 0.3
if openai images preview robot.png; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 2
exit "$demo_status"
SCENE
{
  echo 'feature: saved-image previews and persistent automatic-preview preference'
  echo "before: $demo_before_sha"
  echo "after: $demo_after_sha"
  echo 'data: same synthetic PNG as inline-image demo; copied local file'
  echo 'capture: real PTY; isolated HOME; TERM=xterm-256color; no API key'
  echo 'scope: color-block fallback and local commands; no native-font visual claim'
  echo 'resize: new preview fits changed geometry; earlier scrollback is not reflowed'
  demo_capture_metadata
} > "$demo_output/metadata.txt"
demo_window_size=94x38
demo_render_options=(--renderer resvg --font-family Menlo --font-size 20 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after resized preference; do
  demo_command_dir="$demo_runtime/after"
  demo_expected_status=0
  case "$demo_scene" in
    before) demo_command_dir="$demo_runtime/before"; demo_expected_status=3; demo_label='Before: no command to redisplay a saved file';;
    after) demo_label='After: local preview still works with automatic previews off';;
    resized) demo_label='Redisplay: same PNG, terminal narrowed to 40 columns';;
    preference) demo_label='Remember the setting for future generation, edits and variations';;
  esac
  demo_capture_scene "$demo_scene" "$demo_expected_status" "$demo_command_dir" "$demo_api_url" "$demo_label" \
    OPENAI_API_KEY= HOME="$demo_runtime/home" USERPROFILE="$demo_runtime/home" \
    APPDATA="$demo_runtime/home" XDG_CONFIG_HOME="$demo_runtime/home" \
    DEMO_SCENE="$demo_scene" DEMO_FILES="$demo_runtime/files"
done
demo_stop_api
"$demo_python" - "$demo_output" "$demo_runtime" "$demo_fixture" <<'VALIDATE' > "$demo_output/validation.txt"
import hashlib,json,pathlib,re,shutil,sys
output,runtime,source=map(pathlib.Path,sys.argv[1:])
assert (output/'requests.jsonl').read_bytes()==b'', 'local commands contacted the API'
assert source.read_bytes()==(runtime/'files/robot.png').read_bytes(), 'original image changed'
text={scene:(output/(scene+'.txt')).read_text() for scene in ('before','after','resized','preference')}
assert 'Unknown help topic' in text['before'] and '▀' not in text['before']
assert 'Automatic image previews off' in text['after'] and 'Inline preview (color approximation):' in text['after']
assert 'Automatic image previews on' in text['preference'] and 'image-font activation' in text['preference']
rows=lambda scene:re.findall(r'▀+',text[scene])
assert rows('after') and all(len(row)==64 for row in rows('after'))
assert rows('resized') and all(len(row)==39 for row in rows('resized'))
settings=list((runtime/'home').rglob('image-preferences.json'))
assert len(settings)==1 and json.loads(settings[0].read_text())=={'version':1,'inline':True}
shutil.copyfile(runtime/'files/robot.png',output/'original-robot.png')
print('PASS: zero requests with no API key; source PNG unchanged')
print('PASS: automatic off does not disable local preview; on persists explicit font permission')
print('PASS: redisplay changes from 64 to 39 columns after narrowing the actual PTY')
print('SHA256:',hashlib.sha256(source.read_bytes()).hexdigest())
VALIDATE
demo_assemble_capture 200 before after resized preference
printf 'Recorded saved-image preview demo in %s\n' "$demo_output"
