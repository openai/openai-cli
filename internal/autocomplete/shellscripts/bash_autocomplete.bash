#!/bin/bash

____APPNAME___bash_literal_files() (
  local prefix="$1" directory=. lead="" basename="$1" pattern=""
  local ignore_case=off map_case=off match_hidden=on setting file char index

  # Keep Bash's existing tilde expansion and special-directory handling.
  case "$prefix" in
    '~'*) builtin compgen -f -- "$prefix"; return ;;
  esac
  if [[ "$prefix" == */* ]]; then
    lead="${prefix%/*}/" directory="${prefix%/*}" basename="${prefix##*/}"
    [[ -n "$directory" ]] || directory=/
  fi
  case "$directory" in
    *'$'*|..|../*|*/..|*/../*) builtin compgen -f -- "$prefix"; return ;;
  esac

  # Scope directory and option changes to this subshell. Keep the typed spelling.
  builtin shopt -u cdspell cdable_vars
  CDPATH= builtin cd -P -- "$directory" 2>/dev/null || return 0
  while IFS= read -r setting; do
    case "$setting" in
      'set completion-ignore-case on') ignore_case=on ;;
      'set completion-map-case on') map_case=on ;;
      'set match-hidden-files off') match_hidden=off ;;
    esac
  done < <(builtin bind -v 2>/dev/null)
  builtin shopt -u nocaseglob dotglob
  [[ "$ignore_case" == on ]] && builtin shopt -s nocaseglob
  [[ "$match_hidden" == on ]] && builtin shopt -s dotglob

  # compgen -f can dequote an already decoded filename during active completion.
  # Escape its basename as a glob instead, with one intentional trailing wildcard.
  for ((index = 0; index < ${#basename}; index++)); do
    char="${basename:index:1}"
    case "$char" in
      '\'|'*'|'?'|'['|']'|'('|')'|'|'|'!'|'+'|'@') pattern+="\\$char" ;;
      '-'|'_')
        if [[ "$ignore_case" == on && "$map_case" == on ]]; then
          pattern+='[-_]'
        else
          pattern+="$char"
        fi
        ;;
      *) pattern+="$char" ;;
    esac
  done
  if [[ -z "$basename" || "$basename" == . || "$basename" == .. ]]; then
    while IFS= read -r file; do
      [[ -z "$basename" && "$match_hidden" == off && "$file" == .* ]] && continue
      printf '%s\n' "$lead$file"
    done < <(builtin compgen -f -- "$basename")
  else
    while IFS= read -r file; do
      printf '%s\n' "$lead$file"
    done < <(builtin compgen -G "$pattern*")
  fi
)

____APPNAME___bash_autocomplete() {
  if [[ "${COMP_WORDS[0]}" != "source" ]]; then
    local cur completions exit_code file
    local IFS=$'\n'
    cur="${COMP_WORDS[COMP_CWORD]}"

    local -a completion_args=()
    local word_index previous raw source before after current_raw="$cur"
    local -a adjacent=()
    if [[ ${BASH_VERSINFO[0]} -lt 4 ||
          ( ${BASH_VERSINFO[0]} -eq 4 && ${BASH_VERSINFO[1]} -lt 3 ) ]]; then
      # Before Bash 4.3, COMP_POINT counts bytes; substring expansion counts characters.
      builtin printf -v source '%.*s' "$COMP_POINT" "$COMP_LINE"
    else
      source="${COMP_LINE:0:COMP_POINT}"
    fi
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
    local token value quote char next offset argument current_word=""
    local callback_raw="${2-$current_raw}" callback_start=-1 file_word_start=-1 file_current_word=""
    local last_argument=$((${#completion_args[@]} - 1))
    # Map the callback's raw suffix into the fully decoded last argument.
    # An open quote can omit its opening delimiter from the callback word.
    if [[ $# -ge 2 && $last_argument -ge 0 && "${completion_args[last_argument]}" == *"$callback_raw" ]]; then
      callback_start=$((${#completion_args[last_argument]} - ${#callback_raw}))
    fi
    # Decode Readline's replacement word separately from the rejoined arguments.
    local -a words_to_decode=("${completion_args[@]}" "$callback_raw")
    for ((argument = 0; argument < ${#words_to_decode[@]}; argument++)); do
      token="${words_to_decode[argument]}"
      value="" quote=""
      for ((offset = 0; offset < ${#token}; offset++)); do
        if [[ $argument -eq $last_argument && $offset -eq $callback_start ]]; then
          file_word_start=${#value}
        fi
        char="${token:offset:1}"
        if [[ "$quote" == "'" ]]; then
          if [[ "$char" == "'" ]]; then quote=""; else value+="$char"; fi
        elif [[ "$char" == "\\" && "$quote" != "'" ]]; then
          next="${token:offset+1:1}"
          if [[ -z "$quote" || "$next" == "\\" || "$next" == '"' || "$next" == '$' || "$next" == '`' ]]; then
            # Both raw characters of an escape produce the same decoded character.
            if [[ $argument -eq $last_argument && $callback_start -eq $((offset + 1)) ]]; then
              file_word_start=${#value}
            fi
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
      if [[ $argument -lt ${#completion_args[@]} ]]; then
        completion_args[argument]="$value"
        if [[ $argument -eq $last_argument ]]; then
          [[ $callback_start -eq ${#token} ]] && file_word_start=${#value}
          if [[ $file_word_start -ge 0 ]]; then file_current_word="${value:file_word_start}"; fi
        fi
      else
        current_word="$value"
      fi
    done
    completions=$(COMPLETION_STYLE=bash OPENAI_CLI_COMPLETION_FILE_VALUES=1 OPENAI_CLI_COMPLETION_PRESERVE_WORDS=1 "${COMP_WORDS[0]}" __complete -- "${completion_args[@]}" 2>/dev/null)
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
        local assignment="$completions" replacement_prefix="" replacement_word="$current_word"
        [[ $file_word_start -ge 0 ]] && replacement_word="$file_current_word"
        file_part="${current_value#"$assignment"}"
        # Readline replaces the callback word, after the last '=' or ':'.
        # Restore only the part that Readline includes in its replacement.
        if [[ $# -ge 2 && "$current_value" == *"$replacement_word" ]]; then
          replacement_prefix="${current_value%"$replacement_word"}"
        fi
        while IFS= read -r file; do
          file="$assignment$file"
          COMPREPLY+=("${file#"$replacement_prefix"}")
        done < <(____APPNAME___bash_literal_files "$file_part")
        ;;
      11) COMPREPLY=() ;; # no completion
      0)
        COMPREPLY=()
        if [[ -n "$completions" ]]; then
          local command_prefix=""
          # The backend returns only the suffix after the last colon.
          # Restore colons inside the word Readline replaces, including quoted
          # or escaped colons. Separate ':' words already stay on the line.
          if [[ "$current_word" == *:* && -n "${current_word//:}" ]]; then
            command_prefix="${current_word%:*}:"
          fi
          while IFS= read -r file; do
            COMPREPLY+=("$command_prefix$file")
          done <<<"$completions"
        fi
        ;;
      esac
    fi
    return 0
  fi
}

complete -o filenames -F ____APPNAME___bash_autocomplete __APPNAME__
