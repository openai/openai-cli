__openai_suggestions_bash_source_trace=0
case $- in *x*) __openai_suggestions_bash_source_trace=1; builtin set +x ;; esac
# OpenAI CLI executable suggestions. Explicitly source this after shell setup.
if [[ -z ${BASH_VERSION-} ]] || (( BASH_VERSINFO[0] < 4 )); then
    builtin printf '%s\n' 'Executable suggestions require Bash 4 or later. No suggestion hook was installed.' >&2
    if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
    return 2
fi
if [[ $- != *i* ]]; then
    if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
    return 0
fi
# A hashed command can retain its classification after its file disappears.
# The separately enabled command-insertion wrapper is identifiable by its live
# definition. Use exact comparisons even when nocasematch is enabled.
# Never invoke that wrapper or accept a replaced user function.
if ! { [[ $(builtin type -t '__APPNAME__') == file &&
          -f $(builtin type -p '__APPNAME__') && -x $(builtin type -p '__APPNAME__') ]] ||
       { [[ $(builtin type -t '__APPNAME__') == function &&
            ${____APPNAME___suggestions_enabled-} == 1 && -n ${____APPNAME___suggestions_wrapper_body-} ]] &&
         builtin test "$(builtin declare -f '__APPNAME__')" = "$____APPNAME___suggestions_wrapper_body" &&
         [[ -f $(builtin type -P '__APPNAME__') && -x $(builtin type -P '__APPNAME__') ]]; }; }; then
    builtin printf '%s\n' 'Executable suggestions require __APPNAME__ on PATH. No suggestion hook was installed.' >&2
    if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
    return 2
fi
if builtin alias openai_suggestions_disable >/dev/null 2>&1; then
    builtin printf '%s\n' 'Executable suggestions conflict with an existing disable alias.' >&2
    if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
    return 2
fi

# A later handler replacement wins, including when this script is sourced again.
if builtin test "${__openai_suggestions_bash_state-}" = active; then
    if builtin test "${__openai_suggestions_bash_command-}" != '__APPNAME__'; then
        builtin printf '%s\n' 'Disable executable suggestions before binding another command.' >&2
        if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
        return 2
    fi
    if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
    return 0
fi
# Recreating a foreign handler changes BASH_SOURCE and can break sibling-file reads.
# Refuse it without copying, evaluating, or changing its definition or attributes.
if builtin declare -F command_not_found_handle >/dev/null; then
    builtin printf '%s\n' 'Executable suggestions conflict with an existing Bash handler.' \
        'Keep that handler and use __APPNAME__ for CLI commands.' >&2
    if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
    return 2
fi
if builtin declare -F openai_suggestions_disable >/dev/null &&
   builtin test "$(builtin declare -f openai_suggestions_disable)" != "${__openai_suggestions_bash_disable-}"; then
    builtin printf '%s\n' 'Executable suggestions were not enabled: openai_suggestions_disable already exists.' >&2
    if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
    return 2
fi
if builtin declare -F __openai_suggestions_bash_match >/dev/null &&
   builtin test "$(builtin declare -f __openai_suggestions_bash_match)" != "${__openai_suggestions_bash_match_owner-}"; then
    builtin printf '%s\n' 'Executable suggestions could not reserve its shell helper.' >&2
    if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
    return 2
fi
# Preserve attributes on helpers retained after disabling suggestions.
if (
    builtin shopt -u nocasematch
    case $'\n'"$(builtin declare -Fr; builtin declare -Fx; builtin declare -Ft)"$'\n' in
        *$' openai_suggestions_disable\n'*|*$' __openai_suggestions_bash_match\n'*) builtin exit 0 ;;
    esac
    builtin exit 1
); then
    builtin printf '%s\n' 'Executable suggestions cannot replace readonly, exported, or traced shell hooks.' >&2
    if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
    return 2
fi

__openai_suggestions_bash_state=active
__openai_suggestions_bash_command='__APPNAME__'

function __openai_suggestions_bash_match {
    local LC_ALL=C __openai_suggestions_nocasematch=0 __openai_suggestions_match_result=1
    builtin test "$#" -eq 1 || return 1
    builtin test "$1" != '__APPNAME__' || return 1
    (( ${#1} > 0 && ${#1} <= 65 )) || return 1
    if builtin shopt -q nocasematch; then
        __openai_suggestions_nocasematch=1
        builtin shopt -u nocasematch
    fi
    case $1 in
        *[!A-Za-z0-9_-]*) ;;
        *) case $1 in __SUGGESTION_PATTERN__) __openai_suggestions_match_result=0 ;; esac ;;
    esac
    if (( __openai_suggestions_nocasematch )); then builtin shopt -s nocasematch; fi
    return "$__openai_suggestions_match_result"
}

function command_not_found_handle {
    local __openai_suggestions_trace=0
    case $- in *x*) __openai_suggestions_trace=1; builtin set +x ;; esac
    local __openai_suggestions_target
    if builtin test "${__openai_suggestions_bash_state-}" = active &&
       "__openai_suggestions_bash_match" "${1-}" &&
       { { [[ $(builtin type -t '__APPNAME__') == file ]] &&
           __openai_suggestions_target=$(builtin type -p '__APPNAME__'); } ||
         { [[ $(builtin type -t '__APPNAME__') == function &&
              ${____APPNAME___suggestions_enabled-} == 1 && -n ${____APPNAME___suggestions_wrapper_body-} ]] &&
           builtin test "$(builtin declare -f '__APPNAME__')" = "$____APPNAME___suggestions_wrapper_body" &&
           __openai_suggestions_target=$(builtin type -P '__APPNAME__'); }; } &&
       [[ -f $__openai_suggestions_target && -x $__openai_suggestions_target ]]; then
        builtin printf '%s\n' 'Command not found. Did you mean: __APPNAME__?' \
            'Keep the remaining arguments unchanged.' >&2
    else
        builtin printf '%s\n' 'bash: command not found' >&2
    fi
    if (( __openai_suggestions_trace )); then builtin set -x; fi
    return 127
}

function openai_suggestions_disable {
    local __openai_suggestions_trace=0
    case $- in *x*) __openai_suggestions_trace=1; builtin set +x ;; esac
    __openai_suggestions_bash_state=disabled
    if builtin test "$(builtin declare -f command_not_found_handle)" = "$__openai_suggestions_bash_owner"; then
        builtin unset -f command_not_found_handle
    fi
    if (( __openai_suggestions_trace )); then builtin set -x; fi
    return 0
}
__openai_suggestions_bash_owner=$(builtin declare -f command_not_found_handle)
__openai_suggestions_bash_disable=$(builtin declare -f openai_suggestions_disable)
__openai_suggestions_bash_match_owner=$(builtin declare -f __openai_suggestions_bash_match)

if (( __openai_suggestions_bash_source_trace )); then builtin unset __openai_suggestions_bash_source_trace; builtin set -x; else builtin unset __openai_suggestions_bash_source_trace; fi
