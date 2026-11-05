#!/bin/bash
# Compare the first Space at a terminal boundary using identical inputs.
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record-page-boundary.sh BEFORE_BINARY BEFORE_COMMIT AFTER_BINARY AFTER_COMMIT EMPTY_OUTPUT_DIRECTORY' >&2
  exit 2
fi
demo_recipe="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_recipe/../../.." && pwd)"
source "$demo_root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$3" "$2" "$4" "$5" "$demo_recipe/fixture.py"
demo_python="$(command -v python3)"
demo_start_api "$demo_output/requests.jsonl" --pages 6 --rows 25 --numbered-files
for demo_source in record-page-boundary.sh scene.py fixture.py; do
  cp "$demo_recipe/$demo_source" "$demo_output/$demo_source"
done
cp "$demo_root/scripts/demos/capture_and_render.sh" "$demo_output/capture_and_render.sh"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '150 synthetic files | local API | 25 files per API page\n\n'
printf '$ openai files list --limit 25 --max-items -1\n'
sleep 0.2
if "$DEMO_PYTHON" "$DEMO_SCENE_DRIVER" openai files list --limit 25 --max-items -1; then demo_status=0; else demo_status=$?; fi
printf '$ '
sleep 2
exit "$demo_status"
SCENE
cp "$demo_runtime/scene.sh" "$demo_output/shell-scene.sh"
{
  echo 'comparison: before and after the pager format-hint fix; same Space, Space, b, Space, q inputs'
  echo 'view: actual PTY 101x27; Menlo 18; blue headers; not a native Terminal screenshot'
  echo 'before: first Space reveals the format hint; second Space fetches API page two'
  echo 'after: first Space fetches API page two; second Space fetches API page three'
  echo 'fixture: 150 numbered files; representative recording visits 50 before and 75 after'
  echo "before source commit: $demo_before_sha"
  echo "after source commit: $demo_after_sha"
  demo_capture_metadata
} > "$demo_output/metadata.txt"
demo_window_size=101x27
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 --theme github-dark --fps-cap 20 --last-frame-duration 2)
for demo_version in before after; do
  mkdir "$demo_runtime/home-$demo_version"
  demo_markers='001 025 026 025 026'
  demo_label='Before: the first Space shows the same 25 files'
  if [ "$demo_version" = after ]; then
    demo_markers='001 026 051 026 051'
    demo_label='After: each Space shows the next 25 files'
  fi
  demo_actions="$("$demo_python" -I -B - "$demo_markers" <<'ACTIONS'
import json,sys
print(json.dumps([{'marker':'file-demo-'+value,'footer':'q: quit','key':key}
                  for value,key in zip(sys.argv[1].split(),[' ',' ','b',' ','q'])]))
ACTIONS
)"
  demo_capture_scene "$demo_version" 0 "$demo_runtime/$demo_version" "$demo_api_url" "$demo_label" \
    "DEMO_PYTHON=$demo_python" "DEMO_SCENE_DRIVER=$demo_output/scene.py" "DEMO_ASCIINEMA=$demo_asciinema" \
    "DEMO_ACTIONS_JSON=$demo_actions" "DEMO_RELAY_EVIDENCE=$demo_output/$demo_version.relay.json" \
    "HOME=$demo_runtime/home-$demo_version" "XDG_CONFIG_HOME=$demo_runtime/home-$demo_version" \
    TERM_PROGRAM=Apple_Terminal COLORTERM=truecolor 'COLORFGBG=15;0' GOMAXPROCS=2 \
    OPENAI_CLI_NO_UPDATE_CHECK=1 OPENAI_CLI_NO_AUTO_UPDATE=1
done
demo_stop_api
"$demo_python" -I -B - "$demo_output" "$demo_before" "$demo_after" "$demo_asciinema" <<'VERIFY'
import hashlib,json,re,subprocess,sys
from pathlib import Path
output=Path(sys.argv[1])
report={'binaries':{label:{'path':value,'sha256':hashlib.sha256(Path(value).read_bytes()).hexdigest()}
                   for label,value in zip(('before','after'),sys.argv[2:4])},'keys':[' ',' ','b',' ','q']}
for entry in report['binaries'].values():
    assert entry['sha256']+'  '+entry['path'] in (output/'metadata.txt').read_text(), 'binary changed during capture'
for version in ('before','after'):
    cast=[json.loads(line) for line in (output/f'{version}.cast').read_text().splitlines()]
    assert (cast[0]['width'],cast[0]['height'])==(101,27)
    command='$ openai files list --limit 25 --max-items -1\r\n'
    captured=''.join(event[2] for event in cast[1:] if event[1]=='o')
    assert captured.count(command)==1
    raw,tail=captured.split(command,1)[1].split('$ ',1)
    assert not tail
    relay=json.loads((output/f'{version}.relay.json').read_text())
    assert relay['exit_status']==0 and [item['key'] for item in relay['inputs']]==report['keys']
    assert hashlib.sha256(raw.encode()).hexdigest()==relay['sha256']
    assert '\x1b[38;2;138;168;255mID' in raw
    visible=(output/f'{version}.txt').read_text()
    assert visible.rstrip().endswith('$') and 'file-demo-' not in visible
    command_time=next(event[0] for event in cast[1:] if event[1]=='o' and command in event[2])
    cutoff=command_time+relay['inputs'][1]['time']-0.2
    prefix=[cast[0],*[event for event in cast[1:] if event[0]<=cutoff]]
    recording=output/f'{version}-first-space.cast'
    text=output/f'{version}-first-space.txt'
    recording.write_text('\n'.join(json.dumps(event) for event in prefix)+'\n')
    subprocess.run([sys.argv[4],'convert','-f','txt',str(recording),str(text)],check=True)
    screen=text.read_text()
    first=1 if version=='before' else 26
    ids=re.findall(r'\bfile-demo-\d{3}\b',screen)
    assert ids==[f'file-demo-{value:03d}' for value in range(first,first+25)]
    assert ('Full data:' in screen)==(version=='before')
    assert 'q: quit' in screen and 'Space: more' in screen
    (output/f'{version}-first-space.time').write_text(str(cutoff))
    report[version]={'first_space_ids':ids,'relay_sha256':relay['sha256'],'original_screen_restored':True}
requests=[json.loads(line) for line in (output/'requests.jsonl').read_text().splitlines()]
assert requests==[{'page':page,'status':200,'cursor_valid':True} for page in [1,2,1,2,3]]
report.update(result='PASS',requests=requests)
(output/'validation.json').write_text(json.dumps(report,indent=2)+'\n')
print('PASS first-Space comparison: same inputs; exact bytes; files 001–025 before, 026–050 after; restored shell')
VERIFY
for demo_version in before after; do
  "$demo_agg" --quiet "${demo_render_options[@]}" --select "$(cat "$demo_output/$demo_version-first-space.time")" \
    "$demo_output/$demo_version.cast" "$demo_output/$demo_version-first-space.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_version-first-space.gif" \
    -frames:v 1 "$demo_output/$demo_version-first-space.png"
done
demo_assemble_capture 200 before after
printf 'Recorded page-boundary comparison: %s\n' "$demo_output"
