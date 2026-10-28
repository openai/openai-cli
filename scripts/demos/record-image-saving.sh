#!/bin/bash
# Reproduce with independently built binaries from the two supplied commits:
#   go build -o dist/demos/bin/demo-api ./scripts/demos/main.go
#   PATH="$HOME/.cache/cli-terminal-replay/bin:$PATH" \
#     bash scripts/demos/record-image-saving.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR
# Keep OUTPUT_DIR outside this checkout. The .tape is an alternative VHS recipe;
# this runner uses real PTY capture with asciinema 3.2.1 and agg 1.9.0.
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo "usage: record-image-saving.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR" >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
# A short, isolated HOME keeps complete saved paths readable in each scene.
TMPDIR=/tmp demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/demo-api}"
demo_python="$(command -v python3)"
test "$("$demo_asciinema" --version)" = 'asciinema 3.2.1'
test "$("$demo_agg" --version)" = 'agg 1.9.0'
mkdir -p "$demo_runtime/b" "$demo_runtime/a" "$demo_runtime/j"
DEMO_IMAGE_REQUEST_LOG="$demo_output/requests.jsonl" demo_start_api
cp "$demo_source/image-saving.tape" "$demo_output/comparison.tape"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | asciinema + agg | synthetic loopback API'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.3
demo_args=(images generate --prompt 'A tiny orange robot')
printf '$ openai'
if [ "$DEMO_FORMAT" = json ]; then
  demo_args=(--format json "${demo_args[@]}")
  printf ' --format json'
fi
printf ' images generate --prompt "A tiny orange robot"\n'
sleep 0.3
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 2
exit "$demo_status"
SCENE

cat > "$demo_output/commands.txt" <<'COMMANDS'
before: openai images generate --prompt "A tiny orange robot"
after: openai images generate --prompt "A tiny orange robot"
explicit-json: openai --format json images generate --prompt "A tiny orange robot"
COMMANDS
{
  echo 'feature: generation defaults and automatic saving'
  echo "captured before commit: $demo_before_sha"
  echo "captured after commit: $demo_after_sha"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo 'provenance: supplied commits must be independently verified against the built binaries'
  echo 'data: the identical synthetic one-pixel PNG for every scene; fake key; loopback only'
  echo 'capture: real PTY, stdin/stdout/stderr verified; isolated environment and per-scene HOME; bash --noprofile --norc'
  echo 'render: asciinema 3.2.1 + agg 1.9.0 resvg; Menlo 22px, Dracula, 90 columns x 24 rows, line height 1.2, 20 fps cap'
  echo 'scope: terminal-text replay; no native Apple Terminal, PowerShell, or cmd.exe capture'
  echo 'recipe: record-image-saving.sh; comparison.tape is an alternative VHS recipe, not the renderer used'
  echo 'retention: displayed temporary HOME paths are cleaned; saved bytes remain in saved-images/'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record-image-saving.sh" \
    "$demo_source/image-saving.tape" "$demo_source/main.go"
} > "$demo_output/metadata.txt"

demo_window_size=90x24
demo_render_options=(--renderer resvg --font-family Menlo --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after explicit-json; do
  demo_command_dir="$demo_runtime/after"
  demo_format=default
  case "$demo_scene" in
    before) demo_command_dir="$demo_runtime/before"; demo_home="$demo_runtime/b"; demo_label='Before: main | synthetic image response';;
    after) demo_home="$demo_runtime/a"; demo_label='After: automatic saving | identical synthetic response';;
    explicit-json) demo_home="$demo_runtime/j"; demo_format=json; demo_label='Explicit JSON: API data, with no automatic saving';;
  esac
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_api_url" "$demo_label" \
    HOME="$demo_home" FORCE_COLOR=0 NO_COLOR=1 "DEMO_FORMAT=$demo_format"
done

demo_stop_api
"$demo_python" - "$demo_output" "$demo_runtime" <<'VALIDATE' > "$demo_output/validation.txt"
import base64, hashlib, json, pathlib, re, shutil, sys
output, runtime = map(pathlib.Path, sys.argv[1:])
requests = [json.loads(line) for line in (output / 'requests.jsonl').read_text().splitlines()]
prompt_only = {'prompt': 'A tiny orange robot'}
preset = dict(prompt_only, model='gpt-image-2.5-sunburst', n=1, size='auto', quality='auto',
              output_format='png', background='auto', moderation='auto', partial_images=0, stream=False)
assert requests == [prompt_only, preset, prompt_only], 'unexpected requests or defaults'
assert list((runtime / 'b').rglob('*')) == [], 'before scene unexpectedly saved files'
assert list((runtime / 'j').rglob('*')) == [], 'explicit JSON unexpectedly saved files'
saved_dir = runtime / 'a' / 'Downloads' / 'gpt-images'
files = sorted(saved_dir.iterdir())
assert [file.name for file in files] == ['tiny-orange-robot.png'], 'wrong saved filename or leftover probes'
events = [json.loads(line) for line in (output / 'explicit-json.cast').read_text().splitlines()[1:]]
text = ''.join(event[2] for event in events if event[1] == 'o')
text = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', text)
response, _ = json.JSONDecoder().raw_decode(text[text.index('{'):])
png = base64.b64decode(response['data'][0]['b64_json'], validate=True)
assert png.startswith(b'\x89PNG\r\n\x1a\n') and files[0].read_bytes() == png, 'saved bytes differ'
before = (output / 'before.txt').read_text()
after = (output / 'after.txt').read_text()
assert 'base64 characters' in before and 'Saved image:' not in before, 'unexpected before output'
assert 'Saved image: "' + str(files[0]) + '"' in after, 'saved path not printed in full'
retained = output / 'saved-images'
retained.mkdir()
shutil.copyfile(files[0], retained / files[0].name)
print('PASS: three synthetic requests; only the saving scene applies generation defaults')
print('PASS: before and explicit JSON create no image files')
print('PASS: after creates the default directory and prints the complete prompt-based saved path')
print(f'PASS: retained {len(png)} exact PNG bytes, SHA256 {hashlib.sha256(png).hexdigest()}')
VALIDATE

demo_assemble_capture 200 before after explicit-json
printf 'Recorded image-saving terminal replay in %s\n' "$demo_output"
