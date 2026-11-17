#!/usr/bin/env fish

# Keep expansion syntax, but hide literal filename separators from Fish's
# secondary completion of assignment and colon suffixes.
function ____APPNAME___fish_complete_path
    if not string match -qr '[=:()]' -- "$argv[1]"
        __fish_complete_path "$argv[1]"
        return
    end
    set -l result ''
    set -l quote ''
    set -l backslash ''
    set -l dollar 0
    for c in (string split '' -- "$argv[1]")
        if test -n "$backslash"
            set backslash ''
            if not contains -- "$c" '=' ':'
                set result "$result\\$c"
                set dollar 0
                continue
            end
            # Inside quotes, the backslash before a separator is literal.
            if test -n "$quote"
                set result "$result\\\\"
            end
        else if test "$c" = '\\'
            set backslash '\\'
            set dollar 0
            continue
        else if test "$c" = '('; and begin; test -z "$quote"; or test $dollar -eq 1; end
            # Path completion must not execute a command substitution.
            return 1
        end
        set dollar 0
        if contains -- "$c" '=' ':'
            set -l encoded '\x3d'
            if test "$c" = ':'
                set encoded '\x3a'
            end
            # Hex escapes work outside quotes. Resume the original quote afterward.
            set result "$result$quote$encoded$quote"
        else
            if test "$c" = "$quote"
                set quote ''
            else if test -z "$quote"; and contains -- "$c" "'" '"'
                set quote "$c"
            else if test "$quote" = '"'; and test "$c" = '$'
                set dollar 1
            end
            set result "$result$c"
        end
    end
    __fish_complete_path "$result$backslash"
end

function ____APPNAME___fish_autocomplete
    # Fish 3 lacks expanded tokens; retain its literal tokenization fallback.
    set -l tokens (commandline -xpc 2>/dev/null)
    set -l legacy_tokens $status
    if test $legacy_tokens -ne 0
        set tokens (commandline -opc)
    end
    set -l current (commandline -ct)

    set -l cmd $tokens[1]
    set -l args $tokens[2..-1]

    set -l completions (env COMPLETION_STYLE=fish OPENAI_CLI_COMPLETION_FILE_VALUES=1 OPENAI_CLI_COMPLETION_STDIN_PATH=1 OPENAI_CLI_COMPLETION_PRESERVE_WORDS=1 $cmd __complete -- $args "$current" 2>/dev/null)
    set -l exit_code $status

    # Check for custom file completion patterns
    # Patterns can appear anywhere in the word (e.g., inside quotes: 'my file is @file://path')
    set -l prefix ""
    set -l file_part "$current"
    set -l force_file_completion 0

    if test $exit_code -ne 10; and test $exit_code -ne 12; and string match -gqr '^(?<before>.*)@(?<protocol>file://|data://)?(?<file_part>.*)$' -- $current
        set prefix "$before@$protocol"
        if string match -qr '[\'"]' -- "$before"
            # A quote can start before the @. Ask the tokenizer which closing
            # quote lets a following word begin. Carry that quote into the path.
            for quote in "'" '"'
                if test "$(commandline --input "$before@$quote x" -ct)" = x
                    set file_part "$quote$file_part"
                    break
                end
            end
            set prefix (string unescape -- "$prefix" | string collect)
        end
        set force_file_completion 1
    end

    if test $force_file_completion -eq 1
        for path in (____APPNAME___fish_complete_path "$file_part")
            printf '%s%s\n' "$prefix" "$path"
        end
    else
        switch $exit_code
            case 10 12
                # The backend returns a prefix only for an assigned file flag.
                set -l assignment "$completions"
                set -l value "$current"
                if test -n "$assignment"
                    set value (string sub -s (math (string length -- "$assignment") + 1) -- "$current")
                end
                if test $exit_code -eq 12
                    printf '%s-\tRead the request from the command before |\n' "$assignment"
                end
                # Fish treats leading --name= specially, even for file values.
                set -l literal_prefix ""
                if string match -qr '^-' -- "$value"
                    set value "./$value"
                    set literal_prefix "./"
                end
                for path in (____APPNAME___fish_complete_path "$value")
                    if test -n "$literal_prefix"
                        set path (string sub -s 3 -- "$path")
                    end
                    if test $exit_code -eq 12; and string match -qr '^-(\t|$)' -- "$path"
                        continue
                    end
                    printf '%s%s\n' "$assignment" "$path"
                end
            case 11
                # No completion
                return 0
            case 0
                # Use returned completions
                for completion in $completions
                    echo $completion
                end
        end
    end
end

complete -c __APPNAME__ -f -a '(____APPNAME___fish_autocomplete)'
