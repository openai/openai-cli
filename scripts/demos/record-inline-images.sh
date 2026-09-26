#!/bin/bash
# Real PTY demo of the colored-character fallback, not native terminal graphics.
# Build scripts/demos/main.go first, then supply independently built CLI binaries.
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record-inline-images.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
TMPDIR=/tmp demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/demo-api}"
demo_python="$(command -v python3)"
mkdir -p "$demo_runtime/b" "$demo_runtime/a" "$demo_runtime/o"
DEMO_IMAGE_PREVIEW_FIXTURE=robot DEMO_IMAGE_REQUEST_LOG="$demo_output/requests.jsonl" demo_start_api
cp "$demo_source/inline-images.tape" "$demo_output/comparison.tape"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | synthetic API | colored-character fallback'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.3
demo_args=(images generate --prompt 'A tiny orange robot')
printf '$ openai images generate --prompt "A tiny orange robot"'
if [ "$DEMO_INLINE" = off ]; then
  demo_args+=(--inline off)
  printf ' --inline off'
fi
printf '\n'
sleep 0.3
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 2
exit "$demo_status"
SCENE
{
  echo 'feature: show saved images in the terminal'
  echo "before: $demo_before_sha"
  echo "after: $demo_after_sha"
  echo 'data: fixed locally drawn 64x40 robot; loopback API and fake key only'
  echo 'capture: real PTY, isolated HOME; TERM=xterm-256color; bash without startup files'
  echo 'scope: ANSI colored-character fallback, not native Kitty/iTerm/Apple font validation'
  echo 'retention: temporary output folders are cleaned; unchanged PNGs retained below'
  demo_capture_metadata
} > "$demo_output/metadata.txt"
demo_window_size=94x32
demo_render_options=(--renderer resvg --font-family Menlo --font-size 20 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after disabled; do
  demo_command_dir="$demo_runtime/after"
  demo_mode=auto
  case "$demo_scene" in
    before) demo_command_dir="$demo_runtime/before"; demo_home="$demo_runtime/b"; demo_label='Before: saves the image and prints its path';;
    after) demo_home="$demo_runtime/a"; demo_label='After: saves the same image, then shows a preview';;
    disabled) demo_home="$demo_runtime/o"; demo_label='Override: --inline off keeps saving without a preview'; demo_mode=off;;
  esac
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_api_url" "$demo_label" \
    HOME="$demo_home" "DEMO_INLINE=$demo_mode"
done
demo_stop_api
"$demo_python" - "$demo_output" "$demo_runtime" <<'VALIDATE' > "$demo_output/validation.txt"
import hashlib,json,pathlib,shutil,sys
output,runtime=map(pathlib.Path,sys.argv[1:])
requests=[json.loads(x) for x in (output/'requests.jsonl').read_text().splitlines()]
assert len(requests)==3 and requests[0]==requests[1]==requests[2]
assert 'inline' not in requests[0]
files=[runtime/d/'Downloads/gpt-images/tiny-orange-robot.png' for d in ('b','a','o')]
data=[p.read_bytes() for p in files]
assert data[0]==data[1]==data[2] and data[0].startswith(b'\x89PNG\r\n\x1a\n')
assert int.from_bytes(data[0][16:20],'big')==64 and int.from_bytes(data[0][20:24],'big')==40
for scene,visible in [('before',False),('after',True),('disabled',False)]:
 text=(output/(scene+'.txt')).read_text()
 assert 'Saved image:' in text
 assert ('Inline preview (color approximation):' in text)==visible
 assert ('▀' in text)==visible
retained=output/'saved-images'; retained.mkdir()
shutil.copyfile(files[1],retained/'tiny-orange-robot.png')
print('PASS: same request and original PNG bytes for before, after and disabled')
print('PASS: after displays color fallback; off suppresses only the preview')
print('SHA256:',hashlib.sha256(data[0]).hexdigest())
VALIDATE
demo_assemble_capture 200 before after disabled
printf 'Recorded inline-image fallback demo in %s\n' "$demo_output"
