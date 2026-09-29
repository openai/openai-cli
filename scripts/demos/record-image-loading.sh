#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-image-loading.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
# A short, isolated HOME keeps complete saved paths readable.
TMPDIR=/tmp demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/demo-api}"
demo_python="$(command -v python3)"
demo_window_size=90x20
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
mkdir -p "$demo_runtime/b" "$demo_runtime/a"
DEMO_IMAGE_REQUEST_LOG="$demo_output/requests.jsonl" DEMO_IMAGE_DELAY=2s \
  DEMO_IMAGE_PREVIEW_FIXTURE= demo_start_api

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' 'Terminal replay | asciinema + agg | synthetic response delayed 2s'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.3
printf '$ openai images generate --prompt "A tiny orange robot" --inline off\n'
sleep 0.3
if openai images generate --prompt 'A tiny orange robot' --inline off; then
  demo_status=0
else
  demo_status=$?
fi
printf '\n[exit %s]\n$ ' "$demo_status"
sleep 2
exit "$demo_status"
SCENE
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
{
  echo 'feature: image loading feedback'
  echo "comparison parent commit: $demo_before_sha"
  echo "proposed commit: $demo_after_sha"
  echo 'command: openai images generate --prompt "A tiny orange robot" --inline off'
  echo 'data: fixed synthetic one-pixel PNG; fake key; loopback fixture; 2s response delay'
  echo 'capture: real binaries in isolated bash PTYs with separate temporary HOME directories'
  echo 'render: asciinema + agg, Menlo 18px, Dracula, 90x20, line height 1.2'
  echo 'screenshots: actual pending frames selected one second before each saved-path event'
  echo 'scope: terminal replay, not native Apple Terminal, Windows or image-renderer validation'
  echo 'retention: temporary HOME paths are cleaned; exact saved bytes retained in saved-images/'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record-image-loading.sh" "$demo_source/main.go"
} > "$demo_output/metadata.txt"

demo_capture_scene before 0 "$demo_runtime/before" "$demo_api_url" \
  'Before: parent | waiting for the synthetic response' HOME="$demo_runtime/b"
demo_capture_scene after 0 "$demo_runtime/after" "$demo_api_url" \
  'After: loading feedback | the same synthetic response' HOME="$demo_runtime/a"
demo_stop_api

"$demo_python" - "$demo_output" "$demo_runtime" <<'VALIDATE' > "$demo_output/validation.txt"
import base64, hashlib, json, pathlib, shutil, sys
output, runtime = map(pathlib.Path, sys.argv[1:])
requests = [json.loads(line) for line in (output / 'requests.jsonl').read_text().splitlines()]
expected = dict(prompt='A tiny orange robot', model='gpt-image-2.5-sunburst', n=1,
                size='auto', quality='auto', output_format='png', background='auto',
                moderation='auto', partial_images=0, stream=False)
assert requests == [expected, expected], 'requests or defaults changed'
png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP438AAAAQBAYDFKhhdAAAAAElFTkSuQmCC')
retained = output / 'saved-images'
retained.mkdir()
for scene, home in [('before', 'b'), ('after', 'a')]:
    events = [json.loads(line) for line in (output / (scene + '.cast')).read_text().splitlines()[1:]]
    events = [event for event in events if event[1] == 'o']
    raw = ''.join(event[2] for event in events)
    files = list((runtime / home / 'Downloads' / 'gpt-images').iterdir())
    assert len(files) == 1 and files[0].name == 'tiny-orange-robot.png'
    assert files[0].read_bytes() == png, 'saved bytes changed'
    assert 'Saved image: "' + str(files[0]) + '"' in raw, 'incomplete saved path'
    assert '[exit 0]' in raw and raw.count('Saved image:') == 1
    assert '\x1b[?25l' not in raw, 'recording or CLI hid the cursor'
    saved_at = next(event[0] for event in events if 'Saved image:' in event[2])
    pending_at = saved_at - 1
    pending = ''.join(event[2] for event in events if event[0] <= pending_at)
    assert '--inline off' in pending and 'Saved image:' not in pending
    if scene == 'after':
        assert 'Generating image' in pending, 'no feedback while waiting'
        assert raw.count('Generating image') > 1, 'feedback did not animate'
        assert '\r\x1b[2K' in raw[:raw.index('Saved image:')], 'loading line was not cleared'
        assert 'Generating image' not in raw[raw.index('Saved image:'):], 'feedback continued after output'
    else:
        assert 'Generating image' not in raw, 'baseline already has loading feedback'
    (output / (scene + '-pending-time.txt')).write_text(f'{pending_at:.6f}\n')
    shutil.copyfile(files[0], retained / (scene + '.png'))
    print(f'PASS: {scene} exits 0; exact PNG saved; pending frame at {pending_at:.6f}s, saved path at {saved_at:.6f}s')
print('PASS: identical synthetic requests, unchanged saved bytes and visible cursor')
print('PASS: after shows animated feedback during the wait and clears it before the saved path')
print('PNG SHA256:', hashlib.sha256(png).hexdigest())
VALIDATE

for demo_scene in before after; do
  mv "$demo_output/$demo_scene.png" "$demo_output/$demo_scene-complete.png"
  demo_pending_time="$(cat "$demo_output/$demo_scene-pending-time.txt")"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_pending_time" \
    "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene-pending.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y \
    -i "$demo_output/$demo_scene-pending.gif" -frames:v 1 "$demo_output/$demo_scene.png"
done
demo_assemble_capture 200 before after
printf 'Recorded loading-feedback comparison in %s\n' "$demo_output"
