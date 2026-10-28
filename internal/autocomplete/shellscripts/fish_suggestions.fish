# Optional executable suggestions. Source after your other shell configuration.
# Inspect only the missing executable. Never replay commands or inspect arguments.
# Hide newly captured definitions from tracing. The sourced scope restores tracing.
set -l fish_trace ''
if not status is-interactive
    return 0
end
set -l __openai_suggestions_version (string split . -- $version)
if test "$__openai_suggestions_version[1]" -lt 4
    printf '%s\n' 'Executable suggestions require fish 4.0 or newer.' >&2
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
# The explicitly enabled insertion wrapper is safe only while its definition
# matches the one recorded at installation. Never invoke it to check ownership.
if begin; functions --query '__APPNAME__'; and not begin
        set -q ____APPNAME___suggestions_enabled; and test "$____APPNAME___suggestions_enabled" = 1; and set -q ____APPNAME___suggestions_wrapper_body; and test (functions '__APPNAME__' | string collect) = "$____APPNAME___suggestions_wrapper_body"
    end; end; or builtin --query '__APPNAME__'; or not command --query -- '__APPNAME__'
    printf '%s\n' 'Executable suggestions require __APPNAME__ on PATH. Install the CLI before enabling suggestions.' >&2
    return 1
end

# Native copies preserve execution source and inherited values. Event bindings
# and caller-variable lookup require a different lifecycle; leave them unchanged.
set -l serial 0
if set -q __openai_suggestions_serial
    set serial $__openai_suggestions_serial
else if set -q __openai_suggestions_pending_restores
    printf '%s\n' 'Executable suggestions conflict with existing shell state. Keep the existing handler and use CLI help.' >&2
    return 1
end
set serial (math $serial + 1)
set -l active_name __openai_suggestions_active_$serial
set -l prior_name __openai_suggestions_prior_$serial
set -l current_name __openai_suggestions_current_$serial
set -l pending_name __openai_suggestions_pending_$serial
set -l identity_name __openai_suggestions_identity_$serial
set -l restore_name __openai_suggestions_restore_$serial
set -l event_name __openai_suggestions_events_$serial
set -l inflight_name __openai_suggestions_inflight_$fish_pid\_$serial
if set -q $active_name; or set -q $pending_name; or set -q $inflight_name
    printf '%s\n' 'Executable suggestions conflict with existing shell state. Keep the existing handler and use CLI help.' >&2
    return 1
end
if functions --query $prior_name; or functions --query $current_name; or functions --query $identity_name; or functions --query $restore_name; or functions --query $event_name
    printf '%s\n' 'Executable suggestions conflict with existing shell functions. Keep the existing handler and use CLI help.' >&2
    return 1
end
set -l had_prior 0
# This query loads the normal platform handler before inspecting its metadata.
if functions --query fish_command_not_found
    set -l prior_details (functions --details --verbose fish_command_not_found)
    if test (count $prior_details) != 5; or not string match --quiet --regex -- '\A[0-9]+\z' "$prior_details[3]"; or not contains -- "$prior_details[4]" scope-shadowing no-scope-shadowing
        printf '%s\n' 'Executable suggestions cannot retain this handler metadata. Keep the existing handler and use CLI help.' >&2
        return 1
    end
    if functions fish_command_not_found | string match --quiet --regex -- '^function fish_command_not_found --wraps='
        printf '%s\n' 'Executable suggestions cannot retain a handler with completion wrappers. Keep the existing handler and use CLI help.' >&2
        return 1
    end
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
# Copies retain execution source but add copy-location metadata. Compare the
# complete body and original source properties, preserving comments and captures.
function $identity_name
    set -l fish_trace ''
    functions --query $argv[1]; or return 1
    set -l details (functions --details --verbose $argv[1])
    test (count $details) = 5; or return 1
    string match --quiet --regex -- '\A[0-9]+\z' "$details[3]"; or return 1
    contains -- "$details[4]" scope-shadowing no-scope-shadowing; or return 1
    set -l origin "$details[1]"
    if not contains -- "$details[2]" autoloaded not-autoloaded n/a
        set origin "$details[2]"
    end
    set -l definition (functions --no-details $argv[1] | string collect)
    set definition (string replace --regex -- '\Afunction [^ \n]+' 'function fish_command_not_found' "$definition")
    string escape --style=var -- "$origin" "$details[3]" "$details[4]" "$details[5]" "$definition"
end
if test "$had_prior" = 1
    set -l prior_identity ($identity_name fish_command_not_found | string collect)
    set -l copied_identity ($identity_name $prior_name | string collect)
    if test -z "$prior_identity"; or test "$prior_identity" != "$copied_identity"
        functions --erase $prior_name $identity_name
        printf '%s\n' 'Executable suggestions could not preserve the prior handler metadata. Keep the existing handler and use CLI help.' >&2
        return 1
    end
end
set -g __openai_suggestions_serial $serial
set -g __openai_suggestions_app '__APPNAME__'
set -g $active_name 1

# The pending record precedes public mutation, so interrupted copies use the
# same restoration path as ordinary returns. Changed or removed handlers win.
function $restore_name --inherit-variable pending_name --inherit-variable current_name --inherit-variable identity_name --inherit-variable restore_name
    set -l fish_trace ''
    if set -q __openai_suggestions_pending_restores[-1]; and test "$__openai_suggestions_pending_restores[-1]" != "$restore_name"
        return 1
    end
    # A signal can arrive between clearing the record and removing its stack entry.
    if not set -q $pending_name
        if set -q __openai_suggestions_pending_restores[-1]
            set --erase --global __openai_suggestions_pending_restores[-1]
        end
        return 0
    end
    set -l pending $$pending_name
    set -l live ($identity_name fish_command_not_found | string collect)
    set -l backup ($identity_name $current_name | string collect)
    set -l restore 0
    if contains -- "$pending[1]" installing restoring
        if not functions --query fish_command_not_found; or test "$live" = "$pending[4]"
            set restore 1
        end
    else if test "$pending[1]" = calling; and test "$live" = "$pending[4]"
        set restore 1
    end
    if test $restore = 1
        if test "$pending[2]" = 1; and test "$backup" != "$pending[3]"
            printf '%s\n' 'Executable suggestions could not restore the current handler. Reload your shell configuration.' >&2
            return 1
        end
        set -g $pending_name restoring "$pending[2]" "$pending[3]" "$pending[4]" "$pending[5]"
        functions --erase fish_command_not_found
        if test "$pending[2]" = 1
            if not functions --copy $current_name fish_command_not_found
                printf '%s\n' 'Executable suggestions could not restore the current handler. Reload your shell configuration.' >&2
                return 1
            end
        end
    end
    if test "$pending[2]" = 1; and test "$backup" = "$pending[3]"
        functions --erase $current_name
    end
    set --erase --global $pending_name
    if set -q __openai_suggestions_pending_restores[-1]
        set --erase --global __openai_suggestions_pending_restores[-1]
    end
end
set -l restore_definition (functions $restore_name | string collect)

function $event_name --on-event fish_postexec --on-event fish_prompt --inherit-variable pending_name --inherit-variable restore_name --inherit-variable restore_definition --inherit-variable inflight_name
    set -l fish_trace ''
    if not set -q __openai_suggestions_pending_restores[1]; and not set -q $pending_name
        return 0
    end
    # Exported function-local variables survive ordinary nested function scopes.
    # Cancellation removes them when those scopes unwind. Never read event arguments.
    set --query --local $inflight_name; and return 0
    for pending_restore in $__openai_suggestions_pending_restores
        string match --quiet --regex -- '\A__openai_suggestions_restore_[0-9]+\z' "$pending_restore"; or return 1
        set -l pending_serial (string replace __openai_suggestions_restore_ '' -- "$pending_restore")
        set -l pending_inflight __openai_suggestions_inflight_$fish_pid\_$pending_serial
        set --query --local $pending_inflight; and return 0
    end
    # Retained wrappers can nest across activations. Unwind active transactions
    # in reverse order, regardless of Fish's event-handler dispatch order.
    while set -q __openai_suggestions_pending_restores[-1]
        set -l next $__openai_suggestions_pending_restores[-1]
        string match --quiet --regex -- '\A__openai_suggestions_restore_[0-9]+\z' "$next"; or return 1
        set -l next_serial (string replace __openai_suggestions_restore_ '' -- "$next")
        set -l next_pending __openai_suggestions_pending_$next_serial
        if not set -q $next_pending
            set --erase --global __openai_suggestions_pending_restores[-1]
            continue
        end
        set -l next_record $$next_pending
        functions --query $next; or return 1
        set -l next_definition (functions $next | string collect)
        test -n "$next_record[5]"; and test "$next_definition" = "$next_record[5]"; or return 1
        $next; or return 1
    end
    if set -q $pending_name
        set -l own_definition (functions $restore_name | string collect)
        test "$own_definition" = "$restore_definition"; or return 1
        $restore_name
    end
end

function fish_command_not_found --inherit-variable active_name --inherit-variable had_prior --inherit-variable prior_name --inherit-variable current_name --inherit-variable pending_name --inherit-variable identity_name --inherit-variable restore_name --inherit-variable restore_definition --inherit-variable inflight_name
    set -l fish_trace ''
    set --function --export $inflight_name 1
    set -l result 127
    if test "$had_prior" = 1
        if set -q $pending_name; or functions --query $current_name; or not functions --query $prior_name
            printf '%s\n' 'Executable suggestions could not reserve its handler copy. Keep the existing handler and use CLI help.' >&2
            return 127
        end
        set -l had_current 0
        if functions --query fish_command_not_found
            # Later handlers can retain this wrapper. Do not erase their events.
            set -l current_details (functions --details --verbose fish_command_not_found)
            if test (count $current_details) != 5; or not string match --quiet --regex -- '\A[0-9]+\z' "$current_details[3]"; or not contains -- "$current_details[4]" scope-shadowing no-scope-shadowing
                printf '%s\n' 'Executable suggestions cannot retain this handler metadata. Keep the existing handler and use CLI help.' >&2
                return 127
            end
            if functions fish_command_not_found | string match --quiet --regex -- '^function fish_command_not_found --wraps='
                printf '%s\n' 'Executable suggestions cannot retain a handler with completion wrappers. Keep the existing handler and use CLI help.' >&2
                return 127
            end
            if test "$current_details[4]" = no-scope-shadowing
                printf '%s\n' 'Executable suggestions cannot retain a handler that reads caller variables. Keep the existing handler and use CLI help.' >&2
                return 127
            end
            if functions --handlers | string match --quiet --regex -- '[[:space:]]fish_command_not_found[[:space:]]*$'
                printf '%s\n' 'Executable suggestions cannot retain this custom event handler. Keep the existing handler and use CLI help.' >&2
                return 127
            end
            set had_current 1
        end
        set -l current_identity ($identity_name fish_command_not_found | string collect)
        set -l prior_identity ($identity_name $prior_name | string collect)
        if test -z "$prior_identity"; or begin; test "$had_current" = 1; and test -z "$current_identity"; end
            printf '%s\n' 'Executable suggestions could not preserve the handler metadata. Keep the existing handler and use CLI help.' >&2
            return 127
        end
        set -g $pending_name preparing "$had_current" "$current_identity" "$prior_identity" "$restore_definition"
        set --global --append __openai_suggestions_pending_restores $restore_name
        if test "$had_current" = 1
            if not functions --copy fish_command_not_found $current_name
                $restore_name
                printf '%s\n' 'Executable suggestions could not preserve the current handler. Keep the existing handler and use CLI help.' >&2
                return 127
            end
            set -l copied_identity ($identity_name $current_name | string collect)
            if test "$copied_identity" != "$current_identity"
                functions --erase $current_name
                $restore_name
                printf '%s\n' 'Executable suggestions could not preserve the current handler metadata. Keep the existing handler and use CLI help.' >&2
                return 127
            end
        end
        set -g $pending_name installing "$had_current" "$current_identity" "$prior_identity" "$restore_definition"
        functions --erase fish_command_not_found
        # Copy back to the original name before calling the prior handler.
        if not functions --copy $prior_name fish_command_not_found
            $restore_name
            printf '%s\n' 'Executable suggestions could not restore the prior handler. Reload your shell configuration.' >&2
            return 127
        end
        set -g $pending_name calling "$had_current" "$current_identity" "$prior_identity" "$restore_definition"
        # Restore the caller's tracing only for the original handler's execution.
        set --erase --local fish_trace
        fish_command_not_found $argv
        set result $status
        set -l fish_trace ''
        $restore_name
        # If the prior handler replaced itself, its replacement takes precedence.
    else
        set --erase --local fish_trace
        __fish_default_command_not_found_handler $argv
        set result $status
        set -l fish_trace ''
    end
    # Fish keeps failed command lookup unsuccessful even when its callback returns 0.
    if test "$$active_name" = 1; and set -q argv[1]; and test "$argv[1]" != '__APPNAME__'; and string match --quiet --regex -- '\A[A-Za-z0-9_-]{1,65}\z' "$argv[1]"; and string match --quiet --regex -- '__SUGGESTION_REGEX__' "$argv[1]"; and begin; not functions --query '__APPNAME__'; or begin
        set -q ____APPNAME___suggestions_enabled; and test "$____APPNAME___suggestions_enabled" = 1; and set -q ____APPNAME___suggestions_wrapper_body; and test (functions '__APPNAME__' | string collect) = "$____APPNAME___suggestions_wrapper_body"
    end; end; and not builtin --query '__APPNAME__'; and command --query -- '__APPNAME__'
        printf '%s\n' 'Command not found. Did you mean: __APPNAME__?' 'Keep the remaining arguments unchanged.' >&2
    end
    return $result
end
set -l owned ($identity_name fish_command_not_found | string collect)

function openai_suggestions_disable --inherit-variable active_name --inherit-variable had_prior --inherit-variable prior_name --inherit-variable owned --inherit-variable identity_name --inherit-variable restore_name
    set -l fish_trace ''
    if test "$$active_name" != 1
        return 0
    end
    $restore_name; or return 1
    set -g $active_name 0
    set -l current_owned ($identity_name fish_command_not_found | string collect)
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
    # Keep the prior copy and cleanup helpers so retained wrappers still work.
    set --erase --global __openai_suggestions_app
end
set -g __openai_suggestions_disable_owned (functions openai_suggestions_disable | string collect)
