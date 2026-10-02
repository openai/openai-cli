# Optional image picker integration. Source the output of
# __APPNAME__ @completion bash --picker in an interactive terminal.
if [[ $- == *i* && -t 0 && -t 1 && -t 2 && -n ${TERM-} && ${TERM-} != dumb ]]; then
  if (( BASH_VERSINFO[0] < 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] < 3) )); then
    : # Keep ordinary completion on older Bash without startup diagnostics.
  else
    ____APPNAME___picker_binding() {
      local line prefix="\"$2\" "
      while IFS= read -r line; do
        if [ "${line%%: *}" = "\"$2\"" ]; then
          printf '%s\n' "$line"
          return
        fi
        # bind -X omits the colon used by bind -p and bind -s.
        if [ "${line#"$prefix"}" != "$line" ]; then
          printf '"%s": %s\n' "$2" "${line#"$prefix"}"
          return
        fi
      done < <({ bind -m "$1" -p; bind -m "$1" -s; bind -m "$1" -X; } 2>/dev/null)
    }

    ____APPNAME___picker_matches() {
      local IFS=$' \t'
      local -a words
      [[ ${COMP_LINE-} != *$'\n'* && ${COMP_POINT-0} -eq ${#COMP_LINE} ]] || return 1
      read -r -a words <<<"$COMP_LINE"
      # `test` keeps exact case even with the user's nocasematch option set.
      [ ${#words[@]} -eq 3 ] && [ "${words[0]}" = '__APPNAME__' ] &&
        [ "${words[1]}" = images ] && [ "${words[2]}" = generate ] &&
        [ "$(type -t '__APPNAME__')" = file ]
    }

    ____APPNAME___picker_redraw() {
      local keymap=${____APPNAME___picker_active_keymap-}
      if [ -n "$keymap" ] &&
        [ "$(____APPNAME___picker_binding "$keymap" '\e[99;2~')" = '"\e[99;2~": "____APPNAME___picker_redraw"' ]; then
        bind -m "$keymap" '"\e[99;2~": ""'
      fi
      # COMP_LINE omits shell syntax before this command (assignments, pipes,
      # and command separators). Only bind -x exposes the entire editor buffer.
      local COMP_LINE=${READLINE_LINE-} COMP_POINT=${READLINE_POINT-0}
      if [[ ${____APPNAME___picker_enabled-} == 1 && -t 0 && -t 1 && -t 2 && -n ${TERM-} && ${TERM-} != dumb ]] &&
        ____APPNAME___picker_matches; then
        # Normal completion may have added a suffix. Cancel restores the line
        # that requested the picker, including its original cursor position.
        local leading=${READLINE_LINE%%[!$' \t']*}
        local candidate=$____APPNAME___picker_candidate_line
        candidate=${candidate#"${candidate%%[!$' \t']*}"}
        READLINE_LINE=$leading$candidate
        READLINE_POINT=${#READLINE_LINE}
        printf '\n'
        OPENAI_PICKER_SHELL=bash command '__APPNAME__' images generate <&2
      fi
      # Returning from bind -x asks Readline to redraw its own prompt/buffer.
    }

    ____APPNAME___picker_complete() {
      local keymap=vi-insertion
      local binding
      [[ -o emacs ]] && keymap=emacs-standard
      if [[ ${____APPNAME___picker_enabled-} == 1 && ${COMP_TYPE-} == 9 && ${COMP_KEY-} == 126 &&
        -t 0 && -t 1 && -t 2 && -n ${TERM-} && ${TERM-} != dumb ]] &&
        [ "${____APPNAME___picker_keymaps[$keymap]-}" = 1 ] &&
        [ "$(____APPNAME___picker_binding "$keymap" '\C-i')" = '"\C-i": "\e[99;1~\e[99;2~"' ] &&
        [ "$(____APPNAME___picker_binding "$keymap" '\e[99;1~')" = '"\e[99;1~": complete' ] &&
        ____APPNAME___picker_matches; then
        binding=$(____APPNAME___picker_binding "$keymap" '\e[99;2~')
        if [ "$binding" = '"\e[99;2~": ""' ] || [ "$binding" = '"\e[99;2~": "____APPNAME___picker_redraw"' ]; then
          ____APPNAME___picker_active_keymap=$keymap
          ____APPNAME___picker_candidate_line=$COMP_LINE
          bind -m "$keymap" -x '"\e[99;2~": ____APPNAME___picker_redraw'
        fi
      fi
      ____APPNAME___bash_autocomplete "$@"
    }

    __APPNAME___picker_disable() {
      ____APPNAME___picker_enabled=0
      local keymap binding
      if [ "$(complete -p '__APPNAME__' 2>/dev/null)" = 'complete -o filenames -F ____APPNAME___picker_complete __APPNAME__' ]; then
        complete -o filenames -F ____APPNAME___bash_autocomplete '__APPNAME__'
      fi
      for keymap in emacs-standard vi-insertion; do
        [ "${____APPNAME___picker_keymaps[$keymap]-}" = 1 ] || continue
        if [ "$(____APPNAME___picker_binding "$keymap" '\C-i')" = '"\C-i": "\e[99;1~\e[99;2~"' ]; then
          bind -m "$keymap" '"\C-i": complete'
          if [ "$(____APPNAME___picker_binding "$keymap" '\e[99;1~')" = '"\e[99;1~": complete' ]; then
            bind -m "$keymap" -r '\e[99;1~'
          fi
          binding=$(____APPNAME___picker_binding "$keymap" '\e[99;2~')
          if [ "$binding" = '"\e[99;2~": ""' ] || [ "$binding" = '"\e[99;2~": "____APPNAME___picker_redraw"' ]; then
            bind -m "$keymap" -r '\e[99;2~'
          fi
          unset '____APPNAME___picker_keymaps[$keymap]'
        fi
      done
    }

    ____APPNAME___picker_install() {
      local keymap
      declare -gA ____APPNAME___picker_keymaps
      for keymap in emacs-standard vi-insertion; do
        [ "${____APPNAME___picker_keymaps[$keymap]-}" = 1 ] && continue
        # Install only around ordinary completion and only into unused keys.
        # Custom Tab bindings and later replacements remain owned by their user.
        if [ "$(____APPNAME___picker_binding "$keymap" '\C-i')" = '"\C-i": complete' ] &&
          [ -z "$(____APPNAME___picker_binding "$keymap" '\e[99;1~')" ] &&
          [ -z "$(____APPNAME___picker_binding "$keymap" '\e[99;2~')" ]; then
          bind -m "$keymap" '"\e[99;1~": complete'
          bind -m "$keymap" '"\e[99;2~": ""'
          bind -m "$keymap" '"\C-i": "\e[99;1~\e[99;2~"'
          ____APPNAME___picker_keymaps[$keymap]=1
        fi
      done
      ____APPNAME___picker_enabled=1
      complete -o filenames -F ____APPNAME___picker_complete '__APPNAME__'
    }
    ____APPNAME___picker_install
  fi
fi
