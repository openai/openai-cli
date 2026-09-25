#!/bin/bash

____APPNAME___bash_autocomplete() {
  if [[ "${COMP_WORDS[0]}" != "source" ]]; then
    local cur completions exit_code
    local IFS=$'\n'
    cur="${COMP_WORDS[COMP_CWORD]}"

    local -a completion_args=()
    local word_index
    for ((word_index = 1; word_index <= COMP_CWORD; word_index++)); do
      completion_args+=("${COMP_WORDS[word_index]}")
    done
    completions=$(COMPLETION_STYLE=bash "${COMP_WORDS[0]}" __complete -- "${completion_args[@]}" 2>/dev/null)
    exit_code=$?

    local last_token="$cur"

    # Bash includes ':' in COMP_WORDBREAKS. Rebuild the current shell word so
    # file completion receives the full prefix (for example 'cert:models')
    # rather than only the suffix after the last colon.
    local token_index=$COMP_CWORD
    while [[ $token_index -ge 2 && "${COMP_WORDS[token_index - 1]}" == ":" ]]; do
      last_token="${COMP_WORDS[token_index - 2]}:$last_token"
      token_index=$((token_index - 2))
    done

    # Check for custom file completion patterns
    local prefix=""
    local file_part="$last_token"
    local force_file_completion=false
    if [[ "$last_token" =~ (.*)@(file://|data://)?(.*)$ ]]; then
      local before_at="${BASH_REMATCH[1]}"
      local protocol="${BASH_REMATCH[2]}"
      file_part="${BASH_REMATCH[3]}"

      if [[ "$protocol" == "" ]]; then
        prefix="$before_at@"
      else
        if [[ "$before_at" == "" ]]; then
          prefix="//"
        else
          prefix="$before_at@$protocol"
        fi
      fi

      force_file_completion=true
    fi

    if [[ "$force_file_completion" == true ]]; then
      local file
      COMPREPLY=()
      while IFS= read -r file; do
        COMPREPLY+=("$prefix$file")
      done < <(compgen -f -- "$file_part")
    else
      case $exit_code in
      10)
        mapfile -t COMPREPLY < <(compgen -f -- "$last_token")
        # Readline only replaces the suffix after its last colon word break.
        # Keep the full path for lookup, but omit the prefix it already retains.
        local retained_prefix="${last_token%"$cur"}"
        local index
        for index in "${!COMPREPLY[@]}"; do
          COMPREPLY[$index]="${COMPREPLY[$index]#"$retained_prefix"}"
        done
        ;;
      11) COMPREPLY=() ;;                                   # no completion
      0) mapfile -t COMPREPLY <<<"$completions" ;;          # use returned completions
      esac
    fi
    return 0
  fi
}

complete -o filenames -F ____APPNAME___bash_autocomplete __APPNAME__
