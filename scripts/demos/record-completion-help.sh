#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-completion-help.sh BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
# Completion help runs locally. The shared recorder needs no API fixture here.
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" /usr/bin/true
demo_window_size=90x12
demo_render_options=(--font-family Menlo --font-size 16 --line-height 1.2 --theme dracula --last-frame-duration 2)

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
if [ -z "${ZSH_VERSION:-}" ]; then exec /bin/zsh -f "$DEMO_SCENE_SCRIPT"; fi
set -euo pipefail
test -t 0 && test -t 1 && test -t 2
unset OPENAI_API_KEY
printf '\033[2J\033[H%s  $ openai @completion --help\n' "$DEMO_SCENE_LABEL"
sleep 0.5
openai @completion --help
printf '%s  $ ' "$DEMO_SCENE_LABEL"
sleep 2
SCENE

{
  echo "Baseline: $demo_before_sha"
  echo "Candidate: $demo_after_sha"
  echo 'Command: openai @completion --help'
  echo 'zsh PTY, 90 columns by 12 rows, dark truecolor, Menlo 16px.'
  /bin/zsh --version
  demo_capture_metadata
} > "$demo_output/metadata.txt"
for scene in before after; do
  if [ "$scene" = before ]; then label=Before; else label=After; fi
  mkdir -p "$demo_runtime/home-$scene"
  demo_capture_scene "$scene" 0 "$demo_runtime/$scene" 'http://127.0.0.1:1' "$label" \
    HOME="$demo_runtime/home-$scene" XDG_CONFIG_HOME="$demo_runtime/home-$scene/config" \
    SHELL=/bin/zsh COLORTERM=truecolor COLORFGBG='15;0' OPENAI_CLI_NO_UPDATE_CHECK=1
done
grep -Fq -- '--debug-body' "$demo_output/before.txt"
grep -Fq 'openai explore' "$demo_output/after.txt"
grep -Eq 'help --all "?@completion"?' "$demo_output/after.txt"
if grep -Eq -- '--(debug-body|install-suggestions|automatic)' "$demo_output/after.txt"; then exit 1; fi
demo_assemble_capture 200 before after
