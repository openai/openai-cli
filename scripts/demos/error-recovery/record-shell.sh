#!/bin/bash
set -euo pipefail
if [ "$#" -ne 6 ]; then
  echo 'usage: record-shell.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR normal|narrow' >&2
  exit 2
fi
root="$(cd "$(dirname "$0")/../../.." && pwd -P)"
source "$root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$root" "$1" "$2" "$3" "$4" "$5" /usr/bin/true
test "$(git -C "$root" rev-parse HEAD)" = "$demo_after_sha"
test -z "$(git -C "$root" status --porcelain --untracked-files=no)"
shasum -a 256 "$demo_before" "$demo_after" /bin/zsh "$0" > "$demo_output/source-hashes-before.txt"
case "$6" in
  normal) demo_window_size=80x24; theme=dracula ;;
  narrow) demo_window_size=40x30; theme=solarized-light ;;
  *) echo 'choose normal or narrow' >&2; exit 2 ;;
esac
demo_render_options=(--font-family Menlo --font-size 18 --theme "$theme" --last-frame-duration 2)
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
: > "$DEMO_STATUS_FILE"
cd "$HOME"
printf '%s\n\n' "$DEMO_SCENE_LABEL"
if [ "$DEMO_PHASE" = after ]; then
  printf '$ source <(openai @completion zsh --suggest-executable)\n\n'
  openai @completion zsh --suggest-executable > generated.zsh
else
  openai @completion zsh > generated.zsh
fi
# Generate with the real CLI, then use a sentinel to detect command replay.
mkdir bin
printf '#!/bin/sh\n: > "$HOME/backend-called"\nexit 99\n' > bin/openai
chmod 700 bin/openai
export PATH="$HOME/bin:/usr/bin:/bin"
exec /bin/zsh -f -i ./scene.zsh
SCENE
cat > "$demo_runtime/scene.zsh" <<'SCENE'
HISTFILE=/dev/null
if [[ $DEMO_PHASE == before ]]; then
  autoload -Uz compinit
  compinit -D -i
fi
source ./generated.zsh || exit 1
printf '$ opneai images generate\n'
opneai images generate
result=$?
printf 'executable-typo\t%s\n' "$result" >> "$DEMO_STATUS_FILE" || exit 1
[[ $result == 127 && ! -e ./backend-called ]] || exit 1
printf '\n'
if [[ $DEMO_PHASE == after ]]; then
  printf '$ openai_suggestions_disable\n'
  openai_suggestions_disable || exit 1
  (( ! $+functions[command_not_found_handler] )) || exit 1
fi
sleep 1
exit 0
SCENE
demo_capture_metadata > "$demo_output/metadata.txt"
/bin/zsh --version >> "$demo_output/metadata.txt"
shasum -a 256 /bin/zsh "$0" >> "$demo_output/metadata.txt"
printf 'Before source: %s\nAfter source: %s\nProfile: %s\nTerminal replay; isolated zsh; sentinel executable; no API command executed. No loopback request counter runs in this replay.\n' "$3" "$4" "$6" >> "$demo_output/metadata.txt"
printf 'executable-typo\t127\n' > "$demo_runtime/expected-statuses.tsv"
for phase in before after; do
  mkdir "$demo_runtime/home-$phase"
  cp "$demo_runtime/scene.zsh" "$demo_runtime/home-$phase/scene.zsh"
  demo_capture_scene "$phase" 0 "$demo_runtime/$phase" http://127.0.0.1:1 \
    "$phase: executable typo (terminal replay)" \
    HOME="$demo_runtime/home-$phase" ZDOTDIR="$demo_runtime/home-$phase" \
    HISTFILE=/dev/null DEMO_PHASE="$phase" NO_COLOR=1 GOMAXPROCS=2 \
    DEMO_STATUS_FILE="$demo_output/$phase-statuses.tsv"
  cmp "$demo_runtime/expected-statuses.tsv" "$demo_output/$phase-statuses.tsv"
  if grep -qF '[exit' "$demo_output/$phase.txt"; then exit 1; fi
  test ! -e "$demo_runtime/home-$phase/backend-called"
done
grep -F 'command not found: opneai' "$demo_output/before.txt"
if grep -F 'Did you mean:' "$demo_output/before.txt"; then exit 1; fi
grep -F 'Command not found. Did you mean: openai?' "$demo_output/after.txt"
grep -F 'Keep the remaining arguments unchanged.' "$demo_output/after.txt"
demo_assemble_capture 250 before after
test "$(git -C "$root" rev-parse HEAD)" = "$demo_after_sha"
test -z "$(git -C "$root" status --porcelain --untracked-files=no)"
shasum -a 256 "$demo_before" "$demo_after" /bin/zsh "$0" > "$demo_output/source-hashes-after.txt"
cmp "$demo_output/source-hashes-before.txt" "$demo_output/source-hashes-after.txt"
