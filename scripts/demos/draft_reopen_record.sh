#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: draft_reopen_record.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd -P)"
demo_root="$(cd "$demo_source/../.." && pwd -P)"
# The default scene supports main before draft restoration. The optional auth scene
# requires the reviewed compact-auth baseline 39e26577 or equivalent recovery support.
demo_draft_scene="${DEMO_DRAFT_SCENE:-unsent}"
case "$demo_draft_scene" in
  unsent) demo_expected_requests=0;;
  auth-reopen) demo_expected_requests=1;;
  *) echo 'DEMO_DRAFT_SCENE must be unsent or auth-reopen' >&2; exit 2;;
esac
source "$demo_root/scripts/demos/capture_and_render.sh"
demo_driver="$demo_source/draft_reopen_check.py"
demo_python="$(command -v python3)"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_python"
demo_window_size=80x24
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 --theme dracula --fps-cap 20 --last-frame-duration 2)
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
args=("$DEMO_BINARY" "$DEMO_CASE_OUTPUT" --case "$DEMO_DRAFT_SCENE" --scene)
if [ "$DEMO_BEFORE" = yes ]; then args+=(--before); fi
exec "$DEMO_PYTHON" -B -I "$DEMO_DRIVER" "${args[@]}"
SCENE
{
  echo 'feature: restore prompt and settings after controlled picker exit'
  echo "before source commit: $demo_before_sha"
  echo "after source commit/base: $demo_after_sha"
  echo "after source detail: ${DEMO_AFTER_SOURCE_DETAIL:-clean commit supplied above}"
  echo "scene: $demo_draft_scene"
  echo "fixture: $demo_expected_requests synthetic image POSTs per scene; no requests during reopen"
  echo 'auth-reopen baseline: compact-auth source 39e26577; use unsent for main without recovery'
  echo 'actions: edit prompt and quality, Ctrl+C, reopen; no automatic requests'
  echo 'scope: native zsh PTY and terminal-text replay; fake credentials and isolated HOME'
  demo_capture_metadata
  shasum -a 256 "$demo_driver" "$demo_root/scripts/demos/image_recovery_check.py"
} > "$demo_output/metadata.txt"
for scene in before after; do
  binary="$demo_after"; before=no
  if [ "$scene" = before ]; then binary="$demo_before"; before=yes; fi
  demo_capture_scene "$scene" 0 "$demo_runtime/after" 'http://127.0.0.1' "$scene" \
    PYTHONDONTWRITEBYTECODE=1 DEMO_PYTHON="$demo_python" DEMO_DRIVER="$demo_driver" \
    DEMO_BINARY="$binary" DEMO_CASE_OUTPUT="$demo_output/$scene-evidence" DEMO_BEFORE="$before" DEMO_DRAFT_SCENE="$demo_draft_scene"
  frame_time="$($demo_python - "$demo_output/$scene.cast" <<'FRAME'
import json,pathlib,sys
text=''
for row in pathlib.Path(sys.argv[1]).read_text().splitlines()[1:]:
 event=json.loads(row)
 if event[1]!='o': continue
 text+=event[2]
 marker='$ openai images generate  # reopen'
 if marker in text and 'Create image' in text.split(marker,1)[1]:
  print(event[0]+0.35)
  break
else: raise SystemExit('reopened draft frame missing')
FRAME
)"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select "$frame_time" "$demo_output/$scene.cast" "$demo_output/$scene-reopened.gif"
  "$demo_ffmpeg" -nostdin -hide_banner -loglevel error -y -i "$demo_output/$scene-reopened.gif" -frames:v 1 "$demo_output/$scene-reopened.png"
done
"$demo_python" - "$demo_output" "$demo_expected_requests" <<'COMPARE'
import json,pathlib,sys
root=pathlib.Path(sys.argv[1])
before=json.loads((root/'before-evidence/results.json').read_text())['cases'][0]
after=json.loads((root/'after-evidence/results.json').read_text())['cases'][0]
expected=int(sys.argv[2])
assert before['requests']==after['requests'] and len(after['requests'])==expected
assert len(before['events'])==len(after['events'])==expected
assert before['restored_after_exit'] is False and after['restored_after_exit'] is True
(root/'request-comparison.json').write_text(json.dumps(dict(requests_equal=True,before_image_posts=expected,after_image_posts=expected,reopen_requests=0,automatic_checks=0,automatic_retries=0),indent=2)+'\n')
COMPARE
demo_assemble_capture 200 before after
