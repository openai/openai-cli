# Source this helper from the installed Linux package or an extracted release.
case $- in
    *i*) ;;
    *) printf '%s\n' 'Source the OpenAI CLI installer from an interactive Bash or zsh session.' >&2; return 1 2>/dev/null || exit 1 ;;
esac
if [ -n "${BASH_VERSION:-}" ]; then
    if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
        printf '%s\n' 'Source the OpenAI CLI installer from your interactive shell.' >&2; exit 1
    fi
elif [ -n "${ZSH_VERSION:-}" ]; then
    case $ZSH_EVAL_CONTEXT in *:file) ;; *)
        printf '%s\n' 'Source the OpenAI CLI installer from your interactive shell.' >&2; return 1 2>/dev/null || exit 1 ;;
    esac
else
    printf '%s\n' 'Source this installer from Bash or zsh.' >&2; return 1 2>/dev/null || exit 1
fi

if builtin typeset -f __openai_setup_installed_shell >/dev/null 2>&1; then
    printf '%s\n' 'A shell function already uses the OpenAI CLI installer name.' >&2; return 1
fi
__openai_setup_installed_shell() {
    unset -f __openai_setup_installed_shell
    local entry=$1 installer shell profile_dir selection hook
    local -a install_args setup_args
    if [[ -n ${ZSH_VERSION:-} ]]; then
        emulate -L zsh
        shell=zsh; profile_dir=${ZDOTDIR:-$HOME}
        if (( ${+aliases[openai]} || ${+galiases[openai]} )) ||
           { (( ${+functions[openai]} )) && ! [[ ${__openai_suggestions_enabled-} == 1 &&
                -n ${__openai_suggestions_wrapper_body-} && ${functions[openai]} == "$__openai_suggestions_wrapper_body" ]]; }; then
            printf '%s\n' 'Your shell selects an alias or function named openai. Rename it before enabling updates.' >&2; return 1
        fi
        setup_args=(zsh --auto-suggestions --profile "$profile_dir/.zshrc")
    else
        shell=bash; profile_dir=$HOME
        case $(builtin type -t openai) in
            function)
                if [[ ${__openai_suggestions_enabled-} != 1 || -z ${__openai_suggestions_wrapper_body-} ||
                      $(builtin declare -f openai) != "$__openai_suggestions_wrapper_body" ]]; then
                    printf '%s\n' 'Your shell selects a function named openai. Rename it before enabling updates.' >&2; return 1
                fi ;;
            alias|builtin|keyword)
                printf '%s\n' 'Your shell selects another command named openai. Rename it before enabling updates.' >&2; return 1 ;;
        esac
        setup_args=(bash --auto-suggestions)
    fi

    if [[ $entry -ef /usr/share/openai-cli/enable-updates.sh ]]; then
        installer=/usr/share/openai-cli/install.sh
        install_args=(--linux-package)
    else
        case $entry in */*) installer=${entry%/*}/install.sh ;; *) installer=./install.sh ;; esac
        if [[ -L $entry || -L $installer || ! -f $installer ]]; then
            printf '%s\n' 'Source the helper beside install.sh in the extracted OpenAI CLI release.' >&2; return 1
        fi
        install_args=(--require-gh)
    fi
    OPENAI_CLI_SOURCED_INSTALL=1 /bin/sh "$installer" "${install_args[@]}" "--$shell" "$profile_dir" || return $?
    case $PATH in "$HOME/.openai/bin"|"$HOME/.openai/bin":*) ;; *) export PATH="$HOME/.openai/bin:$PATH" ;; esac
    if [[ $shell == zsh ]]; then
        if (( ${+commands[openai]} )); then builtin unhash openai || return $?; fi
        selection=$(builtin whence -p openai)
    else
        builtin hash -r
        selection=$(builtin type -P openai)
    fi
    if [[ $selection != "$HOME/.openai/bin/openai" ]]; then
        printf '%s\n' 'This shell selected another openai. Use "$HOME/.openai/bin/openai" directly.' >&2; return 1
    fi
    printf '%s\n' 'This shell now uses $HOME/.openai/bin/openai.'
    if [[ $shell == bash ]] && (( BASH_VERSINFO[0] < 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] < 3) )); then
        return 0
    fi
    # Older or interrupted binaries must never feed partial or arbitrary text to the shell.
    if hook=$("$selection" @completion "${setup_args[@]}"); then
        case $hook in *$'\n# openai-cli-install-suggestions-complete v1') builtin eval "$hook" ;; esac
    fi
}
if [ -n "${BASH_VERSION:-}" ]; then
    __openai_setup_installed_shell "${BASH_SOURCE[0]}"
else
    __openai_setup_installed_shell "${(%):-%x}"
fi
