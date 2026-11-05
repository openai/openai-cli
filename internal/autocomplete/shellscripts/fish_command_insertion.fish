# Current-session command insertion. Ctrl-X, then Tab searches displayed commands.
# Existing bindings stay intact. Double Tab is a separate opt-in.
if status is-interactive; and isatty stdin; and isatty stdout; and isatty stderr; and test "$TERM" != dumb
    function ____APPNAME___suggestions_owns_command
        set -q ____APPNAME___suggestions_wrapper_body; or return 1
        functions -q __APPNAME__; or return 1
        test (functions __APPNAME__ | string collect) = "$____APPNAME___suggestions_wrapper_body"
    end

    function ____APPNAME___suggestions_clear
        set -g ____APPNAME___suggestions
        set -g ____APPNAME___suggestions_index 0
        set -g ____APPNAME___suggestions_last ''
    end

    function ____APPNAME___suggestions_read
        # read --null preserves embedded/trailing newlines. The upstream head bounds bytes.
        read --null --local data
        set -l bytes (printf %s "$data" | command wc -c)
        if test "$bytes" -gt 2097152; or not string match -rq '\n\z' -- "$data"
            return 1
        end
        set -l total 0
        set -l commands
        printf %s "$data" | while read -l line
            set -l size (printf %s "$line" | command wc -c)
            set total (math $total + $size + 1)
            if test -z "$line"; or string match -rq '[[:cntrl:]]' -- "$line"; or test "$size" -gt 16384; or test (count $commands) -ge 128; or test "$total" -gt 2097152
                return 1
            end
            set -a commands "$line"
        end
        set -g ____APPNAME___suggestions $commands
    end

    function ____APPNAME___suggestions_collect
        set -l file $argv[1]
        set -e argv[1]
        set -g ____APPNAME___suggestions_started 1
        if not command rm -f -- "$file" 2>/dev/null
            set -lx OPENAI_CLI_COMMANDS_FD ''
            set -lx OPENAI_CLI_COMMANDS_SHELL ''
            command __APPNAME__ $argv 9>&-
            return $status
        end
        set -l code
        begin
            set -lx OPENAI_CLI_COMMANDS_FD 9
            set -lx OPENAI_CLI_COMMANDS_SHELL fish
            command __APPNAME__ $argv
            set code $status
        end
        # fish command substitutions do not inherit function-scoped FD8.
        # A direct pipeline keeps its descriptor and runs the reader in this shell.
        command head -c 2097153 <&8 2>/dev/null | ____APPNAME___suggestions_read
        set -g ____APPNAME___suggestions_directory "$PWD"
        return $code
    end

    function ____APPNAME___suggestions_run
        ____APPNAME___suggestions_clear
        if test (count $argv) -eq 1; and test "$argv[1]" = explore; and not status is-command-substitution; and isatty stdin; and isatty stdout; and isatty stderr
            set -l selected (command __APPNAME__ explore --shell-integration --shell fish)
            set -l code $status
            test "$code" = 0; or return $code
            test (count $selected) -eq 1; and test -n "$selected"; or return 1
            string match -rq '[[:cntrl:]]' -- "$selected"; and return 1
            test (printf %s "$selected" | command wc -c) -le 16384; or return 1
            set -g ____APPNAME___suggestions "$selected"
            set -g ____APPNAME___suggestions_directory "$PWD"
            set -g ____APPNAME___suggestions_index 1
            set -g ____APPNAME___suggestions_last "$selected"
            set -g ____APPNAME___suggestions_draft ''
            set -g ____APPNAME___suggestions_cursor 0
            commandline --replace -- "$selected"
            commandline --cursor (string length -- "$selected")
            return 0
        end
        if command test -e /dev/fd/7; or command test -e /dev/fd/8; or command test -e /dev/fd/9
            command __APPNAME__ $argv
            return $status
        end
        set -l base /tmp
        if set -q TMPDIR; and test -n "$TMPDIR"
            set base "$TMPDIR"
        end
        set -l file (command mktemp "$base/__APPNAME__-suggestions.XXXXXXXX" 2>/dev/null)
        if test $status -ne 0
            command __APPNAME__ $argv
            return $status
        end
        set -g ____APPNAME___suggestions_started 0
        ____APPNAME___suggestions_collect "$file" $argv 7>&2 2>/dev/null 8<"$file" 9>"$file" 2>&7 7>&-
        set -l code $status
        command rm -f -- "$file" 2>/dev/null
        if test "$____APPNAME___suggestions_started" = 0
            command __APPNAME__ $argv
            return $status
        end
        return $code
    end

    function __APPNAME___suggestion_restore
        set -l current (commandline --current-buffer | string collect --no-trim-newlines)
        set -l length (math (string length -- "$current") - 1)
        if test "$length" -gt 0
            printf %s "$current" | read --null --nchars $length current
        else
            set current ''
        end
        if test "$____APPNAME___suggestions_index" -gt 0; and test "$current" = "$____APPNAME___suggestions_last"
            commandline --replace -- "$____APPNAME___suggestions_draft"
            commandline --cursor $____APPNAME___suggestions_cursor
        end
        set -g ____APPNAME___suggestions_index 0
        set -g ____APPNAME___suggestions_last ''
    end

    function __APPNAME___suggestion_next
        test "$____APPNAME___suggestions_enabled" = 1; or return 0
        if test "$PWD" != "$____APPNAME___suggestions_directory"
            ____APPNAME___suggestions_clear
        end
        test (count $____APPNAME___suggestions) -gt 0; or return 0
        set -l current (commandline --current-buffer | string collect --no-trim-newlines)
        set -l length (math (string length -- "$current") - 1)
        if test "$length" -gt 0
            printf %s "$current" | read --null --nchars $length current
        else
            set current ''
        end
        set -l selected (printf '%s\n' $____APPNAME___suggestions | command __APPNAME__ explore --suggestions --shell-integration --shell fish)
        set -l code $status
        test "$code" = 0; or return 0
        test (count $selected) -eq 1; and test -n "$selected"; or return 0
        set -l row (contains --index -- "$selected" $____APPNAME___suggestions)
        test -n "$row"; or return 0
        if test "$____APPNAME___suggestions_index" = 0; or test "$current" != "$____APPNAME___suggestions_last"
            set -g ____APPNAME___suggestions_draft "$current"
            set -g ____APPNAME___suggestions_cursor (commandline --cursor)
        end
        set -g ____APPNAME___suggestions_index $row
        set -g ____APPNAME___suggestions_last "$selected"
        commandline --replace -- "$selected"
        commandline --cursor (string length -- "$selected")
    end

    function ____APPNAME___suggestions_chpwd --on-variable PWD
        ____APPNAME___suggestions_clear
    end

    function ____APPNAME___suggestions_preexec --on-event fish_preexec
        ____APPNAME___suggestions_clear
    end

    function __APPNAME___command_insertion_disable
        set -g ____APPNAME___suggestions_enabled 0
        for keymap in default insert
            for key in \cx\ci \ci\ci
                set -l binding (bind --user -M $keymap $key 2>/dev/null | string collect)
                if string match -rq ' __APPNAME___suggestion_next$' -- "$binding"
                    bind --erase --user -M $keymap $key
                end
            end
        end
        if ____APPNAME___suggestions_owns_command
            functions --erase __APPNAME__
        end
        functions --erase ____APPNAME___suggestions_chpwd
        functions --erase ____APPNAME___suggestions_preexec
        ____APPNAME___suggestions_clear
    end

    function ____APPNAME___suggestions_install
        if functions -q __APPNAME__; and not ____APPNAME___suggestions_owns_command
            return 0
        end
        command -q __APPNAME__; or return 0
        function __APPNAME__
            ____APPNAME___suggestions_run $argv
        end
        set -g ____APPNAME___suggestions_wrapper_body (functions __APPNAME__ | string collect)
        set -g ____APPNAME___suggestions_enabled 1
        set -g ____APPNAME___suggestions_directory "$PWD"
        ____APPNAME___suggestions_clear
        set -l keys \cx\ci
        if test __DOUBLE_TAB__ = 1
            set -a keys \ci\ci
        end
        for keymap in default insert
            for key in $keys
                if not bind --user -M $keymap $key >/dev/null 2>&1; and not bind --preset -M $keymap $key >/dev/null 2>&1
                    bind --user -M $keymap $key __APPNAME___suggestion_next
                end
            end
        end
    end
    ____APPNAME___suggestions_install
end
