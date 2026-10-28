#!/bin/bash
# Compare edit/variation saving with real binaries and the fixed multipart fixture.
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record-image-edit-saving.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
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
mkdir -p "$demo_runtime/inputs"
"$demo_python" - "$demo_runtime/inputs" <<'INPUTS'
import base64, pathlib, sys
png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP438AAAAQBAYDFKhhdAAAAAElFTkSuQmCC')
for name in ['photo.png', 'mask.png']:
    (pathlib.Path(sys.argv[1]) / name).write_bytes(png)
INPUTS
DEMO_IMAGE_UPLOAD_LOG="$demo_output/requests.jsonl" demo_start_api
cp "$demo_source/image-edit-saving.tape" "$demo_output/comparison.tape"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_INPUT_DIR" || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | asciinema + agg | synthetic loopback API'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.3
demo_args=()
printf '$ openai'
if [ "$DEMO_CASE" = json ]; then
  demo_args=(--format json)
  printf ' --format json'
fi
if [ "$DEMO_CASE" = variation ]; then
  demo_args+=(images create-variation --image photo.png)
  printf ' images create-variation --image photo.png\n'
else
  demo_args+=(images edit --image photo.png --mask mask.png --prompt 'Make the sky purple')
  printf ' images edit --image photo.png --mask mask.png --prompt "Make the sky purple"\n'
fi
sleep 0.3
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 3
exit "$demo_status"
SCENE
cat > "$demo_output/commands.txt" <<'COMMANDS'
before/after edit: openai images edit --image photo.png --mask mask.png --prompt "Make the sky purple"
before/after variation: openai images create-variation --image photo.png
explicit JSON: openai --format json images edit --image photo.png --mask mask.png --prompt "Make the sky purple"
COMMANDS
{
  echo 'feature: automatic saving for image edits and variations'
  echo "captured before commit: $demo_before_sha"
  echo "captured after commit: $demo_after_sha"
  echo 'data: one fixed synthetic PNG as photo/mask/result; fake key; loopback only'
  echo 'response: edit returns identical base64; variation returns fixture URL before, b64_json after saving defaults'
  echo 'capture: real PTY; isolated environment/HOME; bash --noprofile --norc; exact uploads validated before logging'
  echo 'render: asciinema 3.2.1 + agg 1.9.0; Menlo 22px; Dracula; 104 columns x 20 rows; 20 fps cap'
  echo 'scope: terminal-text replay, not native image rendering or Windows validation'
  echo 'retention: temporary HOME paths are cleaned; sources and saved originals remain under images/'
  echo 'recipe: record-image-edit-saving.sh; comparison.tape is the VHS alternative'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record-image-edit-saving.sh" \
    "$demo_source/image-edit-saving.tape" "$demo_source/main.go"
} > "$demo_output/metadata.txt"
demo_window_size=104x20
demo_render_options=(--renderer resvg --font-family Menlo --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after before-variation after-variation explicit-json; do
  demo_command_dir="$demo_runtime/after"
  demo_case=edit
  case "$demo_scene" in
    before) demo_command_dir="$demo_runtime/before"; demo_label='Before: edit returns image data';;
    after) demo_label='After: edit saves a new file, keeping the photo and mask';;
    before-variation) demo_command_dir="$demo_runtime/before"; demo_case=variation; demo_label='Before: variation returns a URL';;
    after-variation) demo_case=variation; demo_label='After: variation requests image bytes and saves a new file';;
    explicit-json) demo_case=json; demo_label='Explicit JSON: original API data, with no saving defaults';;
  esac
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_api_url" "$demo_label" \
    HOME="$demo_runtime/$demo_scene-home" FORCE_COLOR=0 NO_COLOR=1 "DEMO_CASE=$demo_case" \
    "DEMO_INPUT_DIR=$demo_runtime/inputs"
done
demo_stop_api
"$demo_python" - "$demo_output" "$demo_runtime" <<'VALIDATE' > "$demo_output/validation.txt"
import base64, hashlib, json, pathlib, shutil, sys
output, runtime = map(pathlib.Path, sys.argv[1:])
requests = [json.loads(line) for line in (output / 'requests.jsonl').read_text().splitlines()]
prompt = {'prompt': ['Make the sky purple']}
preset = dict(prompt, model=['gpt-image-2.5-sunburst'], n=['1'], size=['auto'], quality=['auto'],
              background=['auto'], output_format=['png'], partial_images=['0'], stream=['false'])
assert [r['values'] for r in requests] == [prompt, preset, {}, {'model':['dall-e-2'], 'response_format':['b64_json']}, prompt]
assert [r['path'] for r in requests] == ['/v1/images/edits'] * 2 + ['/v1/images/variations'] * 2 + ['/v1/images/edits']
png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP438AAAAQBAYDFKhhdAAAAAElFTkSuQmCC', validate=True)
digest = hashlib.sha256(png).hexdigest()
photo = {'name':'photo.png', 'sha256':digest}
mask = {'name':'mask.png', 'sha256':digest}
assert [r['files'] for r in requests] == [{'image[]':photo, 'mask':mask}] * 2 + [{'image':photo}] * 2 + [{'image[]':photo, 'mask':mask}]
for source in (runtime / 'inputs').iterdir():
    assert source.read_bytes() == png, 'source or mask changed'
    retained = output / 'images' / 'sources'
    retained.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, retained / source.name)
scenes = ['before', 'after', 'before-variation', 'after-variation', 'explicit-json']
texts = {scene: (output / f'{scene}.txt').read_text() for scene in scenes}
for scene in scenes:
    files = sorted((runtime / f'{scene}-home').rglob('*.png'))
    wanted = {'after':'make-the-sky-purple.png', 'after-variation':'image-variation.png'}.get(scene)
    assert [f.name for f in files] == ([wanted] if wanted else []), f'{scene}: unexpected saved files'
    for file in files:
        assert file.read_bytes() == png, f'{scene}: saved bytes changed'
        assert f'Saved image: "{file}"' in texts[scene], 'complete saved path absent'
        retained = output / 'images' / scene
        retained.mkdir(parents=True)
        shutil.copyfile(file, retained / file.name)
assert 'base64 characters' in texts['before'] and 'Saved image:' not in texts['before']
assert 'https://example.invalid/synthetic-variation.png' in texts['before-variation']
assert 'b64_json' in texts['explicit-json'] and 'Saved image:' not in texts['explicit-json']
print('PASS: all five scenes exit 0; exact fixed multipart requests match defaults and bypass behavior')
print('PASS: edits preserve photo and mask upload bytes; variations preserve source bytes')
print('PASS: only saving scenes create PNGs; names and complete printed paths match')
print('PASS: source, mask and saved results stay byte-for-byte identical; retained under images/')
print(f'PASS: PNG SHA256 {digest}; {len(png)} bytes')
VALIDATE
demo_assemble_capture 200 before after before-variation after-variation explicit-json
printf 'Recorded image edit/variation replay in %s\n' "$demo_output"
