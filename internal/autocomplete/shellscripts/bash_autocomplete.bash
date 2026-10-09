#!/bin/bash

____APPNAME___bash_autocomplete() {
  if [[ "${COMP_WORDS[0]}" != "source" ]]; then
    local cur completions exit_code file
    local IFS=$'\n'
    cur="${COMP_WORDS[COMP_CWORD]}"

    local -a completion_args=()
    local word_index previous raw source before after current_raw="$cur"
    local -a adjacent=()
    source="${COMP_LINE:0:COMP_POINT}"
    # Readline splits '=' and ':' into words. Preserve source gaps when joining
    # those words. Walk backwards so earlier commands cannot interfere.
    for ((word_index = COMP_CWORD; word_index >= 0; word_index--)); do
      raw="${COMP_WORDS[word_index]}"
      if [[ $word_index -eq $COMP_CWORD && -n "$source" ]]; then
        # COMP_WORDS can include closing quotes beyond the cursor.
        while [[ -n "$raw" && "$source" != *"$raw" ]]; do
          raw="${raw:0:${#raw}-1}"
        done
        current_raw="$raw"
      fi
      if [[ -n "$raw" && "$source" == *"$raw"* ]]; then
        before="${source%"$raw"*}"
        after="${source#"$before$raw"}"
        [[ -z "$after" ]] && adjacent[word_index]=1
        source="$before"
      fi
    done
    for ((word_index = 1; word_index <= COMP_CWORD; word_index++)); do
      raw="${COMP_WORDS[word_index]}"
      [[ $word_index -eq $COMP_CWORD ]] && raw="$current_raw"
      previous=$((word_index - 1))
      if [[ ${#completion_args[@]} -gt 0 && "${adjacent[previous]-}" == 1 &&
            ( ( -n "$raw" && -z "${raw//[=:]/}" ) ||
              ( -n "${COMP_WORDS[previous]}" && -z "${COMP_WORDS[previous]//[=:]/}" ) ) ]]; then
        previous=$((${#completion_args[@]} - 1))
        completion_args[previous]+="$raw"
      else
        completion_args+=("$raw")
      fi
    done
    # Remove shell quoting without evaluating substitutions or expressions.
    local token value quote char next offset argument
    for ((argument = 0; argument < ${#completion_args[@]}; argument++)); do
      token="${completion_args[argument]}"
      value="" quote=""
      for ((offset = 0; offset < ${#token}; offset++)); do
        char="${token:offset:1}"
        if [[ "$quote" == "'" ]]; then
          if [[ "$char" == "'" ]]; then quote=""; else value+="$char"; fi
        elif [[ "$char" == "\\" && "$quote" != "'" ]]; then
          next="${token:offset+1:1}"
          if [[ -z "$quote" || "$next" == "\\" || "$next" == '"' || "$next" == '$' || "$next" == '`' ]]; then
            value+="$next"
            ((offset++))
          else
            value+="$char"
          fi
        elif [[ -n "$quote" && "$char" == "$quote" ]]; then
          quote=""
        elif [[ -z "$quote" && ( "$char" == "'" || "$char" == '"' ) ]]; then
          quote="$char"
        else
          value+="$char"
        fi
      done
      completion_args[argument]="$value"
    done
    local static_values=1
    # Filename mode can escape closed empty quotes without a common prefix.
    # Keep that argument intact until the user supplies a value prefix.
    if [[ "${2-}" == "''" || "${2-}" == '""' ]]; then
      static_values=0
    fi
    completions=$(COMPLETION_STYLE=bash OPENAI_CLI_COMPLETION_FILE_VALUES=1 OPENAI_CLI_COMPLETION_STATIC_VALUES="$static_values" "${COMP_WORDS[0]}" __complete -- "${completion_args[@]}" 2>/dev/null)
    exit_code=$?

    local last_token="$cur"

    # If the last token has been split apart by a ':', join it back together.
    # Ex: 'a:b' will be represented in COMP_WORDS as 'a', ':', 'b'
    if [[ $COMP_CWORD -ge 2 ]]; then
      local prev2="${COMP_WORDS[COMP_CWORD - 2]}"
      local prev1="${COMP_WORDS[COMP_CWORD - 1]}"
      if [[ "$prev2" =~ ^@(file|data)$ && "$prev1" == ":" && "$cur" =~ ^// ]]; then
        last_token="$prev2:$cur"
      fi
    fi

    # Check for custom file completion patterns
    local prefix=""
    local file_part="$cur"
    local force_file_completion=false
    if [[ "$exit_code" -ne 10 && "$last_token" =~ (.*)@(file://|data://)?(.*)$ ]]; then
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
      COMPREPLY=()
      while IFS= read -r file; do
        COMPREPLY+=("$prefix$file")
      done < <(compgen -f -- "$file_part")
    else
      case $exit_code in
      10) # File completion, including Bash 3.2 (which has no mapfile).
        COMPREPLY=()
        local current_value="${completion_args[${#completion_args[@]} - 1]}"
        local assignment="$completions" replacement_prefix=""
        file_part="${current_value#"$assignment"}"
        # Readline replaces the callback word, after the last '=' or ':'.
        # Restore only the part that Readline includes in its replacement.
        if [[ $# -ge 2 && "$current_value" == *"$2" ]]; then
          replacement_prefix="${current_value%"$2"}"
        fi
        while IFS= read -r file; do
          file="$assignment$file"
          COMPREPLY+=("${file#"$replacement_prefix"}")
        done < <(compgen -f -- "$file_part")
        ;;
      11) COMPREPLY=() ;; # no completion
      0)
        COMPREPLY=()
        if [[ -n "$completions" ]]; then
          local value_prefix="" current_value="${completion_args[${#completion_args[@]} - 1]}"
          # Static values contain no '='. A callback word retaining '=' means
          # Readline replaces the assignment too, including whole-word quotes.
          if [[ "$current_value" == -*=* && "${2-}" == *=* ]]; then
            value_prefix="${current_value%%=*}="
          fi
          while IFS= read -r file; do
            COMPREPLY+=("$value_prefix$file")
          done <<<"$completions"
          local value_flag="${current_value%%=*}" candidate
          if [[ "$current_value" != -*=* ]]; then
            value_flag=""
            if [[ ${#completion_args[@]} -gt 1 ]]; then
              value_flag="${completion_args[${#completion_args[@]} - 2]}"
            fi
          fi
          if [[ "$value_flag" == -* ]]; then
            while [[ "$value_flag" == -* ]]; do value_flag="${value_flag#-}"; done
            case "$value_flag" in
            format|format-error|purpose)
              # Filename mode would append '/' to static replacement words.
              # Decline the whole set, preserving command/flag completion.
              for candidate in "${COMPREPLY[@]}"; do
                if [[ -d "$candidate" ]]; then
                  COMPREPLY=()
                  break
                fi
              done
              ;;
            esac
          fi
        fi
        ;;
      esac
    fi
    return 0
  fi
}

complete -o filenames -F ____APPNAME___bash_autocomplete __APPNAME__
