# Optional image picker integration. Source after normal completion setup.
if [[ -o interactive && -o zle && -t 0 && -t 1 && -t 2 && -n ${TERM-} && ${TERM-} != dumb ]]; then
  ____APPNAME___picker_matches() {
    emulate -L zsh
    setopt casematch
    local pattern=$'^[ \t]*__APPNAME__[ \t]+images[ \t]+generate[ \t]*$'
    [[ $BUFFER =~ $pattern && $CURSOR -eq ${#BUFFER} && $(whence -w '__APPNAME__') == '__APPNAME__: command' ]]
  }

  ____APPNAME___picker_owns_tab() {
    emulate -L zsh
    local binding=$(bindkey -M "${KEYMAP:-main}" '^I')
    [[ $binding == '"^I" ____APPNAME___picker_tab_emacs' || $binding == '"^I" ____APPNAME___picker_tab_viins' ]]
  }

  ____APPNAME___picker_clear_hint() {
    emulate -L zsh
    if [[ ${____APPNAME___picker_hint_visible-} == 1 && ${POSTDISPLAY-} == '  [Tab: image options]' ]]; then
      POSTDISPLAY=''
    fi
    typeset -g ____APPNAME___picker_hint_visible=0
  }

  ____APPNAME___picker_hint() {
    emulate -L zsh
    ____APPNAME___picker_clear_hint
    if [[ ${____APPNAME___picker_enabled-} == 1 && -t 1 && -t 2 && -n ${TERM-} && ${TERM-} != dumb && -z $POSTDISPLAY ]] &&
      ____APPNAME___picker_owns_tab && ____APPNAME___picker_matches; then
      POSTDISPLAY='  [Tab: image options]'
      typeset -g ____APPNAME___picker_hint_visible=1
    fi
  }

  ____APPNAME___picker_tab() {
    emulate -L zsh
    if [[ ${____APPNAME___picker_enabled-} == 1 && -t 1 && -t 2 && -n ${TERM-} && ${TERM-} != dumb ]] &&
      ____APPNAME___picker_owns_tab && ____APPNAME___picker_matches; then
      ____APPNAME___picker_clear_hint
      # ZLE redirects stdin away from the terminal while running widgets.
      # Stderr was verified above, so duplicate that terminal for the child.
      zle -I
      OPENAI_PICKER_SHELL=zsh command '__APPNAME__' images generate <&2
      zle reset-prompt
      return 0
    fi
    zle "____APPNAME___picker_previous_$1" -- "${@:2}"
  }

  ____APPNAME___picker_tab_emacs() { ____APPNAME___picker_tab emacs "$@"; }
  ____APPNAME___picker_tab_viins() { ____APPNAME___picker_tab viins "$@"; }

  __APPNAME___picker_disable() {
    emulate -L zsh
    typeset -g ____APPNAME___picker_enabled=0
    ____APPNAME___picker_clear_hint
    local keymap binding
    for keymap in emacs viins; do
      binding=$(bindkey -M "$keymap" '^I')
      if [[ $binding == '"^I" ____APPNAME___picker_tab_'$keymap ]]; then
        bindkey -M "$keymap" '^I' "${____APPNAME___picker_bindings[$keymap]}"
      fi
    done
    add-zle-hook-widget -d line-pre-redraw ____APPNAME___picker_hint
    add-zle-hook-widget -d line-finish ____APPNAME___picker_clear_hint
    # Leave private widget aliases available for plugins that chained them.
    # They delegate to the saved widget while the picker is disabled.
  }

  () {
    emulate -L zsh
    zmodload zsh/zleparameter || return
    autoload -Uz add-zle-hook-widget
    typeset -gA ____APPNAME___picker_bindings
    local keymap original
    local -a binding
    for keymap in emacs viins; do
      binding=(${(z)$(bindkey -M "$keymap" '^I')})
      original=${(Q)binding[2]}
      # Capture a delegate once. Recapturing a newer plugin that calls our
      # wrapper would introduce a cycle when integration is enabled again.
      if [[ -n ${____APPNAME___picker_bindings[$keymap]-} ]]; then
        if [[ $original == ${____APPNAME___picker_bindings[$keymap]} && ${____APPNAME___picker_enabled-} != 1 ]]; then
          bindkey -M "$keymap" '^I' "____APPNAME___picker_tab_$keymap"
        fi
        continue
      fi
      [[ -n ${widgets[$original]-} && $original != ____APPNAME___picker_tab_* ]] || continue
      zle -A "$original" "____APPNAME___picker_previous_$keymap" || continue
      ____APPNAME___picker_bindings[$keymap]=$original
      zle -N "____APPNAME___picker_tab_$keymap"
      bindkey -M "$keymap" '^I' "____APPNAME___picker_tab_$keymap"
    done
    typeset -g ____APPNAME___picker_enabled=1
    add-zle-hook-widget line-pre-redraw ____APPNAME___picker_hint
    add-zle-hook-widget line-finish ____APPNAME___picker_clear_hint
  }
fi
