#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-command-insertion.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA CANDIDATE_SHA OUTPUT_DIR' >&2
  echo 'Use the actual main binary for BEFORE_BINARY. Keep media outside the repository.' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
# Local help and command search need neither an API fixture nor credentials.
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" /usr/bin/true
demo_expect="$(command -v expect)"
demo_window_size=108x36
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3)
demo_before_digest="$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
demo_after_digest="$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
if [ "$demo_before_digest" = "$demo_after_digest" ]; then
  echo 'The baseline and candidate must be distinct builds.' >&2
  exit 2
fi

cat > "$demo_runtime/startup.zsh" <<'STARTUP'
PROMPT='$ '
RPROMPT=''
unset HISTFILE
SAVEHIST=0
bindkey -e
# The inner shell owns normal cooked-output translation between picker runs.
stty opost onlcr columns 108 rows 36
printf '\033[2J\033[H%s\n\n' "$DEMO_SCENE_LABEL"
demo_capture_buffer() {
  printf '%s\n' "$BUFFER" >> "$DEMO_BUFFERS"
}
zle -N demo_capture_buffer
bindkey '^X^G' demo_capture_buffer
bindkey '^X^R' openai_suggestion_restore
preexec() { printf '%s\n' "$1" >> "$DEMO_COMMANDS"; }
demo_record_startup() {
  printf '%s\n' "${__openai_suggestions_enabled-0}" >> "$DEMO_STARTUPS"
  add-zsh-hook -d precmd demo_record_startup
}
autoload -Uz add-zsh-hook
add-zsh-hook precmd demo_record_startup
STARTUP

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2
# The inner PTY already applies its own output processing. A second ONLCR pass
# would turn cursor-relative LF moves into CRLF and corrupt the captured frame.
# This is the recorder-owned outer PTY, not the user's terminal.
stty -opost
unset OPENAI_API_KEY OPENAI_ADMIN_KEY
exec "$DEMO_EXPECT" "$DEMO_DRIVER"
SCENE

{
  echo 'feature: shared command search and fill-only shell insertion'
  echo "before commit: $demo_before_sha (inspect build info for source identity)"
  echo "candidate commit: $demo_after_sha (inspect build info and source manifest for dirty state)"
  echo 'capture: actual binaries in isolated zsh PTYs, driven by Expect'
  echo 'relay: outer PTY output processing disabled; inner terminal bytes remain unchanged'
  echo 'setup: actual candidate installer in each synthetic HOME, followed by a fresh zsh process'
  echo 'scenes: baseline help; recent-help search and draft restoration; explore search and prompt insertion'
  echo 'data: local help/search only; no API/admin key; loopback-only base URL'
  echo 'scope: asciinema/agg terminal replay; not graphical-terminal or mouse-adapter validation'
  echo 'terminal background query: driver reports the replay renderer Dracula background (#282a36)'
  echo 'renderer: resvg preserves rounded border joins; scene advertises truecolor for the blue theme'
  echo 'picker screenshot: visible search frame; selection and cleanup continue after relay stops'
  echo "terminal dimensions: $demo_window_size"
  demo_capture_metadata
  /bin/zsh --version
  shasum -a 256 "$demo_source/record-command-insertion.sh" "$demo_source/command-insertion.exp"
} > "$demo_output/metadata.txt"
if command -v go >/dev/null; then
  go version -m "$demo_before" > "$demo_output/before-build-info.txt"
  go version -m "$demo_after" > "$demo_output/after-build-info.txt"
fi
if [ -n "${DEMO_SOURCE_MANIFEST:-}" ]; then
  cp "$DEMO_SOURCE_MANIFEST" "$demo_output/candidate-source-sha256.txt"
  shasum -a 256 "$demo_output/candidate-source-sha256.txt" >> "$demo_output/metadata.txt"
fi

for demo_scene in before after-recent after-explore after-picker; do
  demo_home="$demo_runtime/$demo_scene-home"
  mkdir "$demo_home"
  cp "$demo_runtime/startup.zsh" "$demo_home/.zshrc"
  : > "$demo_output/$demo_scene-buffers.txt"
  : > "$demo_output/$demo_scene-commands.txt"
  : > "$demo_output/$demo_scene-startups.txt"
  demo_directory="$demo_runtime/after"
  case "$demo_scene" in
    before)
      demo_directory="$demo_runtime/before"
      demo_label="Before: main ${demo_before_sha:0:8} | Read help, then copy or type a command"
      ;;
    after-recent) demo_label='After: Ctrl+X, then Tab | Search suggested commands and fill the prompt';;
    *) demo_label='After: openai explore | Search all commands and fill the prompt';;
  esac
  if [ "$demo_scene" != before ]; then
    env -i HOME="$demo_home" ZDOTDIR="$demo_home" XDG_CONFIG_HOME="$demo_home/.config" \
      PATH="$demo_directory:/usr/bin:/bin" OPENAI_BASE_URL=http://127.0.0.1:1 \
      "$demo_directory/openai" --format json @completion zsh --install-suggestions \
      --profile "$demo_home/.zshrc" > "$demo_output/$demo_scene-setup.json"
  fi
  demo_capture_scene "$demo_scene" 0 "$demo_directory" http://127.0.0.1:1 "$demo_label" \
    "COLORTERM=truecolor" \
    "HOME=$demo_home" "ZDOTDIR=$demo_home" "XDG_CONFIG_HOME=$demo_home/.config" \
    "TMPDIR=$demo_home" "DEMO_MODE=$demo_scene" "DEMO_EXPECT=$demo_expect" \
    "DEMO_DRIVER=$demo_source/command-insertion.exp" \
    "DEMO_STARTUPS=$demo_output/$demo_scene-startups.txt" \
    "DEMO_BUFFERS=$demo_output/$demo_scene-buffers.txt" \
    "DEMO_COMMANDS=$demo_output/$demo_scene-commands.txt"
  case "$demo_scene" in
    before)
      printf '\n' > "$demo_runtime/expected-buffers"
      printf 'openai models --help\nexit\n' > "$demo_runtime/expected-commands"
      printf '0\n' > "$demo_runtime/expected-startups"
      ;;
    after-recent)
      printf 'openai models retrieve --help\nopenai --version\n' > "$demo_runtime/expected-buffers"
      printf 'openai models --help\nexit\n' > "$demo_runtime/expected-commands"
      printf '1\n' > "$demo_runtime/expected-startups"
      ;;
    *)
      printf 'openai models --help\n' > "$demo_runtime/expected-buffers"
      printf 'openai explore\nexit\n' > "$demo_runtime/expected-commands"
      printf '1\n' > "$demo_runtime/expected-startups"
      ;;
  esac
  diff -u "$demo_runtime/expected-buffers" "$demo_output/$demo_scene-buffers.txt"
  diff -u "$demo_runtime/expected-commands" "$demo_output/$demo_scene-commands.txt"
  diff -u "$demo_runtime/expected-startups" "$demo_output/$demo_scene-startups.txt"
  if [ "$demo_scene" != before ]; then
    env -i HOME="$demo_home" ZDOTDIR="$demo_home" XDG_CONFIG_HOME="$demo_home/.config" \
      PATH="$demo_directory:/usr/bin:/bin" \
      "$demo_directory/openai" --format json @completion zsh --suggestions-status \
      --profile "$demo_home/.zshrc" > "$demo_output/$demo_scene-status.json"
    printf '%s\n' '{"action":"status","shell":"zsh","profiles":1,"installed":1,"changed":false,"needs_update":false,"double_tab":false}' > "$demo_runtime/expected-status"
    diff -u "$demo_runtime/expected-status" "$demo_output/$demo_scene-status.json"
  fi
done

test "$demo_before_digest" = "$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
test "$demo_after_digest" = "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
cp "$demo_runtime/startup.zsh" "$demo_output/startup.zsh"
cp "$demo_source/command-insertion.exp" "$demo_output/command-insertion.exp"
demo_assemble_capture 250 before after-recent after-explore
cp "$demo_output/after-picker.png" "$demo_output/after.png"
shasum -a 256 "$demo_output/"*.png "$demo_output/comparison.gif" > "$demo_output/media-sha256.txt"
echo 'Assertions passed: exact editable buffers; previous draft restored; no selected command submitted; isolated setup intact.' >> "$demo_output/metadata.txt"
printf 'Recorded command picker comparison in %s\n' "$demo_output"
printf 'PR media: comparison.gif, before.png, after.png; fill evidence: after-recent.png and after-explore.png\n'
