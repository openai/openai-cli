#!/bin/bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo 'usage: prepare-command-insertion-trial.sh CANDIDATE_BINARY OUTPUT_DIR' >&2
  exit 2
fi
trial_binary="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
test -x "$trial_binary"
umask 077
mkdir "$2"
trial_directory="$(cd "$2" && pwd)"
mkdir "$trial_directory/bin" "$trial_directory/runs"
cp "$trial_binary" "$trial_directory/bin/openai"

cat > "$trial_directory/startup.zsh" <<'STARTUP'
PROMPT='trial $ '
RPROMPT=''
unset HISTFILE
SAVEHIST=0
bindkey -e
autoload -Uz compinit
compinit -D
source "$INSERTION_TRIAL_ROOT/completion.zsh"
autoload -Uz add-zsh-hook add-zle-hook-widget

# Observe only registered insertion events. Do not record ordinary draft typing.
insertion_trial_observe() {
  emulate -L zsh
  if (( ${__openai_suggestions_index-0} > 0 )) &&
      [[ $BUFFER == ${__openai_suggestions_last-} && $BUFFER != ${__insertion_trial_recorded-} ]]; then
    print -r -- "$BUFFER" > "$INSERTION_TRIAL_RUN/inserted.txt"
    print -r -- "$CURSOR" > "$INSERTION_TRIAL_RUN/cursor.txt"
    typeset -g __insertion_trial_recorded=$BUFFER
  fi
  return 0
}

insertion_trial_submitted() {
  emulate -L zsh
  local suggestion
  # Record known suggestions literally. Other input gets a fixed event label.
  for suggestion in "${__openai_suggestions[@]}"; do
    if [[ $1 == $suggestion ]]; then
      print -r -- "$suggestion" >> "$INSERTION_TRIAL_RUN/submitted.txt"
      return 0
    fi
  done
  print -r -- '[another command was submitted]' >> "$INSERTION_TRIAL_RUN/submitted.txt"
}

insertion_trial_ready() {
  emulate -L zsh
  add-zsh-hook -d precmd insertion_trial_ready
  add-zle-hook-widget line-pre-redraw insertion_trial_observe
  openai models --help
  print
  if (( ${#__openai_suggestions} > 0 )); then
    print -r -- 'TRY NOW: Press Ctrl+X, release it, then press Tab.'
    print -r -- 'Type retrieve to filter. Press Enter to fill the selected command.'
    print -r -- 'After the picker closes, do not press Enter again. Nothing should run yet.'
  else
    print -r -- 'The trial did not capture suggestions. Report this message before continuing.'
  fi
  print -r -- 'Run openai explore to search all commands and examples.'
  print -r -- 'Escape cancels the picker. Ctrl+X, then R restores the previous draft.'
  print -r -- 'To leave: Esc closes the picker. Ctrl+C clears the prompt.'
  print -r -- 'Then run exit to return to your normal shell.'
  print -r -- "${__openai_suggestions_enabled-0} ${#__openai_suggestions}" > "$INSERTION_TRIAL_RUN/ready.txt"
}
bindkey '^Xr' openai_suggestion_restore
add-zsh-hook preexec insertion_trial_submitted
add-zsh-hook precmd insertion_trial_ready
STARTUP

# This is a trusted completion artifact from the reviewed binary, not model output.
env -i PATH="$trial_directory/bin:/usr/bin:/bin" CI=1 \
  "$trial_directory/bin/openai" @completion zsh > "$trial_directory/completion.zsh"

cat > "$trial_directory/launch.sh" <<'LAUNCH'
#!/bin/bash
set -euo pipefail
trial_directory="$(cd "$(dirname "$0")" && pwd)"
if [ ! -t 0 ] || [ ! -t 1 ] || [ ! -t 2 ] || [ "${TERM:-dumb}" = dumb ]; then
  echo 'Run this trial from an interactive terminal with TERM configured.' >&2
  exit 2
fi
umask 077
trial_run="$(mktemp -d "$trial_directory/runs/run.XXXXXXXX")"
trial_home="$trial_run/home"
mkdir "$trial_home" "$trial_run/work"
cp "$trial_directory/startup.zsh" "$trial_home/.zshrc"
touch "$trial_run/inserted.txt" "$trial_run/submitted.txt" "$trial_run/cursor.txt"
printf '%s\n' "$trial_run" > "$trial_directory/latest-run.$$"
mv -f "$trial_directory/latest-run.$$" "$trial_directory/latest-run.txt"
trial_cleanup() { rm -rf -- "$trial_home"; }
trap trial_cleanup EXIT
env -i HOME="$trial_home" ZDOTDIR="$trial_home" XDG_CONFIG_HOME="$trial_home/.config" \
  PATH="$trial_directory/bin:/usr/bin:/bin:/usr/sbin:/sbin" CI=1 \
  OPENAI_BASE_URL=http://127.0.0.1:1 \
  "$trial_directory/bin/openai" @completion zsh --install-suggestions --profile "$trial_home/.zshrc" \
  > "$trial_run/setup.txt"
cd "$trial_run/work"
env -i HOME="$trial_home" ZDOTDIR="$trial_home" XDG_CONFIG_HOME="$trial_home/.config" \
  PATH="$trial_directory/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
  TERM="${TERM:-xterm-256color}" LC_ALL=en_US.UTF-8 CI=1 \
  COLORTERM="${COLORTERM:-}" NO_COLOR="${NO_COLOR:-}" \
  CLICOLOR="${CLICOLOR:-}" CLICOLOR_FORCE="${CLICOLOR_FORCE:-}" \
  INSERTION_TRIAL_ROOT="$trial_directory" INSERTION_TRIAL_RUN="$trial_run" \
  OPENAI_BASE_URL=http://127.0.0.1:1 \
  /bin/zsh -d -i
LAUNCH
chmod 700 "$trial_directory/launch.sh"

cat > "$trial_directory/README.txt" <<'GUIDE'
OpenAI CLI searchable command picker trial

Run launch.sh as a command in an existing terminal. Do not source it.
The trial opens a child zsh with isolated startup files and a private CLI copy.
It installs managed keyboard insertion only inside that temporary home.
Ordinary CLI Tab completion is configured separately for this trial.
Your personal profiles and installed CLI stay unchanged.

At the trial prompt:
1. Press Ctrl+X.
2. Release Ctrl.
3. Press Tab.
4. Type retrieve to filter the suggestions.
5. Press Enter to fill the selected command.

The picker closes and the prompt contains the selected command.
Do not press Enter again yet. Selection must not run the command.
Escape cancels the picker. Ctrl+X, then R restores the original draft.
Run openai explore to search all command definitions and examples.
Enter is a separate submission action. Use local help commands only.
To try another feature, clear the prompt and submit openai files --help.
To leave:
1. Press Esc to close the picker.
2. Press Ctrl+C to clear the prompt.
3. Run exit to return to your normal shell.

Each launch gets a new runs/run.* directory, listed in latest-run.txt.
inserted.txt records only the last registered suggestion inserted by the widget.
It does not record ordinary draft typing.
submitted.txt records matching suggestions submitted with Enter.
Other submissions use a fixed event label without retaining their command text.
Startup help is automatic and is not a user submission.
The home/profile files are removed on normal exit. Evidence files remain.

The child receives no API credentials. Its API endpoint is a closed loopback port.
This is a keyboard trial. It does not launch or control Kitty or alter mouse gestures.
GUIDE

shasum -a 256 "$trial_directory/bin/openai" "$trial_directory/startup.zsh" \
  "$trial_directory/completion.zsh" "$trial_directory/launch.sh" > "$trial_directory/source.sha256"
printf 'Prepared isolated keyboard trial: %s\n' "$trial_directory/launch.sh"
