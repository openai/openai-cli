#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
eval "$(openai @completion bash)"

show_values() {
  local expected="$1"
  shift
  COMP_WORDS=(openai "$@")
  COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
  COMP_LINE="${COMP_WORDS[*]}"
  COMP_POINT=${#COMP_LINE}
  printf '$ %s<Tab>\n' "$COMP_LINE"
  __openai_bash_autocomplete openai "${COMP_WORDS[COMP_CWORD]}" "${COMP_WORDS[COMP_CWORD-1]}"
  local actual="${COMPREPLY[*]-}"
  if [ "$DEMO_SCENE_SIDE" = before ]; then
    test -z "$actual"
    printf '(no value suggestions)\n'
  else
    test "$actual" = "$expected"
    printf '%s\n' "$actual"
  fi
  printf '\n'
}

show_values 'json jsonl' --format j
show_values 'json jsonl' --format-error j
show_values 'user_data' files upload sample.jsonl --purpose u
show_values 'batch batch_output' files list --purpose b
sleep 3
