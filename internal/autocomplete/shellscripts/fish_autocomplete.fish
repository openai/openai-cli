#!/usr/bin/env fish

function ____APPNAME___fish_autocomplete
    set -l tokens (commandline -xpc)
    set -l current (commandline -ct)

    set -l cmd $tokens[1]
    set -l args $tokens[2..-1]

    set -l completions (env COMPLETION_STYLE=fish $cmd __complete -- $args $current 2>/dev/null)
    set -l exit_code $status

    # Check for custom file completion patterns
    # Patterns can appear anywhere in the word (e.g., inside quotes: 'my file is @file://path')
    set -l prefix ""
    set -l file_part "$current"
    set -l force_file_completion 0

    if string match -gqr '^(?<before>.*)@(?<protocol>file://|data://)?(?<file_part>.*)$' -- $current
        if string match -qr '^[\'"]' -- $before
            # Ensures we don't insert an extra quote when the user is building an argument in quotes
            set before (string sub -s 2 -- $before)
        end

        set prefix "$before@$protocol"
        set force_file_completion 1
    end

    if test $force_file_completion -eq 1
        for path in (__fish_complete_path "$file_part")
            echo $prefix$path
        end
    else
        switch $exit_code
            case 10
                # The backend returns a prefix only for an assigned file flag.
                set -l assignment "$completions"
                set -l value "$current"
                if test -n "$assignment"
                    set value (string sub -s (math (string length -- "$assignment") + 1) -- "$current")
                end
                # Fish treats leading --name= specially, even for file values.
                set -l literal_prefix ""
                if string match -qr '^-' -- "$value"
                    set value "./$value"
                    set literal_prefix "./"
                end
                for path in (__fish_complete_path "$value")
                    if test -n "$literal_prefix"
                        set path (string sub -s 3 -- "$path")
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

