# Optional executable suggestions. Source after your other shell configuration.
# Inspect only the missing executable. Never replay commands or inspect arguments.
# Hide newly captured definitions from tracing. The sourced scope restores tracing.
set -l fish_trace ''
if not status is-interactive
    return 0
end
set -l __openai_suggestions_version (string split . -- $version)
if test "$__openai_suggestions_version[1]" -lt 3; or begin; test "$__openai_suggestions_version[1]" -eq 3; and test "$__openai_suggestions_version[2]" -lt 2; end
    printf '%s\n' 'Executable suggestions require fish 3.2 or newer.' >&2
    return 1
end
if set -q __openai_suggestions_app
    if test "$__openai_suggestions_app" != '__APPNAME__'
        printf '%s\n' 'Run openai_suggestions_disable before enabling suggestions for another executable.' >&2
        return 1
    end
    return 0
end
if functions --query openai_suggestions_disable
    set -l disable_definition (functions openai_suggestions_disable | string collect)
    if test "$disable_definition" != "$__openai_suggestions_disable_owned"
        printf '%s\n' 'Executable suggestions conflict with existing shell functions. Keep the existing handler and use CLI help.' >&2
        return 1
    end
end
if functions --query '__APPNAME__'; or builtin --query '__APPNAME__'; or not command --query -- '__APPNAME__'
    printf '%s\n' 'Executable suggestions require __APPNAME__ on PATH. Install the CLI before enabling suggestions.' >&2
    return 1
end

# Native copies preserve execution source and inherited values. Event bindings
# and caller-variable lookup require a different lifecycle; leave them unchanged.
set -l serial 0
if set -q __openai_suggestions_serial
    set serial $__openai_suggestions_serial
end
set serial (math $serial + 1)
set -l active_name __openai_suggestions_active_$serial
set -l prior_name __openai_suggestions_prior_$serial
set -l current_name __openai_suggestions_current_$serial
if set -q $active_name
    printf '%s\n' 'Executable suggestions conflict with existing shell state. Keep the existing handler and use CLI help.' >&2
    return 1
end
if functions --query $prior_name; or functions --query $current_name
    printf '%s\n' 'Executable suggestions conflict with existing shell functions. Keep the existing handler and use CLI help.' >&2
    return 1
end
set -l had_prior 0
# This query loads the normal platform handler before inspecting its metadata.
if functions --query fish_command_not_found
    set -l prior_details (functions --details --verbose fish_command_not_found)
    if test "$prior_details[4]" = no-scope-shadowing
        printf '%s\n' 'Executable suggestions cannot retain a handler that reads caller variables. Keep the existing handler and use CLI help.' >&2
        return 1
    end
    if functions --handlers | string match --quiet --regex -- '[[:space:]]fish_command_not_found[[:space:]]*$'
        printf '%s\n' 'Executable suggestions cannot retain this custom event handler. Keep the existing handler and use CLI help.' >&2
        return 1
    end
    if not functions --copy fish_command_not_found $prior_name
        printf '%s\n' 'Executable suggestions could not preserve the prior handler. Keep the existing handler and use CLI help.' >&2
        return 1
    end
    set had_prior 1
end
set -g __openai_suggestions_serial $serial
set -g __openai_suggestions_app '__APPNAME__'
set -g $active_name 1

function fish_command_not_found --inherit-variable active_name --inherit-variable had_prior --inherit-variable prior_name --inherit-variable current_name
    set -l fish_trace ''
    set -l result 127
    if test "$had_prior" = 1
        if functions --query $current_name; or not functions --query $prior_name
            printf '%s\n' 'Executable suggestions could not reserve its handler copy. Keep the existing handler and use CLI help.' >&2
            return 127
        end
        set -l had_current 0
        if functions --query fish_command_not_found
            # Later handlers can retain this wrapper. Do not erase their events.
            set -l current_details (functions --details --verbose fish_command_not_found)
            if test "$current_details[4]" = no-scope-shadowing
                printf '%s\n' 'Executable suggestions cannot retain a handler that reads caller variables. Keep the existing handler and use CLI help.' >&2
                return 127
            end
            if functions --handlers | string match --quiet --regex -- '[[:space:]]fish_command_not_found[[:space:]]*$'
                printf '%s\n' 'Executable suggestions cannot retain this custom event handler. Keep the existing handler and use CLI help.' >&2
                return 127
            end
            if not functions --copy fish_command_not_found $current_name
                printf '%s\n' 'Executable suggestions could not preserve the current handler. Keep the existing handler and use CLI help.' >&2
                return 127
            end
            set had_current 1
            functions --erase fish_command_not_found
        end
        # Copy back to the original name before calling the prior handler.
        if not functions --copy $prior_name fish_command_not_found
            if test "$had_current" = 1
                functions --copy $current_name fish_command_not_found
                functions --erase $current_name
            end
            printf '%s\n' 'Executable suggestions could not restore the prior handler. Reload your shell configuration.' >&2
            return 127
        end
        set -l restored_definition (functions fish_command_not_found | string collect)
        # Restore the caller's tracing only for the original handler's execution.
        set --erase --local fish_trace
        fish_command_not_found $argv
        set result $status
        set -l fish_trace ''
        set -l after_definition (functions fish_command_not_found 2>/dev/null | string collect)
        if test "$after_definition" = "$restored_definition"
            functions --erase fish_command_not_found
            if test "$had_current" = 1
                if not functions --copy $current_name fish_command_not_found
                    functions --erase $current_name
                    printf '%s\n' 'Executable suggestions could not restore the current handler. Reload your shell configuration.' >&2
                    return $result
                end
            end
        end
        if test "$had_current" = 1
            functions --erase $current_name
        end
        # If the prior handler replaced itself, its replacement takes precedence.
    else
        set --erase --local fish_trace
        __fish_default_command_not_found_handler $argv
        set result $status
        set -l fish_trace ''
    end
    # Fish keeps failed command lookup unsuccessful even when its callback returns 0.
    if test "$$active_name" = 1; and set -q argv[1]; and test "$argv[1]" != '__APPNAME__'; and string match --quiet --regex -- '\A[A-Za-z0-9_-]{1,65}\z' "$argv[1]"; and string match --quiet --regex -- '__SUGGESTION_REGEX__' "$argv[1]"; and not functions --query '__APPNAME__'; and not builtin --query '__APPNAME__'; and command --query -- '__APPNAME__'
        printf '%s\n' 'Command not found. Did you mean: __APPNAME__?' 'Keep the remaining arguments unchanged.' >&2
    end
    return $result
end
set -l owned (functions fish_command_not_found | string collect)

function openai_suggestions_disable --inherit-variable active_name --inherit-variable had_prior --inherit-variable prior_name --inherit-variable owned
    set -l fish_trace ''
    if test "$$active_name" != 1
        return 0
    end
    set -g $active_name 0
    set -l current_owned (functions fish_command_not_found 2>/dev/null | string collect)
    if functions --query fish_command_not_found; and test "$current_owned" = "$owned"
        if test "$had_prior" = 1
            if not functions --query $prior_name
                printf '%s\n' 'Executable suggestions could not restore the prior handler. Reload your shell configuration.' >&2
                return 1
            end
            functions --erase fish_command_not_found
            if not functions --copy $prior_name fish_command_not_found
                printf '%s\n' 'Executable suggestions could not restore the prior handler. Reload your shell configuration.' >&2
                return 1
            end
        else
            functions --erase fish_command_not_found
        end
        set --erase --global $active_name
    end
    # Another plugin may retain a wrapper copy even when the public hook is ours.
    # Keep the native prior copy until shell exit so disabled wrappers still work.
    set --erase --global __openai_suggestions_app
end
set -g __openai_suggestions_disable_owned (functions openai_suggestions_disable | string collect)
