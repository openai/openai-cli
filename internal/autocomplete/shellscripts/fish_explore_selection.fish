# Only the interactive picker can fill the next prompt.
function __openai_is_interactive_explore
    test "$argv[1]" = explore; or return 1
    if test (count $argv) -ne 1
        test (count $argv) -eq 2; and test "$argv[2]" = --full-screen; or return 1
    end
    not status is-command-substitution; and isatty stdin; and isatty stdout; and isatty stderr
end

function __openai_fill_explore
    set -l selected (command "$argv[1]" $argv[3..] --shell-integration --shell fish)
    set -l code $status
    test "$code" = 0; or return $code
    test (count $selected) -eq 1; and test -n "$selected"; or return 1
    string match -rq '[[:cntrl:]]' -- "$selected"; and return 1
    test (printf %s "$selected" | command wc -c) -le 16384; or return 1
    if test "$argv[2]" = remember
        set -g __openai_suggestions "$selected"
        set -g __openai_suggestions_directory "$PWD"
        set -g __openai_suggestions_index 1
        set -g __openai_suggestions_last "$selected"
        set -g __openai_suggestions_draft ''
        set -g __openai_suggestions_cursor 0
    end
    commandline --replace -- "$selected"
    commandline --cursor (string length -- "$selected")
end
