# Optional command insertion. Ctrl-X, then Tab searches displayed commands.
# Existing bindings remain untouched. Selection never executes a command.
# Double Tab is a separate opt-in and can delay ordinary Tab completion.
if [[ -o interactive && -o zle && -t 0 && -t 1 && -t 2 && ${TERM-} != dumb ]]; then
  # Re-sourcing keyboard-only integration disables a prior click transport.
  (( $+functions[____APPNAME___suggestions_click_disable] )) && ____APPNAME___suggestions_click_disable
  ____APPNAME___suggestions_owns_command() {
    [[ -n ${____APPNAME___suggestions_wrapper_body-} &&
      ${functions[__APPNAME__]-} == $____APPNAME___suggestions_wrapper_body &&
      -z ${aliases[__APPNAME__]-} ]]
  }

  ____APPNAME___suggestions_clear() {
    (( $+functions[____APPNAME___suggestions_click_clear] )) && ____APPNAME___suggestions_click_clear
    typeset -ga ____APPNAME___suggestions=()
    typeset -g ____APPNAME___suggestions_index=0 ____APPNAME___suggestions_last=''
  }

  ____APPNAME___suggestions_collect() {
    emulate -L zsh
    local code data line
    ____APPNAME___suggestions_started=1
    # FD8 and FD9 have independent offsets. Unlink before running the command.
    if ! command rm -f -- "$1" 2>/dev/null; then
      shift
      OPENAI_CLI_COMMANDS_FD= OPENAI_CLI_COMMANDS_SHELL= OPENAI_CLI_COMMANDS_CLICK_SESSION= OPENAI_CLI_COMMANDS_CLICK_GENERATION= command '__APPNAME__' "$@" 9>&-
      return $?
    fi
    shift
    local click_session='' click_generation=''
    (( $+functions[____APPNAME___suggestions_click_prepare] )) && ____APPNAME___suggestions_click_prepare
    if OPENAI_CLI_COMMANDS_FD=9 OPENAI_CLI_COMMANDS_SHELL=zsh OPENAI_CLI_COMMANDS_CLICK_SESSION=$click_session OPENAI_CLI_COMMANDS_CLICK_GENERATION=$click_generation command '__APPNAME__' "$@"; then
      code=0
    else
      code=$?
    fi
    local LC_ALL=C
    data=$(command head -c 2097153 <&8 2>/dev/null; printf '.')
    data=${data%.}
    if (( ${#data} > 2097152 )) || [[ $data != *$'\n' ]]; then
      return $code
    fi
    while IFS= read -r line; do
      if [[ -z $line || $line == *[[:cntrl:]]* ]] || (( ${#line} > 16384 || ${#____APPNAME___suggestions} >= 128 )); then
        ____APPNAME___suggestions_clear
        return $code
      fi
      ____APPNAME___suggestions+=("$line")
    done <<<"${data%$'\n'}"
    typeset -g ____APPNAME___suggestions_directory=$PWD
    (( $+functions[____APPNAME___suggestions_click_publish] )) && ____APPNAME___suggestions_click_publish
    return $code
  }

  ____APPNAME___suggestions_run() {
    emulate -L zsh
    # Sequential stderr redirections must retain the terminal, not a multios pipe.
    unsetopt multios
    local file code ____APPNAME___suggestions_started=0
    ____APPNAME___suggestions_clear
    # Only the bare interactive command hands its selection to the next editor.
    # Explicit flags and redirected commands retain the executable's own output.
    if (( $# == 1 && ZSH_SUBSHELL == 0 )) && [[ $1 == explore && -t 0 && -t 1 && -t 2 ]]; then
      local selected
      if selected=$(OPENAI_CLI_COMMANDS_FD= OPENAI_CLI_COMMANDS_SHELL= command '__APPNAME__' explore --shell-integration --shell zsh); then
        local LC_ALL=C
        [[ -n $selected && $selected != *[[:cntrl:]]* ]] && (( ${#selected} <= 16384 )) || return 1
        typeset -ga ____APPNAME___suggestions=("$selected")
        typeset -g ____APPNAME___suggestions_directory=$PWD
        typeset -g ____APPNAME___suggestions_index=1 ____APPNAME___suggestions_last=$selected
        typeset -g ____APPNAME___suggestions_draft='' ____APPNAME___suggestions_cursor=0
        print -rz -- "$selected"
        return 0
      else
        return $?
      fi
    fi
    # Optional capture must not replace descriptors used by CLI arguments.
    if [[ -e /dev/fd/7 || -e /dev/fd/8 || -e /dev/fd/9 ]]; then
      OPENAI_CLI_COMMANDS_CLICK_SESSION= OPENAI_CLI_COMMANDS_CLICK_GENERATION= command '__APPNAME__' "$@"
      return $?
    fi
    if ! file=$(command mktemp "${TMPDIR:-/tmp}/__APPNAME__-suggestions.XXXXXXXX" 2>/dev/null); then
      OPENAI_CLI_COMMANDS_CLICK_SESSION= OPENAI_CLI_COMMANDS_CLICK_GENERATION= command '__APPNAME__' "$@"
      return $?
    fi
    {
      # Suppress setup failures only. Restore stderr before the CLI starts.
      if ____APPNAME___suggestions_collect "$file" "$@" 7>&2 2>/dev/null 8<"$file" 9>"$file" 2>&7 7>&-; then
        code=0
      else
        code=$?
      fi
      if (( ! ____APPNAME___suggestions_started )); then
        OPENAI_CLI_COMMANDS_CLICK_SESSION= OPENAI_CLI_COMMANDS_CLICK_GENERATION= command '__APPNAME__' "$@"
        code=$?
      fi
    } always {
      command rm -f -- "$file" 2>/dev/null
    }
    return $code
  }

  __APPNAME___suggestion_restore() {
    emulate -L zsh
    if [[ -n ${WIDGET-} && ${____APPNAME___suggestions_index-0} -gt 0 && $BUFFER == $____APPNAME___suggestions_last ]]; then
      BUFFER=$____APPNAME___suggestions_draft
      CURSOR=$____APPNAME___suggestions_cursor
    fi
    typeset -g ____APPNAME___suggestions_index=0 ____APPNAME___suggestions_last=''
  }

  __APPNAME___suggestion_next() {
    emulate -L zsh
    [[ -n ${WIDGET-} && ${____APPNAME___suggestions_enabled-} == 1 ]] || return 0
    if [[ $PWD != ${____APPNAME___suggestions_directory-} ]]; then
      ____APPNAME___suggestions_clear
    fi
    (( ${#____APPNAME___suggestions} )) || { zle -M 'No commands from the last openai invocation.'; return 0; }
    local selected code row=0 candidate
    (( $+functions[____APPNAME___suggestions_click_revoke] )) && ____APPNAME___suggestions_click_revoke
    zle -I
    if selected=$(printf '%s\n' "${____APPNAME___suggestions[@]}" | OPENAI_CLI_COMMANDS_FD= OPENAI_CLI_COMMANDS_SHELL= command '__APPNAME__' explore --suggestions --shell-integration --shell zsh); then
      code=0
    else
      code=$?
    fi
    (( $+functions[____APPNAME___suggestions_click_publish] )) && ____APPNAME___suggestions_click_publish
    (( code == 0 )) || return 0
    # Accept only an exact current suggestion, never arbitrary helper output.
    for candidate in "${____APPNAME___suggestions[@]}"; do
      (( ++row ))
      [[ $candidate == "$selected" ]] && break
    done
    [[ -n $selected && $candidate == "$selected" ]] || return 0
    if (( ____APPNAME___suggestions_index == 0 )) || [[ $BUFFER != $____APPNAME___suggestions_last ]]; then
      typeset -g ____APPNAME___suggestions_draft=$BUFFER ____APPNAME___suggestions_cursor=$CURSOR
    fi
    typeset -g ____APPNAME___suggestions_index=$row
    BUFFER=$selected
    CURSOR=${#BUFFER}
    typeset -g ____APPNAME___suggestions_last=$BUFFER
    # The picker returns at its origin. Rebuild ZLE's display before a message
    # uses its cursor geometry, or the message can overwrite prior help output.
    zle -R
    zle -M 'Command inserted. Enter runs it.'
  }

  __APPNAME___command_insertion_disable() {
    emulate -L zsh
    (( $+functions[____APPNAME___suggestions_click_disable] )) && ____APPNAME___suggestions_click_disable
    typeset -g ____APPNAME___suggestions_enabled=0
    local keymap key
    for keymap in emacs viins; do
      for key in '^X^I' '^I^I'; do
        if [[ $(bindkey -M "$keymap" "$key") == *' __APPNAME___suggestion_next' ]]; then
          bindkey -M "$keymap" -r "$key"
        fi
      done
    done
    if ____APPNAME___suggestions_owns_command; then
      unfunction '__APPNAME__'
    fi
    add-zsh-hook -d chpwd ____APPNAME___suggestions_clear
    add-zsh-hook -d preexec ____APPNAME___suggestions_clear
    ____APPNAME___suggestions_clear
  }

  () {
    emulate -L zsh
    # Preserve aliases, user functions, and replacements made after setup.
    [[ $(whence -w '__APPNAME__') == '__APPNAME__: command' ]] || ____APPNAME___suggestions_owns_command || return 0
    function __APPNAME__ { ____APPNAME___suggestions_run "$@"; }
    typeset -g ____APPNAME___suggestions_wrapper_body=$functions[__APPNAME__]
    typeset -g ____APPNAME___suggestions_enabled=1
    ____APPNAME___suggestions_clear
    zle -N __APPNAME___suggestion_next
    zle -N __APPNAME___suggestion_restore
    autoload -Uz add-zsh-hook
    add-zsh-hook chpwd ____APPNAME___suggestions_clear
    add-zsh-hook preexec ____APPNAME___suggestions_clear
    local keymap key
    local -a keys=('^X^I')
    [[ __DOUBLE_TAB__ == 1 ]] && keys+=('^I^I')
    for keymap in emacs viins; do
      for key in $keys; do
        if [[ $(bindkey -M "$keymap" "$key") == *' undefined-key' ]]; then
          bindkey -M "$keymap" "$key" __APPNAME___suggestion_next
        fi
      done
    done
  }
fi
