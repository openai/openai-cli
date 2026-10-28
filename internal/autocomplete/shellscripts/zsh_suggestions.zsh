__openai_suggestions_zsh_source_trace=0
case $- in *x*) __openai_suggestions_zsh_source_trace=1; builtin set +x ;; esac
# OpenAI CLI executable suggestions. Explicitly source this after shell setup.
if [[ -z ${ZSH_VERSION-} || ! -o interactive ]]; then
    if (( __openai_suggestions_zsh_source_trace )); then builtin unset __openai_suggestions_zsh_source_trace; builtin set -x; else builtin unset __openai_suggestions_zsh_source_trace; fi
    return 0
fi
# A hashed command can retain its classification after its file disappears.
# Accept only the separately enabled insertion wrapper's unchanged definition.
# Looking up its external executable never executes the wrapper or request data.
if ! { { [[ $(builtin whence -w '__APPNAME__') == '__APPNAME__: command' ||
            $(builtin whence -w '__APPNAME__') == '__APPNAME__: hashed' ]] &&
         [[ -f $(builtin whence '__APPNAME__') && -x $(builtin whence '__APPNAME__') ]]; } ||
       [[ $(builtin whence -w '__APPNAME__') == '__APPNAME__: function' &&
          ${____APPNAME___suggestions_enabled-} == 1 && -n ${____APPNAME___suggestions_wrapper_body-} &&
          ${functions[__APPNAME__]-} == "$____APPNAME___suggestions_wrapper_body" &&
          -f $(builtin whence -p '__APPNAME__') && -x $(builtin whence -p '__APPNAME__') ]]; }; then
    builtin printf '%s\n' 'Executable suggestions require __APPNAME__ on PATH. No suggestion hook was installed.' >&2
    if (( __openai_suggestions_zsh_source_trace )); then builtin unset __openai_suggestions_zsh_source_trace; builtin set -x; else builtin unset __openai_suggestions_zsh_source_trace; fi
    return 2
fi
if builtin alias openai_suggestions_disable >/dev/null 2>&1; then
    builtin printf '%s\n' 'Executable suggestions conflict with an existing disable alias.' >&2
    if (( __openai_suggestions_zsh_source_trace )); then builtin unset __openai_suggestions_zsh_source_trace; builtin set -x; else builtin unset __openai_suggestions_zsh_source_trace; fi
    return 2
fi
if [[ ${__openai_suggestions_zsh_state-} == active ]]; then
    if [[ ${__openai_suggestions_zsh_command-} != '__APPNAME__' ]]; then
        builtin printf '%s\n' 'Disable executable suggestions before binding another command.' >&2
        if (( __openai_suggestions_zsh_source_trace )); then builtin unset __openai_suggestions_zsh_source_trace; builtin set -x; else builtin unset __openai_suggestions_zsh_source_trace; fi
        return 2
    fi
    if (( __openai_suggestions_zsh_source_trace )); then builtin unset __openai_suggestions_zsh_source_trace; builtin set -x; else builtin unset __openai_suggestions_zsh_source_trace; fi
    return 0
fi
# Zsh gives native handlers a different $0 than direct function calls.
# Refuse foreign handlers without reading, copying, or autoloading their bodies.
if (( $+functions[command_not_found_handler] )); then
    builtin printf '%s\n' 'Executable suggestions conflict with an existing zsh handler.' \
        'Keep that handler and use __APPNAME__ for CLI commands.' >&2
    if (( __openai_suggestions_zsh_source_trace )); then builtin unset __openai_suggestions_zsh_source_trace; builtin set -x; else builtin unset __openai_suggestions_zsh_source_trace; fi
    return 2
fi
if (( $+functions[openai_suggestions_disable] )) &&
   [[ -z ${__openai_suggestions_zsh_state-} || ${functions[openai_suggestions_disable]} != "${__openai_suggestions_zsh_disable-}" ]]; then
    builtin printf '%s\n' 'Executable suggestions were not enabled: openai_suggestions_disable already exists.' >&2
    if (( __openai_suggestions_zsh_source_trace )); then builtin unset __openai_suggestions_zsh_source_trace; builtin set -x; else builtin unset __openai_suggestions_zsh_source_trace; fi
    return 2
fi
if (( $+functions[__openai_suggestions_zsh_match] )) &&
   [[ -z ${__openai_suggestions_zsh_state-} || ${functions[__openai_suggestions_zsh_match]} != "${__openai_suggestions_zsh_match_owner-}" ]]; then
    builtin printf '%s\n' 'Executable suggestions could not reserve its shell helper.' >&2
    if (( __openai_suggestions_zsh_source_trace )); then builtin unset __openai_suggestions_zsh_source_trace; builtin set -x; else builtin unset __openai_suggestions_zsh_source_trace; fi
    return 2
fi
__openai_suggestions_zsh_state=active
__openai_suggestions_zsh_command='__APPNAME__'

function __openai_suggestions_zsh_match {
    local LC_ALL=C
    [[ $# == 1 && $1 != '__APPNAME__' && ${#1} -gt 0 && ${#1} -le 65 ]] || return 1
    case $1 in *[!A-Za-z0-9_-]*) return 1 ;; esac
    case $1 in __SUGGESTION_PATTERN__) return 0 ;; esac
    return 1
}

function command_not_found_handler {
    local __openai_suggestions_trace=0
    case $- in *x*) __openai_suggestions_trace=1; builtin set +x ;; esac
    local __openai_suggestions_target
    if [[ ${__openai_suggestions_zsh_state-} == active ]] &&
       "__openai_suggestions_zsh_match" "${1-}" &&
       { { [[ $(builtin whence -w '__APPNAME__') == '__APPNAME__: command' ||
              $(builtin whence -w '__APPNAME__') == '__APPNAME__: hashed' ]] &&
           __openai_suggestions_target=$(builtin whence '__APPNAME__'); } ||
         { [[ $(builtin whence -w '__APPNAME__') == '__APPNAME__: function' &&
              ${____APPNAME___suggestions_enabled-} == 1 && -n ${____APPNAME___suggestions_wrapper_body-} &&
              ${functions[__APPNAME__]-} == "$____APPNAME___suggestions_wrapper_body" ]] &&
           __openai_suggestions_target=$(builtin whence -p '__APPNAME__'); }; } &&
       [[ -f $__openai_suggestions_target && -x $__openai_suggestions_target ]]; then
        builtin printf '%s\n' 'Command not found. Did you mean: __APPNAME__?' \
            'Keep the remaining arguments unchanged.' >&2
    else
        builtin printf '%s\n' 'zsh: command not found' >&2
    fi
    if (( __openai_suggestions_trace )); then builtin set -x; fi
    return 127
}

function openai_suggestions_disable {
    local __openai_suggestions_trace=0
    case $- in *x*) __openai_suggestions_trace=1; builtin set +x ;; esac
    __openai_suggestions_zsh_state=disabled
    if [[ ${functions[command_not_found_handler]-} == "$__openai_suggestions_zsh_owner" ]]; then
        builtin unfunction command_not_found_handler
    fi
    if (( __openai_suggestions_trace )); then builtin set -x; fi
    return 0
}
__openai_suggestions_zsh_owner=${functions[command_not_found_handler]}
__openai_suggestions_zsh_disable=${functions[openai_suggestions_disable]}
__openai_suggestions_zsh_match_owner=${functions[__openai_suggestions_zsh_match]}

if (( __openai_suggestions_zsh_source_trace )); then builtin unset __openai_suggestions_zsh_source_trace; builtin set -x; else builtin unset __openai_suggestions_zsh_source_trace; fi
