#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
mkdir -p "$demo_runtime/fixture/demo-skill/assets"
"$demo_python" - "$demo_runtime/fixture" <<'PY'
import pathlib, sys, zipfile
root = pathlib.Path(sys.argv[1])
skill = root / 'demo-skill'
(skill / 'SKILL.md').write_text('---\nname: demo-skill\ndescription: Synthetic upload fixture.\n---\nUse the bundled helper.\n')
(skill / 'helper.sh').write_text('#!/bin/sh\nprintf "synthetic\\n"\n')
(skill / 'helper.sh').chmod(0o755)
(skill / 'assets' / 'bytes.bin').write_bytes(bytes(range(256)))
with zipfile.ZipFile(root / 'demo-skill.zip', 'w') as archive:
    for path in sorted(skill.rglob('*')):
        if path.is_file():
            archive.write(path, path.relative_to(root))
PY
demo_start_api "$demo_output/requests.jsonl" "$demo_runtime/fixture"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: Skills ZIP and directory upload repair'
  echo "baseline: $demo_before_sha"
  echo "candidate: $demo_after_sha"
  echo 'Synthetic localhost API; isolated Bash PTY; terminal replay, not graphical-terminal or live API acceptance.'
  demo_capture_metadata
  shasum -a 256 "$demo_source/server.py" "$demo_source/record.sh" "$demo_source/scene.sh"
} > "$demo_output/metadata.txt"
demo_columns="${SKILL_DEMO_COLUMNS:-100}"
demo_rows="${SKILL_DEMO_ROWS:-40}"
demo_theme="${SKILL_DEMO_THEME:-asciinema}"
[[ "$demo_columns" =~ ^[0-9]+$ && "$demo_rows" =~ ^[0-9]+$ ]]
((demo_columns >= 40 && demo_rows >= 40))
case "$demo_theme" in asciinema|github-light) ;; *) echo 'Unsupported demo theme.' >&2; exit 2;; esac
demo_window_size="${demo_columns}x${demo_rows}"
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme "$demo_theme" --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_expected_status=1
  demo_label='BEFORE: directory failure and damaged ZIP bytes'
  if [ "$demo_scene" = after ]; then
    demo_expected_status=0
    demo_label='AFTER: directory packaging and unchanged ZIP bytes'
  fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_label" \
    "HOME=$demo_runtime/$demo_scene-home" NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 \
    "DEMO_INPUT_DIR=$demo_runtime/fixture" "DEMO_EXPECTED_STATUS=$demo_expected_status"
done
demo_stop_api
"$demo_python" - "$demo_output/requests.jsonl" <<'PY'
import json, pathlib, sys
records = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines()]
assert len(records) == 6, records
assert all(not row['valid'] and not row['zip_bytes_equal'] for row in records[:2]), records
assert all(row['valid'] for row in records[2:]), records
assert all(row['zip_bytes_equal'] for row in records[4:]), records
print('PASS: baseline ZIP bytes differ; candidate directory contents and supplied ZIP bytes match.')
PY
demo_assemble_capture 300 before after
printf 'Recorded Skills upload comparison in %s\n' "$demo_output"
