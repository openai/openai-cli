# Source this launcher from an interactive Linux Bash or zsh session.
if ! (
  case $- in *i*) ;; *) exit 1 ;; esac
  if [ -n "${BASH_VERSION:-}" ]; then
    [ "${BASH_SOURCE[0]}" != "$0" ]
  else
    case ${ZSH_EVAL_CONTEXT:-} in *:file) exit 0 ;; *) exit 1 ;; esac
  fi
); then
  printf '%s\n' 'Run source ./openai.sh in an interactive Bash or zsh terminal.' >&2
  return 1 2>/dev/null || exit 1
fi

(
  installer=$(mktemp /tmp/openai-install.XXXXXXXX) || exit
  trap 'rm -f -- "$installer"' EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
  printf 'Downloading the OpenAI CLI installer...\n' >&2
  curl -qfsSL --retry 3 --retry-max-time 60 --max-time 60 --proto '=https' --proto-redir '=https' \
    -o "$installer" https://raw.githubusercontent.com/openai/openai-cli/334f332fc6a5f8298b20548d4fbbbb30c940cdc5/scripts/install-linux-release.sh || exit
  printf '%s  %s\n' b0c05af08bd5e168366063bde36147d880f90fba337ce57fff928aa5f3633ce5 "$installer" | sha256sum --check --quiet || exit
  /bin/bash -p "$installer"
) && source /usr/share/openai-cli/enable-updates.sh
