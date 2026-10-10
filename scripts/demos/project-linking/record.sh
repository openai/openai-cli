#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
demo_platform="$(uname -s)"
if [ "$demo_platform" = Darwin ]; then demo_platform=macOS; fi
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: folder project linking'
  echo "comparison base / before commit: $demo_before_sha"
  echo "candidate / after commit: $demo_after_sha"
  echo 'capture: terminal replay; synthetic loopback API; isolated Bash PTYs and temporary homes'
  echo 'scope: no native graphical terminal, live API, or project-access validation'
  echo 'render: 100 columns x 26 rows; Menlo 18px; asciinema theme'
  echo 'before: link is unavailable and returns status 3'
  echo 'after: link, inspect, remote files list, and unlink return status 0'
  echo 'fixture: file_remote_proj_work proves the received synthetic project header'
  echo 'privacy: request evidence excludes credentials and other headers'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/server.py" "$demo_source/scene.sh" "$demo_source/record.sh"
} > "$demo_output/metadata.txt"
if [ -n "${DEMO_SOURCE_MANIFEST:-}" ]; then
  cp "$DEMO_SOURCE_MANIFEST" "$demo_output/source-manifest.txt"
fi
demo_window_size=100x26
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after; do
  demo_home="$demo_runtime/$demo_scene-home"
  demo_work="$demo_runtime/$demo_scene-work"
  mkdir -p "$demo_home" "$demo_work/work-chatbot"
  demo_label='Before: no saved folder project'
  if [ "$demo_scene" = after ]; then demo_label='After: this folder uses proj_work'; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" \
    "$demo_api_url/$demo_scene/v1" "$demo_label" \
    "HOME=$demo_home" "USERPROFILE=$demo_home" "APPDATA=$demo_home" "XDG_CONFIG_HOME=$demo_home" \
    NO_COLOR=1 FORCE_COLOR=0 CI=1 GOMAXPROCS=2 PAGER=cat \
    "DEMO_SCENE=$demo_scene" "DEMO_PLATFORM=$demo_platform" "DEMO_WORK_ROOT=$demo_work" \
    "DEMO_STATUS_LOG=$demo_output/statuses.tsv"
done
demo_stop_api
"$demo_python" -I -B - "$demo_output" <<'PY' | tee "$demo_output/validation.txt"
import json
import pathlib
import sys

output = pathlib.Path(sys.argv[1])
statuses = (output / "statuses.tsv").read_text().splitlines()
expected = ["before\tlink\t3", "after\tlink\t0", "after\tinspect\t0",
            "after\tfiles\t0", "after\tunlink\t0"]
assert statuses == expected, statuses
requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
assert requests == [{"method": "GET", "path": "/after/v1/files", "project": "proj_work",
                     "file_id": "file_remote_proj_work", "status": 200}], requests
for scene in ("before", "after"):
    text = (output / (scene + ".txt")).read_text()
    assert "Terminal replay" in text and "synthetic remote Files API" in text
    assert "$ cd work-chatbot" in text
    assert "$ openai link --project proj_work" in text
    assert "synthetic-demo-key" not in text
    if scene == "after":
        assert "file_remote_proj_work" in text
        assert "remote-project-notes.txt" in text
        assert "$ openai unlink" in text
        assert "This folder has no saved project link." in text
        assert len(text.splitlines()) <= 26, "candidate scene exceeds the terminal height"
print("PASS: actual statuses match; exactly one remote Files request used proj_work.")
print("PASS: baseline linking failed; candidate linked, inspected, listed remote files, and unlinked.")
print("PASS: transcript contains the synthetic file ID and fits the 26-row terminal.")
PY
demo_assemble_capture 300 before after
printf 'Recorded folder-linking terminal replays in %s\n' "$demo_output"
