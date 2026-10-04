# Optional image picker. Source after the ordinary fish completions.
# Keep custom bindings as shell-owned commands; never evaluate the input buffer.
if not status is-interactive; or not isatty stdin; or not isatty stdout; or not isatty stderr
    return
end
if test "$TERM" = dumb; or test -z "$TERM"
    return
end
if set -q __openai_picker_modes
    return
end

function openai_picker_disable
    if not set -q __openai_picker_modes
        return
    end
    for slot in (seq (count $__openai_picker_modes))
        set -l wrapper $__openai_picker_wrappers[$slot]
        set -g $wrapper\_active 0
        set -l mode $__openai_picker_modes[$slot]
        set -l owned_name __openai_picker_owned_$slot
        set -l prior_name __openai_picker_prior_$slot
        if test "$(bind --user --mode $mode \t 2>/dev/null | string collect)" = "$$owned_name"
            bind --erase --user --mode $mode \t
            if test -n "$$prior_name"
                printf '%s\n' $$prior_name | source
            end
            functions --erase $wrapper
            set --erase --global $wrapper\_active
        end
        set --erase --global __openai_picker_owned_$slot __openai_picker_prior_$slot
    end
    set --erase --global __openai_picker_modes __openai_picker_wrappers
end

# fish initializes its default bindings lazily. Resolve them before saving Tab.
if not set -q fish_key_bindings
    fish_default_key_bindings
end
set -g __openai_picker_modes
set -g __openai_picker_wrappers
if not set -q __openai_picker_serial
    set -g __openai_picker_serial 0
end
for mode in default insert
    set -l prior (bind --user --mode $mode \t 2>/dev/null | string collect)
    set -l binding "$prior"
    if test -z "$binding"
        set binding (bind --preset --mode $mode \t 2>/dev/null | string collect)
    end
    if test -z "$binding"
        continue
    end
    # bind emits shell-escaped words. Tokenize them without executing anything.
    set -l words
    printf '%s\n' "$binding" | read --tokenize --array words
    set -l position 2
    set -l next_mode ''
    while string match --quiet -- '-*' "$words[$position]"
        switch $words[$position]
            case -M --mode
                set position (math $position + 2)
            case -m --sets-mode
                set next_mode $words[(math $position + 1)]
                set position (math $position + 2)
            case --preset --user
                set position (math $position + 1)
            case '*'
                break
        end
    end
    set -l commands $words[(math $position + 1)..-1]
    if test (count $commands) -eq 0
        continue
    end
    set -l input_functions (bind --function-names)
    set -l all_input 1
    for original_command in $commands
        if not contains -- "$original_command" $input_functions
            set all_input 0
        end
    end
    set -g --append __openai_picker_modes $mode
    set -l slot (count $__openai_picker_modes)
    set -g __openai_picker_prior_$slot "$prior"
    set -g __openai_picker_serial (math $__openai_picker_serial + 1)
    set -l wrapper __openai_picker_tab_$__openai_picker_serial
    set -l active_name $wrapper\_active
    set -g $active_name 1
    set -g --append __openai_picker_wrappers $wrapper
    function $wrapper --inherit-variable commands --inherit-variable all_input --inherit-variable next_mode --inherit-variable active_name
        # Keep commandline's one output newline in the pattern. Removing it via
        # command substitution would also trim newlines in the editor buffer.
        set -l buffer (commandline --current-buffer | string collect --no-trim-newlines)
        set -l app (string escape --style=regex -- '__APPNAME__')
        if test "$$active_name" = 1; and isatty stdin; and isatty stdout; and isatty stderr; and test "$TERM" != dumb; and test (commandline --cursor) -eq (math (string length -- "$buffer") - 1); and string match --quiet --regex -- "\\A[ \\t]*"$app"[ \\t]+images[ \\t]+generate[ \\t]*\\n\\z" "$buffer"; and test "$(type --type __APPNAME__ 2>/dev/null)" = file__FISH_PICKER_COMMAND_GUARD__
            # The current command resolves to an external executable.
            printf '\n'
            env OPENAI_PICKER_SHELL=fish __APPNAME__ images generate
            commandline --function repaint
            return
        end

        if test "$all_input" = 1
            commandline --function $commands
        else
            # These are the original, trusted fish binding commands. They are not
            # commandline text, completions, or output from the CLI.
            for original_command in $commands
                eval $original_command
            end
        end
        if test -n "$next_mode"
            set -g fish_bind_mode $next_mode
        end
    end

    bind --user --mode $mode \t $wrapper
    set -g __openai_picker_owned_$slot (bind --user --mode $mode \t | string collect)
end
