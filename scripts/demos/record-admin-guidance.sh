#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record-admin-guidance.sh BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/admin_guidance_fixture.py"
demo_python="$(command -v python3)"
demo_start_api "$demo_output/requests.json"
demo_window_size=100x36
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3)
cat > "$demo_runtime/entry.exp" <<'ENTRY'
set timeout 10
expect_before timeout {exit 93}
spawn -noecho openai setup admin
expect {
  -exact {Admin API key (hidden): } {}
  eof {exit 94}
}
after 2000
send -- "\033\[200~sk-admin-SYNTHETIC-demo-only\033\[201~\r"
expect -exact {Admin access verified. The project-list request succeeded.}
expect -exact {No key was saved. Later commands still need authentication.}
expect eof
set result [wait]
if {[lindex $result 2] != 0} {exit 95}
exit [lindex $result 3]
ENTRY
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY OPENAI_ADMIN_KEY
screen() { printf '\033[2J\033[H%s\n\n' "$1"; }
screen "$DEMO_SCENE_LABEL"
if [ "$DEMO_STAGE" = before ]; then
printf '$ openai setup admin\n\n'
if openai setup admin; then demo_command_status=0; else demo_command_status=$?; fi
sleep 4
exit "$demo_command_status"
fi
if [ "$DEMO_STAGE" = missing ]; then
printf 'No key is configured.\n\n$ openai admin organization projects list\n'
if openai admin organization projects list; then demo_command_status=0; else demo_command_status=$?; fi
sleep 4
exit "$demo_command_status"
fi
[ "$DEMO_STAGE" = success ] || exit 98
printf 'Synthetic key and local API. No live account or key creation.\n\n'
printf '$ openai setup admin\n'
export OPENAI_BASE_URL="$DEMO_API_URL"
/usr/bin/expect "$DEMO_ENTRY"
sleep 4
SCENE
{
  echo 'feature: one setup command, hidden key entry, automatic read-only verification'
  echo "base commit: $demo_before_sha"
  echo "candidate commit: $demo_after_sha"
  echo 'capture: actual binaries in isolated PTYs; synthetic credentials and local API only'
  echo 'dashboard: guide text only; no live account session or key creation'
  echo 'before: fresh main rejects the unavailable setup admin command'
  echo 'after: missing-key guidance followed by setup admin and a hidden synthetic paste'
  echo 'scope: asciinema/agg terminal replay, not native graphical terminal inspection'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record-admin-guidance.sh"
} > "$demo_output/metadata.txt"
cp "$demo_runtime/scene.sh" "$demo_runtime/entry.exp" "$demo_output/"
demo_capture_scene before 3 "$demo_runtime/before" https://api.openai.com/v1 \
  'Before: no interactive admin setup command' \
  DEMO_STAGE=before \
  HTTPS_PROXY=http://127.0.0.1:1 NO_PROXY=127.0.0.1
demo_capture_scene after-error 1 "$demo_runtime/after" https://api.openai.com/v1 \
  'After: missing-key guidance points to one setup command' \
  DEMO_STAGE=missing HTTPS_PROXY=http://127.0.0.1:1 NO_PROXY=127.0.0.1
demo_capture_scene after 0 "$demo_runtime/after" https://api.openai.com/v1 \
  'After: hidden key entry and automatic verification' \
  DEMO_STAGE=success HTTPS_PROXY=http://127.0.0.1:1 NO_PROXY=127.0.0.1 \
  DEMO_API_URL="$demo_api_url" DEMO_ENTRY="$demo_runtime/entry.exp"
demo_stop_api
"$demo_python" - "$demo_output" <<'VALIDATE' > "$demo_output/validation.txt"
import json, pathlib, re, sys
output = pathlib.Path(sys.argv[1])
before = (output / 'before.txt').read_text()
after = ''.join((output / (name + '.txt')).read_text() for name in ('after-error', 'after'))
assert '$ openai setup admin' in before
assert "Unknown help topic. Did you mean 'openai safety'?" in before
assert 'Admin API key (hidden):' not in before
assert 'Admin access verified.' not in before
metadata = (output / 'metadata.txt').read_text()
assert 'before exit status: 3 (expected 3)' in metadata
assert 'after-error exit status: 1 (expected 1)' in metadata
assert 'after exit status: 0 (expected 0)' in metadata
assert 'openai setup admin' in after
assert 'https://platform.openai.com/settings/organization/admin-keys' in after
assert 'Admin API key (hidden):' in after
events = [json.loads(line) for line in (output / 'after.cast').read_text().splitlines()[1:]]
raw_after = ''.join(event[2] for event in events if event[1] == 'o')
assert 'Admin API key (hidden): ' in raw_after
assert 'Verifying admin access...' in after
assert 'Admin access verified. The project-list request succeeded.' in after
assert 'No key was saved. Later commands still need authentication.' in after
assert not re.search(r'\b(?:read|export|unset)\b', after)
for pattern in ('*.txt', '*.cast'):
    for path in output.glob(pattern):
        assert 'sk-admin-SYNTHETIC-demo-only' not in path.read_text(), path.name
assert json.loads((output / 'requests.json').read_text()) == [{'method': 'GET', 'path': '/v1/organization/projects?limit=1', 'authenticated': True, 'status': 200}]
print('PASS: one setup command, hidden synthetic key, one authenticated GET with limit=1, and successful verification')
VALIDATE
demo_assemble_capture 300 before after-error after
