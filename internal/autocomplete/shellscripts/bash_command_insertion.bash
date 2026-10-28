# Optional, current-session command insertion for Bash 4.3+.
# Ctrl-X, then Tab searches displayed commands. Selection never executes.
# Bindings are installed only when free. Double Tab is a separate opt-in.
if [[ $- == *i* && -t 0 && -t 1 && -t 2 && ${TERM-} != dumb ]] &&
  (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 3) )); then
  ____APPNAME___suggestions_owns_command() {
    [[ -n ${____APPNAME___suggestions_wrapper_body-} && $(type -t '__APPNAME__') == function ]] &&
      builtin test "$(declare -f '__APPNAME__')" = "$____APPNAME___suggestions_wrapper_body"
  }

  ____APPNAME___suggestions_clear() {
    ____APPNAME___suggestions=()
    ____APPNAME___suggestions_index=0
    ____APPNAME___suggestions_last=''
    ____APPNAME___suggestions_fresh=0
    ____APPNAME___suggestions_staged=''
  }

  ____APPNAME___suggestions_prompt() {
    local code=$?
    if [[ ${____APPNAME___suggestions_enabled-} == 1 ]]; then
      if [[ ${____APPNAME___suggestions_fresh-0} != 1 ]] || [ "$PWD" != "${____APPNAME___suggestions_directory-}" ]; then
        ____APPNAME___suggestions_clear
      fi
      ____APPNAME___suggestions_fresh=0
    fi
    return "$code"
  }

  ____APPNAME___suggestions_collect() {
    local code data line
    ____APPNAME___suggestions_started=1
    if ! command rm -f -- "$1" 2>/dev/null; then
      shift
      OPENAI_CLI_COMMANDS_FD= OPENAI_CLI_COMMANDS_SHELL= command '__APPNAME__' "$@" 9>&-
      return $?
    fi
    shift
    if OPENAI_CLI_COMMANDS_FD=9 OPENAI_CLI_COMMANDS_SHELL=bash command '__APPNAME__' "$@"; then
      code=0
    else
      code=$?
    fi
    local LC_ALL=C
    data=$(command head -c 2097153 <&8 2>/dev/null; printf '.')
    data=${data%.}
    if (( ${#data} > 2097152 )) || [[ $data != *$'\n' ]]; then
      return "$code"
    fi
    while IFS= read -r line; do
      if [[ -z $line || $line == *[[:cntrl:]]* ]] || (( ${#line} > 16384 || ${#____APPNAME___suggestions[@]} >= 128 )); then
        ____APPNAME___suggestions_clear
        return "$code"
      fi
      ____APPNAME___suggestions+=("$line")
    done <<<"${data%$'\n'}"
    ____APPNAME___suggestions_directory=$PWD
    # A picker callback runs between prompts. Its state must expire on submission.
    if [[ ! ${READLINE_LINE+x} ]]; then ____APPNAME___suggestions_fresh=1; fi
    return "$code"
  }

  ____APPNAME___suggestions_run() {
    local file code ____APPNAME___suggestions_started=0
    ____APPNAME___suggestions_clear
    if (( $# == 1 && BASH_SUBSHELL == 0 )) && [ "$1" = explore ] && [[ -t 0 && -t 1 && -t 2 ]]; then
      local selected
      if selected=$(OPENAI_CLI_COMMANDS_FD= OPENAI_CLI_COMMANDS_SHELL= command '__APPNAME__' explore --shell-integration --shell bash); then
        local LC_ALL=C
        [[ -n $selected && $selected != *[[:cntrl:]]* ]] && (( ${#selected} <= 16384 )) || return 1
        ____APPNAME___suggestions=("$selected")
        ____APPNAME___suggestions_directory=$PWD
        ____APPNAME___suggestions_fresh=1
        ____APPNAME___suggestions_staged=$selected
        printf 'Command selected. Press Ctrl+X, then Tab to insert it.\n' >&2
        return 0
      else
        return $?
      fi
    fi
    if [[ -e /dev/fd/7 || -e /dev/fd/8 || -e /dev/fd/9 ]]; then
      command '__APPNAME__' "$@"
      return $?
    fi
    if ! file=$(command mktemp "${TMPDIR:-/tmp}/__APPNAME__-suggestions.XXXXXXXX" 2>/dev/null); then
      command '__APPNAME__' "$@"
      return $?
    fi
    # Scoped redirections restore the caller's descriptors, including on Ctrl-C.
    # FD8 and FD9 open the file separately, so the reader starts at byte zero.
    if ____APPNAME___suggestions_collect "$file" "$@" 7>&2 2>/dev/null 8<"$file" 9>"$file" 2>&7 7>&-; then
      code=0
    else
      code=$?
    fi
    command rm -f -- "$file" 2>/dev/null
    if (( ! ____APPNAME___suggestions_started )); then
      command '__APPNAME__' "$@"
      return $?
    fi
    return "$code"
  }

  __APPNAME___suggestion_restore() {
    if [[ ${READLINE_LINE+x} && ${____APPNAME___suggestions_index-0} -gt 0 ]] && [ "$READLINE_LINE" = "$____APPNAME___suggestions_last" ]; then
      READLINE_LINE=$____APPNAME___suggestions_draft
      READLINE_POINT=$____APPNAME___suggestions_cursor
    fi
    ____APPNAME___suggestions_index=0
    ____APPNAME___suggestions_last=''
  }

  __APPNAME___suggestion_next() {
    [[ ${READLINE_LINE+x} && ${____APPNAME___suggestions_enabled-} == 1 ]] || return 0
    if [ "$PWD" != "${____APPNAME___suggestions_directory-}" ]; then
      ____APPNAME___suggestions_clear
    fi
    (( ${#____APPNAME___suggestions[@]} )) || return 0
    local selected candidate row=0 found=0
    if [[ -n ${____APPNAME___suggestions_staged-} ]]; then
      selected=$____APPNAME___suggestions_staged
    elif ! selected=$(printf '%s\n' "${____APPNAME___suggestions[@]}" | OPENAI_CLI_COMMANDS_FD= OPENAI_CLI_COMMANDS_SHELL= command '__APPNAME__' explore --suggestions --shell-integration --shell bash); then
      return 0
    fi
    for candidate in "${____APPNAME___suggestions[@]}"; do
      (( ++row ))
      if [ "$candidate" = "$selected" ]; then found=1; break; fi
    done
    [[ -n $selected && $found == 1 ]] || return 0
    if (( ____APPNAME___suggestions_index == 0 )) || [ "$READLINE_LINE" != "$____APPNAME___suggestions_last" ]; then
      ____APPNAME___suggestions_draft=$READLINE_LINE
      ____APPNAME___suggestions_cursor=$READLINE_POINT
    fi
    ____APPNAME___suggestions_index=$row
    ____APPNAME___suggestions_staged=''
    READLINE_LINE=$selected
    # Bash 4.x uses byte offsets; Bash 5+ uses character offsets.
    if (( BASH_VERSINFO[0] < 5 )); then local LC_ALL=C; fi
    READLINE_POINT=${#READLINE_LINE}
    ____APPNAME___suggestions_last=$READLINE_LINE
  }

  ____APPNAME___suggestions_binding() {
    local line prefix="\"$2\" "
    while IFS= read -r line; do
      if builtin test "${line%%: *}" = "\"$2\""; then
        printf '%s\n' "$line"
        return
      fi
      if builtin test "${line:0:${#prefix}}" = "$prefix"; then
        printf '"%s": %s\n' "$2" "${line:${#prefix}}"
        return
      fi
    done < <({ bind -m "$1" -p; bind -m "$1" -s; bind -m "$1" -X; } 2>/dev/null)
  }

  __APPNAME___command_insertion_disable() {
    ____APPNAME___suggestions_enabled=0
    local keymap key index
    for keymap in emacs-standard vi-insert; do
      for key in '\C-x\C-i' '\C-i\C-i'; do
        if builtin test "$(____APPNAME___suggestions_binding "$keymap" "$key")" = "\"$key\": \"__APPNAME___suggestion_next\""; then
          bind -m "$keymap" -r "$key"
        fi
      done
    done
    if ____APPNAME___suggestions_owns_command; then
      unset -f '__APPNAME__'
    fi
    if [[ -n ${____APPNAME___suggestions_prompt_installed-} ]] &&
      builtin test "$(declare -p PROMPT_COMMAND 2>/dev/null)" = "$____APPNAME___suggestions_prompt_installed"; then
      case $____APPNAME___suggestions_prompt_kind in
        array)
          PROMPT_COMMAND=()
          for index in "${!____APPNAME___suggestions_prompt_previous[@]}"; do
            PROMPT_COMMAND[${____APPNAME___suggestions_prompt_indices[index]}]=${____APPNAME___suggestions_prompt_previous[index]}
          done
          ;;
        scalar) PROMPT_COMMAND=${____APPNAME___suggestions_prompt_previous[0]} ;;
        unset) unset PROMPT_COMMAND ;;
      esac
      unset ____APPNAME___suggestions_prompt_installed
    fi
    ____APPNAME___suggestions_clear
  }

  ____APPNAME___suggestions_install() {
    [[ $(type -t '__APPNAME__') == file ]] || ____APPNAME___suggestions_owns_command || return 0
    local prompt_declaration
    prompt_declaration=$(declare -p PROMPT_COMMAND 2>/dev/null)
    # Freshness requires a prompt hook. Leave readonly prompt configurations intact.
    [[ ! $prompt_declaration =~ ^declare\ -[^[:space:]]*r ]] || return 0
    # Older scripts appended an array hook without saving the original indices.
    # An unchanged owned array still contains those indices before its final hook.
    if [[ -n ${____APPNAME___suggestions_prompt_installed-} && ${____APPNAME___suggestions_prompt_kind-} == array ]] &&
      ! declare -p ____APPNAME___suggestions_prompt_indices &>/dev/null &&
      builtin test "$prompt_declaration" = "$____APPNAME___suggestions_prompt_installed"; then
      declare -ga ____APPNAME___suggestions_prompt_indices
      ____APPNAME___suggestions_prompt_indices=("${!PROMPT_COMMAND[@]}")
      local last_prompt_index=$((${#____APPNAME___suggestions_prompt_indices[@]} - 1))
      unset "____APPNAME___suggestions_prompt_indices[$last_prompt_index]"
      __APPNAME___command_insertion_disable
      prompt_declaration=$(declare -p PROMPT_COMMAND 2>/dev/null)
    fi
    function __APPNAME__ { ____APPNAME___suggestions_run "$@"; }
    ____APPNAME___suggestions_wrapper_body=$(declare -f '__APPNAME__')
    ____APPNAME___suggestions_enabled=1
    declare -ga ____APPNAME___suggestions
    ____APPNAME___suggestions_clear
    # Preserve scalar/array prompt hooks. Never modify a readonly hook.
    if [[ -z ${____APPNAME___suggestions_prompt_installed-} && ! $prompt_declaration =~ ^declare\ -[^[:space:]]*r ]]; then
      declare -ga ____APPNAME___suggestions_prompt_previous
      if [[ $prompt_declaration =~ ^declare\ -[^[:space:]]*a ]]; then
        ____APPNAME___suggestions_prompt_kind=array
        ____APPNAME___suggestions_prompt_previous=("${PROMPT_COMMAND[@]}")
        declare -ga ____APPNAME___suggestions_prompt_indices
        ____APPNAME___suggestions_prompt_indices=("${!PROMPT_COMMAND[@]}")
        if (( BASH_VERSINFO[0] > 5 || (BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 1) )); then
          PROMPT_COMMAND+=(____APPNAME___suggestions_prompt)
        else
          # Bash 4.3 through 5.0 execute only element zero of an indexed prompt array.
          PROMPT_COMMAND[0]+=$'\n____APPNAME___suggestions_prompt'
        fi
      elif [[ ${PROMPT_COMMAND+x} ]]; then
        ____APPNAME___suggestions_prompt_kind=scalar
        ____APPNAME___suggestions_prompt_previous=("$PROMPT_COMMAND")
        PROMPT_COMMAND+=$'\n____APPNAME___suggestions_prompt'
      else
        ____APPNAME___suggestions_prompt_kind=unset
        PROMPT_COMMAND=____APPNAME___suggestions_prompt
      fi
      ____APPNAME___suggestions_prompt_installed=$(declare -p PROMPT_COMMAND)
    fi
    local keymap key
    local -a keys=('\C-x\C-i')
    [[ __DOUBLE_TAB__ == 1 ]] && keys+=('\C-i\C-i')
    for keymap in emacs-standard vi-insert; do
      for key in "${keys[@]}"; do
        if [[ -z $(____APPNAME___suggestions_binding "$keymap" "$key") ]]; then
          bind -m "$keymap" -x "\"$key\": __APPNAME___suggestion_next"
        fi
      done
    done
  }
  ____APPNAME___suggestions_install
fi
