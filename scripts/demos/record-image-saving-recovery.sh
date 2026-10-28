#!/bin/bash
# Reuse the image-saving fixture and shared real-PTY capture/render lifecycle.
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record-image-saving-recovery.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
TMPDIR=/tmp demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/demo-api}"
demo_python="$(command -v python3)"
test "$("$demo_asciinema" --version)" = 'asciinema 3.2.1'
test "$("$demo_agg" --version)" = 'agg 1.9.0'
DEMO_IMAGE_REQUEST_LOG="$demo_output/requests.jsonl" demo_start_api
cp "$demo_source/image-saving-recovery.tape" "$demo_output/comparison.tape"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | asciinema + agg | synthetic loopback API'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.3
demo_args=(images generate --prompt 'A tiny orange robot')
if [ "$DEMO_CASE" = unlimited ]; then
  printf '$ openai images generate --prompt "A tiny orange robot" --stream=true --max-items -1\n'
  sleep 0.3
  if openai "${demo_args[@]}" --stream=true --max-items -1; then demo_status=0; else demo_status=$?; fi
else
  printf '$ openai images generate --prompt "A tiny orange robot" --count 2 | true\n'
  sleep 0.3
  openai "${demo_args[@]}" --count 2 | true
  demo_status=${PIPESTATUS[0]}
fi
printf '\nExit status: %s\n\n$ ' "$demo_status"
sleep 3
exit "$demo_status"
SCENE
cat > "$demo_output/commands.txt" <<'COMMANDS'
before/after unlimited: openai images generate --prompt "A tiny orange robot" --stream=true --max-items -1
before/after partial save: openai images generate --prompt "A tiny orange robot" --count 2 | true
COMMANDS
{
  echo 'feature: unlimited image streams and saved-path failure recovery'
  echo "captured before commit: $demo_before_sha"
  echo "captured after commit: $demo_after_sha"
  echo 'data: fixed synthetic one-pixel PNG; malformed second image only in count-2 case; fake key; loopback only'
  echo 'capture: real PTY; isolated environment/HOME; bash --noprofile --norc; true closes the stdout pipe in recovery scenes'
  echo 'render: asciinema 3.2.1 + agg 1.9.0; Menlo 22px; Dracula; 104 columns x 18 rows; 20 fps cap'
  echo 'scope: terminal-text replay, not native Apple Terminal/PowerShell/cmd.exe validation'
  echo 'retention: temporary HOME paths are cleaned; exact saved PNGs retained in saved-images/'
  echo 'recipe: record-image-saving-recovery.sh; comparison.tape is an alternative VHS recipe'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record-image-saving-recovery.sh" \
    "$demo_source/image-saving-recovery.tape" "$demo_source/main.go"
} > "$demo_output/metadata.txt"
demo_window_size=104x18
demo_render_options=(--renderer resvg --font-family Menlo --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after before-recovery after-recovery; do
  demo_command_dir="$demo_runtime/after"
  demo_case=unlimited
  demo_expected=1
  case "$demo_scene" in
    before) demo_command_dir="$demo_runtime/before"; demo_label='Before: unlimited stream is rejected';;
    after) demo_expected=0; demo_label='After: unlimited stream saves the finished image';;
    before-recovery) demo_command_dir="$demo_runtime/before"; demo_case=recovery; demo_label='Before: one file saved, but stdout is closed';;
    after-recovery) demo_case=recovery; demo_label='After: both failures and recovery instructions are shown';;
  esac
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_capture_scene "$demo_scene" "$demo_expected" "$demo_command_dir" "$demo_api_url" "$demo_label" \
    HOME="$demo_runtime/$demo_scene-home" FORCE_COLOR=0 NO_COLOR=1 "DEMO_CASE=$demo_case"
done
demo_stop_api
"$demo_python" - "$demo_output" "$demo_runtime" <<'VALIDATE' > "$demo_output/validation.txt"
import base64, hashlib, json, pathlib, shutil, sys
output, runtime = map(pathlib.Path, sys.argv[1:])
requests = [json.loads(line) for line in (output / 'requests.jsonl').read_text().splitlines()]
preset = dict(prompt='A tiny orange robot', model='gpt-image-2.5-sunburst', n=1, size='auto',
              quality='auto', output_format='png', background='auto', moderation='auto', partial_images=0, stream=False)
assert requests == [dict(preset, stream=True), dict(preset, n=2), dict(preset, n=2)], 'unexpected request bodies/count'
texts = {scene: (output / f'{scene}.txt').read_text() for scene in ['before', 'after', 'before-recovery', 'after-recovery']}
assert '--max-items could stop before' in texts['before'] and 'Exit status: 1' in texts['before']
assert 'Saved image:' in texts['after'] and 'Exit status: 0' in texts['after']
assert 'The listed files are kept' in texts['before-recovery']
assert 'print all saved paths' in texts['after-recovery']
assert 'Check the output folder before generating again' in texts['after-recovery']
assert 'listed files' not in texts['after-recovery']
assert all('Exit status: 1' in texts[s] for s in ['before-recovery', 'after-recovery'])
png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP438AAAAQBAYDFKhhdAAAAAElFTkSuQmCC', validate=True)
for scene in texts:
    images = sorted((runtime / f'{scene}-home').rglob('*.png'))
    assert len(images) == (0 if scene == 'before' else 1), f'{scene}: wrong saved file count'
    for file in images:
        assert file.name == 'tiny-orange-robot.png' and file.read_bytes() == png, f'{scene}: PNG changed'
        retained = output / 'saved-images' / scene
        retained.mkdir(parents=True)
        shutil.copyfile(file, retained / file.name)
        if scene == 'after':
            assert f'Saved image: "{file}"' in texts[scene], 'complete saved path absent'
print('PASS: unlimited flag rejected before without an API call; saves exact PNG and exits 0 after')
print('PASS: partial batches save exactly one intact PNG before/after and exit 1 with closed stdout')
print('PASS: after recovery describes the incomplete batch, failed path output, and output-folder guidance')
print(f'PASS: retained PNG is {len(png)} bytes; SHA256 {hashlib.sha256(png).hexdigest()}')
VALIDATE
demo_assemble_capture 200 before after before-recovery after-recovery
printf 'Recorded image-saving recovery replay in %s\n' "$demo_output"
