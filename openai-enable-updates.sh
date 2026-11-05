# Source this package-owned file from interactive Bash or zsh.
case $- in
    *i*) ;;
    *) printf '%s\n' 'In an interactive Bash or zsh shell, run: source /usr/share/openai-cli/enable-updates.sh' >&2; return 1 2>/dev/null || exit 1 ;;
esac
if [ -n "${BASH_VERSION:-}" ]; then
    if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
        printf '%s\n' 'Run: source /usr/share/openai-cli/enable-updates.sh' >&2; exit 1
    fi
    case $(builtin type -t openai) in alias|function|builtin|keyword)
        printf '%s\n' 'Your shell selects an alias or function named openai. Rename it before enabling updates.' >&2; return 1 ;;
    esac
    /bin/sh /usr/share/openai-cli/install.sh --linux-package --bash "$HOME" || return $?
elif [ -n "${ZSH_VERSION:-}" ]; then
    case $ZSH_EVAL_CONTEXT in *:file) ;; *)
        printf '%s\n' 'Run: source /usr/share/openai-cli/enable-updates.sh' >&2; return 1 2>/dev/null || exit 1 ;;
    esac
    if (( ${+aliases[openai]} || ${+galiases[openai]} || ${+functions[openai]} )); then
        printf '%s\n' 'Your shell selects an alias or function named openai. Rename it before enabling updates.' >&2; return 1
    fi
    /bin/sh /usr/share/openai-cli/install.sh --linux-package --zsh "${ZDOTDIR:-$HOME}" || return $?
else
    printf '%s\n' 'Source this installer from Bash or zsh.' >&2; return 1 2>/dev/null || exit 1
fi
case "$PATH" in
    "$HOME/.openai/bin"|"$HOME/.openai/bin":*) ;;
    *) export PATH="$HOME/.openai/bin:$PATH" ;;
esac
builtin hash -r
if [ "$(command -v openai)" != "$HOME/.openai/bin/openai" ]; then
    printf '%s\n' 'This shell selected another openai. Check command -v openai.' >&2; return 1
fi
printf '%s\n' 'This shell now uses $HOME/.openai/bin/openai.'
