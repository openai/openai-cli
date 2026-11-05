#!/bin/bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo 'usage: prepare-command-click.sh CANDIDATE_BINARY OUTPUT_DIR' >&2
  exit 2
fi
demo_binary="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
test -x "$demo_binary"
umask 077
mkdir "$2"
demo_directory="$(cd "$2" && pwd)"
mkdir "$demo_directory/bin" "$demo_directory/config" "$demo_directory/cache" \
  "$demo_directory/runtime" "$demo_directory/home"
touch "$demo_directory/buffers.txt" "$demo_directory/cursors.txt" "$demo_directory/submitted.txt"
cp "$demo_binary" "$demo_directory/bin/openai"
env -i HOME="$demo_directory/home" PATH="$demo_directory/bin:/usr/bin:/bin" \
  "$demo_directory/bin/openai" @completion --suggestions-adapter kitty > "$demo_directory/config/openai_suggestions.py"
env -i HOME="$demo_directory/home" PATH="$demo_directory/bin:/usr/bin:/bin" \
  "$demo_directory/bin/openai" @completion zsh --suggestions --suggestions-click > "$demo_directory/integration.zsh"

cat > "$demo_directory/config/kitty.conf" <<'CONFIG'
font_family Menlo
font_size 16
initial_window_width 110c
initial_window_height 32c
remember_window_size no
allow_remote_control no
allow_hyperlinks yes
shell_integration disabled
mouse_map ctrl+shift+left release grabbed,ungrabbed mouse_handle_click link
CONFIG
cat > "$demo_directory/config/open-actions.conf" <<'ACTIONS'
protocol openai-insert
action kitten openai_suggestions.py ${URL}
ACTIONS

cat > "$demo_directory/home/.zshrc" <<'STARTUP'
PROMPT='$ '
RPROMPT=''
unset HISTFILE
SAVEHIST=0
bindkey -e
autoload -Uz compinit
compinit -D
source "$OPENAI_CLICK_DEMO/integration.zsh"
demo_capture_buffer() {
  print -r -- "$BUFFER" >> "$OPENAI_CLICK_DEMO/buffers.txt"
  print -r -- "$CURSOR" >> "$OPENAI_CLICK_DEMO/cursors.txt"
  zle -M 'Recorded the editable command line without submitting it'
}
zle -N demo_capture_buffer
bindkey '^X^G' demo_capture_buffer
bindkey '^X^R' openai_suggestion_restore
demo_record_command() { print -r -- "$1" >> "$OPENAI_CLICK_DEMO/submitted.txt"; }
autoload -Uz add-zsh-hook
add-zsh-hook preexec demo_record_command
print 'Manual Kitty click test. Ctrl-X Ctrl-G records the editable command line.'
print 'Ctrl-X Ctrl-R restores the draft. Use local help commands only.'
openai models --help
STARTUP

cat > "$demo_directory/launch.sh" <<'LAUNCH'
#!/bin/bash
set -euo pipefail
if [ "$#" -ne 1 ]; then
  echo 'usage: launch.sh ABSOLUTE_KITTY_EXECUTABLE' >&2
  exit 2
fi
demo_kitty="$1"
test -x "$demo_kitty"
demo_directory="$(cd "$(dirname "$0")" && pwd)"
cd "$demo_directory"
exec env -i \
  HOME="$demo_directory/home" ZDOTDIR="$demo_directory/home" \
  USER="$(id -un)" LOGNAME="$(id -un)" \
  PATH="$demo_directory/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
  LANG=en_US.UTF-8 TERM=xterm-kitty \
  KITTY_CONFIG_DIRECTORY="$demo_directory/config" \
  KITTY_CACHE_DIRECTORY="$demo_directory/cache" \
  KITTY_RUNTIME_DIRECTORY="$demo_directory/runtime" \
  XDG_CONFIG_HOME="$demo_directory/config" XDG_CACHE_HOME="$demo_directory/cache" \
  OPENAI_CLICK_DEMO="$demo_directory" OPENAI_BASE_URL=http://127.0.0.1:1 \
  "$demo_kitty" --config "$demo_directory/config/kitty.conf" \
  --title 'OpenAI command click test' /bin/zsh -d -i
LAUNCH
chmod 700 "$demo_directory/launch.sh"

cat > "$demo_directory/README.md" <<'RECIPE'
# Manual Kitty click test

This fixture uses isolated configuration, cache, runtime files, and shell startup.
The fixture does not change personal settings.
The fixture contains no API key and directs API requests to a closed local port.
Use only local help commands during this test.

Run `launch.sh` with the absolute Kitty executable path.
The preparation script does not launch Kitty.

## Click insertion

1. Type `openai --version` without pressing Enter.
2. Ctrl+Shift+click the Key setup command in the Models help.
3. Verify that the prompt contains `openai help setup`.
4. Press Ctrl+X, Ctrl+G to record the editable command line.
5. Verify that `submitted.txt` contains no setup command.
6. Press Ctrl+X, Ctrl+R to restore `openai --version`.

## Keyboard fallback

1. Press Ctrl+X, then Tab to open the recent-command picker.
2. Use the arrow keys to select a Models help command.
3. Press Enter to fill the prompt without running the command.
4. Verify that the picker closed and the selected command remains editable.
5. Press Ctrl+X, Ctrl+G to record the editable command line.
6. Verify that `submitted.txt` contains no selected command.
7. Press Ctrl+X, Ctrl+R to restore the draft.
8. Press Ctrl+U to clear the draft.

## Stale links

1. Submit `openai files --help`.
2. Type `openai --version` without pressing Enter.
3. Ctrl+Shift+click the earlier Key setup command from Models help.
4. Verify that the draft remains `openai --version`.
5. Ctrl+Shift+click the current Key setup command from Files help.
6. Verify that the prompt contains `openai help setup`.
7. Press Ctrl+X, Ctrl+G to record the editable command line.
8. Press Ctrl+X, Ctrl+R to restore the draft.
9. Press Ctrl+U to clear the draft.

## Busy shell

1. Submit `sleep 8`.
2. Ctrl+Shift+click the Files Key setup command while sleep runs.
3. Wait for the next prompt.
4. Verify that the prompt remains empty.
5. Press Ctrl+X, Ctrl+G to record the editable command line.

## Separate submission

1. Submit `openai batches --help`.
2. Ctrl+Shift+click its Key setup command.
3. Verify that no command runs.
4. Press Ctrl+X, Ctrl+G to record the editable command line.
5. Press Enter to display the local setup guide.
6. Verify that `submitted.txt` records `openai help setup` only after Enter.

## Evidence

Record the Kitty version and each observed result in `results.md`.
Keep screenshots outside Git.
Use `buffers.txt`, `cursors.txt`, and `submitted.txt` to distinguish insertion from submission.
Close the fixture's Kitty window before removing its directory.

This recipe is not proof that native mouse dispatch passed.
The current automated environment blocks Kitty control through its computer-use tool.
Native loader execution and native mouse dispatch remain unverified.
RECIPE

{
  echo 'Prepared manual fixture only; Kitty was not launched.'
  echo 'Fixture data: local help; no API credentials; loopback API endpoint.'
  shasum -a 256 "$demo_directory/bin/openai" "$demo_directory/config/openai_suggestions.py" \
    "$demo_directory/integration.zsh"
} > "$demo_directory/metadata.txt"
printf 'Prepared manual click fixture: %s\n' "$demo_directory"
