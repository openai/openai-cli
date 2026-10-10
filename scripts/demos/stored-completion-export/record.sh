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
demo_before_digest="$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
demo_after_digest="$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
demo_start_api "$demo_output/requests.jsonl" "$demo_output/fixture.json"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
exec "$DEMO_PYTHON" -I -B "$DEMO_DRIVER"
SCENE
{
  echo 'feature: explicit stored-completion JSONL export'
  echo "before source commit: $demo_before_sha"
  echo "after source commit: $demo_after_sha"
  echo "after source state: ${DEMO_AFTER_SOURCE_STATE:-committed source supplied by caller}"
  echo 'before: existing list autopagination, explicit JSONL and shell redirection'
  echo 'after: explicit export destination, all-page receipt, existing-file refusal and empty result'
  echo 'fixture: three synthetic completions across two pages; no input-message requests'
  echo 'checks: exact saved bytes, statuses, page requests, existing-file preservation and stage cleanup'
  echo 'capture: shared lifecycle; real binaries; inherited terminal stderr; separate temporary homes'
  echo 'render: 92 columns x 20 rows; Menlo 20px; asciinema theme'
  echo 'scope: terminal replay; no live API or graphical terminal validation'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_python" "$demo_ffmpeg" "$demo_ffprobe" /bin/bash
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_source/record.sh" "$demo_source/scene.py" "$demo_source/server.py" \
  "$demo_root/scripts/demos/capture_and_render.sh" > "$demo_output/source-sha256.txt"
if [ -n "${DEMO_SOURCE_MANIFEST:-}" ]; then
  cp "$DEMO_SOURCE_MANIFEST" "$demo_output/source-manifest.txt"
fi
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
demo_window_size=92x20
demo_render_options=(--renderer resvg --font-family Menlo --font-size 20 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after existing empty; do
  demo_command_dir="$demo_runtime/after"
  demo_status=0
  case "$demo_scene" in
    before) demo_label='Before | list already supports complete JSONL export'; demo_command_dir="$demo_runtime/before";;
    after) demo_label='After | explicit destination and completed-export receipt';;
    existing) demo_label='After | existing files stay unchanged'; demo_status=1;;
    empty) demo_label='After | zero matches produce an empty JSONL file';;
  esac
  demo_home="$demo_runtime/home-$demo_scene"
  demo_data="$demo_output/data/$demo_scene"
  mkdir -p "$demo_home" "$demo_data"
  demo_capture_scene "$demo_scene" "$demo_status" "$demo_command_dir" "$demo_api_url/$demo_scene/v1" "$demo_label" \
    "DEMO_PYTHON=$demo_python" "DEMO_DRIVER=$demo_source/scene.py" "DEMO_SCENE=$demo_scene" \
    "DEMO_DATA_DIR=$demo_data" "DEMO_FIXTURE=$demo_output/fixture.json" "DEMO_EVIDENCE=$demo_output/$demo_scene-evidence.json" \
    "HOME=$demo_home" "XDG_CONFIG_HOME=$demo_home/config" NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2
done
demo_stop_api
test "$demo_before_digest" = "$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
test "$demo_after_digest" = "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
"$demo_python" -I -B - "$demo_output" <<'PY'
import json, pathlib, sys
output = pathlib.Path(sys.argv[1])
requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
assert [(item["scene"], item["page"], item["records"], item["status"]) for item in requests] == [
    ("before", 1, 2, 200), ("before", 2, 1, 200),
    ("after", 1, 2, 200), ("after", 2, 1, 200), ("empty", 1, 0, 200)], requests
assert [item["response_sha256"] for item in requests[:2]] == [item["response_sha256"] for item in requests[2:4]]
for scene in ("before", "after", "existing", "empty"):
    evidence = json.loads((output / f"{scene}-evidence.json").read_text())
    assert evidence["exit_status"] == (1 if scene == "existing" else 0), evidence
    transcript = (output / f"{scene}.txt").read_text()
    if scene in {"after", "empty"}:
        count = 3 if scene == "after" else 0
        assert evidence["complete_records"] == count, evidence
        assert "Saved completions.jsonl" in transcript, transcript
        assert f"Stored completions: {count}" in transcript, transcript
        assert "All pages fetched." in transcript, transcript
    elif scene == "existing":
        assert evidence["destination_preserved"], evidence
        assert "destination already exists" in transcript, transcript
        assert "All pages fetched." not in transcript, transcript
    else:
        assert evidence["complete_records"] == 3, evidence
        assert "Saved " not in transcript and "All pages fetched." not in transcript, transcript
before = (output / "data/before/completions.jsonl").read_bytes()
after = (output / "data/after/completions.jsonl").read_bytes()
assert before == after
(output / "validation.txt").write_text(
    "PASS: before and after preserve identical complete records across two API pages.\n"
    "PASS: exact statuses, receipts, existing-file preservation, empty file and stage cleanup.\n"
    "PASS: only synthetic loopback requests; no request for an existing destination.\n")
PY
demo_assemble_capture 200 before after existing empty
shasum -a 256 "$demo_output/"*.png "$demo_output/comparison.gif" > "$demo_output/media-sha256.txt"
printf 'Recorded stored-completion export in %s\n' "$demo_output"
