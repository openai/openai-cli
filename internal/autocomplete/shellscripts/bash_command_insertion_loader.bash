# Generate trusted integration code from this installation at each shell startup.
# Suggestion records remain data. The command catalog is never stored here.
if [[ $- == *i* && -t 0 && -t 1 && -t 2 && ${TERM-} != dumb &&
      ${__openai_suggestions_loader_loaded-} != 1 ]] &&
   (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 3) )); then
  __openai_suggestions_load_current() {
    local __openai_startup_executable=__EXECUTABLE__ __openai_startup_resolved __openai_startup_generated
    [[ -f "$__openai_startup_executable" && -x "$__openai_startup_executable" && $(type -t openai) == file ]] || return 0
    __openai_startup_resolved=$(type -P openai 2>/dev/null) || return 0
    [[ "$__openai_startup_executable" -ef "$__openai_startup_resolved" ]] || return 0
    if __openai_startup_generated=$(command "$__openai_startup_executable" @completion bash --suggestions --suggestions-only__DOUBLE_TAB_ARGUMENT__ </dev/null 2>/dev/null); then
      builtin test "${__openai_startup_generated##*$'\n'}" = '# openai-cli-suggestions-complete v1' || return 0
      __openai_suggestions_loader_loaded=1
      # Evaluate only successful output from the pinned executable, never command records.
      builtin eval -- "$__openai_startup_generated"
    fi
    return 0
  }
  __openai_suggestions_load_current
  unset -f __openai_suggestions_load_current
fi
