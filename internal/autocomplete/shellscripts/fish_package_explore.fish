# The system package handles bare Explore only. It captures no other commands.
if status is-interactive
    function __openai_package_explore_allowed
        set -q HOME[1]; and string match --quiet '/*' -- "$HOME"; or return 1
        set -l marker "$HOME/.openai/shell/command-insertion.off-fish"
        not test -e "$marker"; and not test -L "$marker"
    end
    function __openai_package_explore_owns_command
        set -q __openai_package_explore_body; and functions -q openai; and test (functions openai | string collect) = "$__openai_package_explore_body"
    end
    if __openai_package_explore_allowed; and not functions -q __openai_package_explore_on_prompt
        function __openai_package_explore_on_prompt --on-event fish_prompt
            functions --erase __openai_package_explore_on_prompt
            __openai_package_explore_allowed; or return
            isatty stdin; and isatty stdout; and isatty stderr; and test -n "$TERM"; and test "$TERM" != dumb; or return
            functions -q openai; and return
            functions -q __openai_suggestions_load_current; and return
            command -sq openai; and command /usr/bin/test (command -s openai) -ef /usr/bin/openai 2>/dev/null; or return
            builtin source /usr/share/openai-cli/explore-selection.fish; or return
            function openai
                if __openai_is_bare_explore $argv; and __openai_package_explore_allowed; and command /usr/bin/test (command -s openai) -ef /usr/bin/openai 2>/dev/null
                    __openai_fill_explore /usr/bin/openai
                    return $status
                end
                command openai $argv
            end
            set -g __openai_package_explore_body (functions openai | string collect)
        end
    end
end
