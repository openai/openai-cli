#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-automatic-update.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "${DEMO_API_BINARY:?Set DEMO_API_BINARY to the existing rejecting demo fixture}"
demo_runtime="$(cd "$demo_runtime" && pwd -P)"
demo_window_size=100x42
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.3 --theme asciinema --fps-cap 20 --last-frame-duration 2)

for demo_side in before after; do
  demo_home="$demo_runtime/$demo_side-home"
  mkdir -p "$demo_home/.openai/bin" "$demo_home/work"
  chmod 700 "$demo_home/.openai" "$demo_home/.openai/bin"
  cp "$demo_runtime/$demo_side/openai" "$demo_home/.openai/bin/openai"
  chmod 700 "$demo_home/.openai/bin/openai"
  printf '#!/bin/sh\nexit 1\n' > "$demo_home/.openai/bin/gh"
  chmod 700 "$demo_home/.openai/bin/gh"
  env -i PATH=/usr/bin:/bin HOME="$demo_home" OPENAI_CLI_NO_UPDATE_CHECK=1 \
    "$demo_home/.openai/bin/openai" --version > "$demo_output/$demo_side-version.txt"
done

"$demo_python" -I -B - "$demo_runtime" "$demo_output" "$demo_after_sha" <<'PY'
import hashlib, json, pathlib, re, sys, time
runtime, output = map(pathlib.Path, sys.argv[1:3])
versions = []
for side in ('before', 'after'):
    text = (output / (side + '-version.txt')).read_text()
    matches = re.findall(r'\b\d+\.\d+\.\d+\b', text)
    assert len(matches) == 1, text
    version = matches[0]
    versions.append(version)
    home = runtime / (side + '-home')
    cache = home / ('Library/Caches' if sys.platform == 'darwin' else '.cache') / 'openai-cli/update-notice'
    cache.mkdir(parents=True, mode=0o700)
    state = cache / 'state.json'
    state.write_text(json.dumps(dict(schema=1, checked=int(time.time()), notified=0, latest=version)))
    state.chmod(0o600)
assert versions[0] == versions[1], 'Compare identical help versions; only confirmation should change'
home = runtime / 'after-home'
binary = home / '.openai/bin/openai'
directory = binary.parent / '.update'
directory.mkdir(mode=0o700)
receipt = dict(schema=1, checked=int(time.time()), activation_checked=0, starting=0,
               applied=versions[1], announced=False,
               pending=dict(release=dict(version=versions[1], commit=sys.argv[3], release_id=1, asset_id=2,
                                        digest='a'*64, size=10, updated='2026-10-22T00:00:00Z'),
                            binary_hash=hashlib.sha256(binary.read_bytes()).hexdigest(), previous_hash='b'*64))
state = directory / 'state.json'
state.write_text(json.dumps(receipt))
state.chmod(0o600)
(output / 'synthetic-receipt.json').write_text(json.dumps(receipt, indent=2))
PY

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY OPENAI_ADMIN_KEY OPENAI_WEBHOOK_SECRET
cd "$HOME/work"
printf '\033[2J\033[H$ openai\n'
sleep 0.3
openai
printf '\n$ '
sleep 2
SCENE
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
{
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'Synthetic activation receipt; no update download, installation, or real release upgrade is depicted.'
  echo 'Both help versions match. The candidate only consumes its synthetic activation receipt.'
  echo 'Real CLI binaries; isolated Bash PTYs; terminal replay, not graphical terminal validation.'
  demo_capture_metadata
} > "$demo_output/metadata.txt"
demo_start_api "$demo_runtime/requests.txt"
for demo_scene in before after repeat; do
  demo_side=after
  if [ "$demo_scene" = before ]; then demo_side=before; fi
  demo_home="$demo_runtime/$demo_side-home"
  demo_capture_scene "$demo_scene" 0 "$demo_home/.openai/bin" "$demo_api_url/help-only/v1" "$demo_scene: activation confirmation" \
    "HOME=$demo_home" "XDG_CONFIG_HOME=$demo_home/config" 'COLORFGBG=15;0' 'COLORTERM=truecolor' \
    'HTTPS_PROXY=http://127.0.0.1:1' 'NO_PROXY=' 'GOENV=off' 'GOMAXPROCS=2'
done
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
test ! -s "$demo_output/requests.txt"
"$demo_python" -I -B - "$demo_output" "$demo_runtime/after-home/.openai/bin/.update/state.json" <<'PY'
import json, pathlib, re, sys
output = pathlib.Path(sys.argv[1])
texts = [(output / (name+'.txt')).read_text() for name in ('before','after','repeat')]
assert 'Updated to ' not in texts[0]
assert 'Updated to ' not in texts[2]
assert len(re.findall(r'^Updated to v\d+\.\d+\.\d+\.$', texts[1], re.M)) == 1
assert texts[0] == texts[2]
assert texts[0] == re.sub(r'^Updated to v\d+\.\d+\.\d+\.\n', '', texts[1], flags=re.M)
assert json.loads(pathlib.Path(sys.argv[2]).read_text())['announced'] is True
(output / 'validation.txt').write_text('PASS: unchanged help; one left-aligned confirmation; repeat silent; receipt consumed; zero API requests.\n')
PY
demo_assemble_capture 200 before after repeat
shasum -a 256 "$demo_output/"*.png "$demo_output/comparison.gif" > "$demo_output/media-sha256.txt"
