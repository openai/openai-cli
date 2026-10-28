#!/bin/bash
set -euo pipefail
if [ "$#" -ne 3 ]; then
  echo 'usage: record-large.sh BINARY SOURCE_COMMIT EMPTY_OUTPUT_DIRECTORY' >&2
  exit 2
fi
demo_recipe="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_recipe/../../.." && pwd)"
source "$demo_root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$1" "$2" "$2" "$3" "$demo_recipe/fixture.py"
demo_python="$(command -v python3)"
demo_initial_hash="$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
demo_start_api "$demo_output/requests.jsonl" --pages 6 --rows 25 --numbered-files
cp "$demo_recipe/scene.py" "$demo_output/scene.py"
cp "$demo_recipe/fixture.py" "$demo_output/fixture.py"
cp "$demo_recipe/record-large.sh" "$demo_output/capture.sh"
cp "$demo_root/scripts/demos/capture_and_render.sh" "$demo_output/capture_and_render.sh"
mkdir "$demo_runtime/home"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '150 synthetic files | local API | 25 files per API page\n'
printf 'Space: more   b: back   q: quit\n\n'
printf '$ openai files list --limit 25 --max-items -1\n'
sleep 0.2
if "$DEMO_PYTHON" "$DEMO_SCENE_DRIVER" openai files list --limit 25 --max-items -1; then
  demo_status=0
else
  demo_status=$?
fi
printf '$ '
sleep 2
exit "$demo_status"
SCENE
cp "$demo_runtime/scene.sh" "$demo_output/shell-scene.sh"
{
  echo 'scope: actual pager replay with a 150-file synthetic loopback fixture; no runtime changes'
  echo 'view: 98x20; Menlo 18; blue CLI theme; not a native Terminal screenshot'
  echo 'inputs: Space through all six API pages, b, Space, q'
  echo 'coverage: all 150 unique files must appear in reconstructed recorded screens'
  echo 'numbering: file-demo-001 through file-demo-150; dataset-001.jsonl through dataset-150.jsonl'
  echo "source commit: $demo_after_sha; uncommitted candidate details remain in the pager-only review packet"
  demo_capture_metadata
  shasum -a 256 "$demo_output/scene.py" "$demo_output/fixture.py" "$demo_output/capture.sh"
} > "$demo_output/metadata.txt"
demo_window_size=98x20
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme github-dark --fps-cap 20 --last-frame-duration 2)
demo_actions="$("$demo_python" -I -B - <<'ACTIONS'
import json
actions = []
for page in range(1, 7):
    actions.append({'marker': f'file-demo-{(page-1)*25+1:03d}', 'footer': 'q: quit', 'key': ' '})
    actions.append({'marker': f'file-demo-{page*25:03d}', 'footer': 'q: quit', 'key': ' ' if page < 6 else 'b'})
actions.extend([
    {'marker': 'file-demo-126', 'footer': 'q: quit', 'key': ' '},
    {'marker': 'file-demo-150', 'footer': 'q: quit', 'key': 'q'},
])
print(json.dumps(actions))
ACTIONS
)"
demo_capture_scene after 0 "$demo_runtime/after" "$demo_api_url" '150 synthetic files' \
  "DEMO_PYTHON=$demo_python" "DEMO_SCENE_DRIVER=$demo_output/scene.py" \
  "DEMO_ASCIINEMA=$demo_asciinema" "DEMO_ACTIONS_JSON=$demo_actions" \
  "DEMO_RELAY_EVIDENCE=$demo_output/after.relay.json" \
  "HOME=$demo_runtime/home" "XDG_CONFIG_HOME=$demo_runtime/home" \
  TERM_PROGRAM=Apple_Terminal COLORTERM=truecolor 'COLORFGBG=15;0' GOMAXPROCS=2 \
  OPENAI_CLI_NO_UPDATE_CHECK=1 OPENAI_CLI_NO_AUTO_UPDATE=1
demo_stop_api
"$demo_agg" --quiet "${demo_render_options[@]}" --select 1 "$demo_output/after.cast" "$demo_output/after-first.gif"
"$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/after-first.gif" \
  -frames:v 1 "$demo_output/after-first.png"
"$demo_python" -I -B - "$demo_output" "$demo_after" "$demo_initial_hash" "$demo_asciinema" <<'VERIFY'
import hashlib, json, re, subprocess, sys
from pathlib import Path
output, binary = map(Path, sys.argv[1:3])
assert hashlib.sha256(binary.read_bytes()).hexdigest() == sys.argv[3], "binary changed during capture"
cast = [json.loads(line) for line in (output/'after.cast').read_text().splitlines()]
assert (cast[0]['width'], cast[0]['height']) == (98,20)
captured = ''.join(event[2] for event in cast[1:] if event[1] == 'o')
command = '$ openai files list --limit 25 --max-items -1\r\n'
assert captured.count(command) == 1
raw, tail = captured.split(command,1)[1].split('$ ',1)
assert not tail
raw = raw.encode()
relay = json.loads((output/'after.relay.json').read_text())
assert relay['exit_status'] == 0
keys = [item['key'] for item in relay['inputs']]
assert keys == [' '] * 11 + ['b', ' ', 'q']
assert hashlib.sha256(raw).hexdigest() == relay['sha256']
assert b'\x1b[38;2;138;168;255mID' in raw
assert not re.search(rb'\x1b\[\?(?:2026|2027)\$p', raw)
visible = (output/'after.txt').read_text()
assert visible.count('150 synthetic files') == 1 and visible.rstrip().endswith('$')
assert 'file-demo-' not in visible
requests = [json.loads(line) for line in (output/'requests.jsonl').read_text().splitlines()]
assert requests == [{'page': page, 'status': 200, 'cursor_valid': True} for page in range(1,7)]
screens = output/'screens'
screens.mkdir()
command_time = next(event[0] for event in cast[1:] if event[1] == 'o' and command in event[2])
expected_ids = [f'file-demo-{number:03d}' for number in range(1,151)]
seen, frames = [], []
for number, action in enumerate(relay['inputs'], start=1):
    cutoff = command_time + action['time'] - 0.2
    prefix = [cast[0], *[event for event in cast[1:] if event[0] <= cutoff]]
    recording, transcript = screens/f'{number:02d}.cast', screens/f'{number:02d}.txt'
    recording.write_text('\n'.join(json.dumps(event) for event in prefix)+'\n')
    subprocess.run([sys.argv[4], 'convert', '-f', 'txt', str(recording), str(transcript)],
                   check=True, capture_output=True, timeout=5,
                   env={'PATH':'/usr/bin:/bin', 'HOME':str(screens),
                        'ASCIINEMA_CONFIG_HOME':str(screens/'config'),
                        'ASCIINEMA_STATE_HOME':str(screens/'state')})
    screen = transcript.read_text()
    assert action['ready'] in screen and 'q: quit' in screen, (number, action, screen)
    identifiers = re.findall(r'\bfile-demo-[0-9]{3}\b', screen)
    assert len(identifiers) == len(set(identifiers)), (number, identifiers)
    assert all(f'dataset-{identifier[-3:]}.jsonl' in screen for identifier in identifiers), screen
    new = [identifier for identifier in identifiers if identifier not in seen]
    assert new == expected_ids[len(seen):len(seen)+len(new)], (number, 'missing or reordered records', new)
    seen.extend(new)
    frames.append({'screen':str(transcript.relative_to(output)), 'ids':identifiers, 'key':action['key']})
assert seen == expected_ids, ('recorded files missing', len(seen))
assert frames[-2]['ids'] == frames[-4]['ids'], 'back did not restore the previous chunk'
assert frames[-1]['ids'] == frames[-3]['ids'], 'forward did not restore the final chunk'
assert 'End' in screen and 'b: back' in screen and 'Space: more' not in screen
(output/'after-final-page.cast').write_bytes(recording.read_bytes())
(output/'after-final-page.txt').write_text(screen)
(output/'after-final-page-time.txt').write_text(str(cutoff)+'\n')
(output/'after.raw').write_bytes(raw)
(output/'validation.json').write_text(json.dumps({
  'binary': str(binary), 'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
  'result': 'PASS', 'fixture_records':150, 'recorded_unique_records':len(seen), 'recorded_api_pages':6,
  'rendered_screens':frames, 'keys':keys, 'exact_relay_sha256':relay['sha256'],
  'original_history_restored':True, 'blue_headers':True,
  'scope':'Actual PTY replay; all 150 unique files verified across reconstructed recorded screens.'},indent=2)+'\n')
print('PASS full replay: 150 visible files; six API pages; exact bytes; back; restored shell')
VERIFY
"$demo_agg" --quiet "${demo_render_options[@]}" --select "$(cat "$demo_output/after-final-page-time.txt")" \
  "$demo_output/after.cast" "$demo_output/after-final-page.gif"
"$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/after-final-page.gif" \
  -frames:v 1 "$demo_output/after-final-page.png"
printf 'Recorded 150-file pager replay: %s\n' "$demo_output"
