#!/bin/zsh
# Source this file from the Homebrew bin directory to select the CLI in this shell.
() {
  emulate -L zsh
  if [[ $ZSH_EVAL_CONTEXT != *:file:shfunc ]]; then
    print -ru2 -- 'In zsh, run: source "$(brew --prefix)/bin/openai-enable-updates.zsh"'
    return 1
  fi
  if (( ${+aliases[openai]} || ${+galiases[openai]} || ${+functions[openai]} )); then
    print -ru2 -- 'Your shell selects an alias or function named openai. Rename it before enabling automatic updates.'
    return 1
  fi

  local entry=${(%):-%x}
  local brew_bin=${entry:a:h}/brew
  local physical=${entry:A} caskroom
  local installer=${physical:h}/install.sh
  caskroom=$("$brew_bin" --caskroom openai/tools/openai) || return $?
  if [[ ${physical:h:h} != ${caskroom:A} || ${physical:t} != openai-enable-updates.zsh ||
        ! -f $installer || -L $installer ]]; then
    print -ru2 -- 'The Homebrew helper is incomplete. Run brew update && brew upgrade --cask openai/tools/openai.'
    return 1
  fi
  /bin/sh "$installer" --homebrew "$brew_bin" --zsh "${ZDOTDIR:-$HOME}" || return $?

  if [[ $path[1] != "$HOME/.openai/bin" ]]; then
    export PATH="$HOME/.openai/bin:$PATH"
  fi
  if (( ${+commands[openai]} )); then
    builtin unhash openai || return $?
  fi
  if [[ $(whence openai) != "$HOME/.openai/bin/openai" ]]; then
    print -ru2 -- 'Your shell selects another command. Run "$HOME/.openai/bin/openai" directly.'
    return 1
  fi
  print -r -- 'This shell now uses $HOME/.openai/bin/openai.'
}
