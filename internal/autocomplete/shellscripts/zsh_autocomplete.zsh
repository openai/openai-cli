#compdef __APPNAME__

____APPNAME___zsh_autocomplete() {

  local -a opts
  local temp
  local exit_code

  # The backend completes the last argument, so stop at the cursor's word.
  temp=$(COMPLETION_STYLE=zsh OPENAI_CLI_COMPLETION_FILE_VALUES=1 OPENAI_CLI_COMPLETION_STATIC_VALUES=1 "${words[1]}" __complete "${words[@]:1:$((CURRENT - 1))}")
  exit_code=$?

  # Check for custom file completion patterns
  # Patterns can appear anywhere in the word (e.g., inside quotes: 'my file is @file://path')
  local cur="${words[CURRENT]}"

  if [[ "$exit_code" -ne 10 && "$cur" = *'@'* ]]; then
    # Extract everything after the last @
    local after_last_at="${cur##*@}"

    if [[ $after_last_at =~ ^(file://|data://) ]]; then
      compset -P "*$MATCH"
      _files
    else
      compset -P '*@'
      _files
    fi
    return
  fi

  case $exit_code in
    10)
      # The backend returns a prefix only for an assigned file flag.
      if [[ -n "$temp" ]]; then
        compset -P "${(b)temp}"
      fi
      _files
      ;;
    11)
      # No completion behavior - return nothing
      return 1
      ;;
    0)
      # Default behavior - show command completions
      opts=("${(@f)temp}")
      # A normal suffix lands inside an existing closing quote. Keep the
      # completed argument exact; ordinary unquoted spacing stays unchanged.
      if [[ "$cur" == \'*\' || "$cur" == \"*\" ]]; then
        _describe 'values' opts -S ''
      else
        _describe 'values' opts
      fi
      ;;
  esac
}

# When installed in fpath (e.g., via Homebrew's zsh_completion stanza), this file
# is autoloaded as the function ___APPNAME__ and its body becomes that function's
# body. Detect that case via funcstack and dispatch to the completion function.
# When sourced (e.g., `source <(__APPNAME__ @completion zsh)`), register the
# function with compdef instead.
if [[ "${funcstack[1]}" = "___APPNAME__" ]]; then
  ____APPNAME___zsh_autocomplete "$@"
else
  compdef ____APPNAME___zsh_autocomplete __APPNAME__
fi
