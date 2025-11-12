#!/usr/bin/env fish

function ____APPNAME___fish_autocomplete
    set -l tokens (commandline -xpc)
    set -l current (commandline -ct)

    set -l cmd $tokens[1]
    set -l args $tokens[2..-1]

    set -l completions (env COMPLETION_STYLE=fish $cmd __complete -- $args $current 2>>/tmp/fish-debug.log)
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
                # File completion
                __fish_complete_path "$current"
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

