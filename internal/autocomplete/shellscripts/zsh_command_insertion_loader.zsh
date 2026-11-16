# Generate trusted integration code from this installation at each shell startup.
# Resolve the logical path again, so package-manager symlink upgrades take effect.
if [[ -o interactive && -o zle && -t 0 && -t 1 && -t 2 && ${TERM-} != dumb &&
      ${__openai_suggestions_loader_loaded-} != 1 ]]; then
  () {
    emulate -L zsh
    local __openai_startup_executable=__EXECUTABLE__ __openai_startup_resolved __openai_startup_generated
    [[ -f $__openai_startup_executable && -x $__openai_startup_executable && $(whence -w openai) == 'openai: command' ]] || return 0
    __openai_startup_resolved=$(whence -p openai 2>/dev/null) || return 0
    [[ $__openai_startup_executable -ef $__openai_startup_resolved ]] || return 0
    if __openai_startup_generated=$(command "$__openai_startup_executable" @completion zsh --suggestions --suggestions-only__DOUBLE_TAB_ARGUMENT__ </dev/null 2>/dev/null); then
      [[ ${__openai_startup_generated##*$'\n'} == '# openai-cli-suggestions-complete v1' ]] || return 0
      typeset -g __openai_suggestions_loader_loaded=1
      # Evaluate only successful output from the pinned executable, never command records.
      builtin eval -- "$__openai_startup_generated"
    fi
    return 0
  }
fi
