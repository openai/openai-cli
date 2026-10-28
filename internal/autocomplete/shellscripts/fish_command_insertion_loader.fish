# conf.d loads before config.fish. Wait for user functions and bindings to finish.
if status is-interactive; and not set -q __openai_suggestions_loader_loaded; and not functions -q __openai_suggestions_load_current
    function __openai_suggestions_load_current --on-event fish_prompt
        functions --erase __openai_suggestions_load_current
        isatty stdin; and isatty stdout; and isatty stderr; and test "$TERM" != dumb; or return 0
        set -l __openai_startup_executable __EXECUTABLE__
        test -f "$__openai_startup_executable"; and test -x "$__openai_startup_executable"; or return 0
        functions -q openai; and return 0
        set -l __openai_startup_resolved (command -s openai)
        test -n "$__openai_startup_resolved"; and test "$__openai_startup_executable" -ef "$__openai_startup_resolved"; or return 0
        set -l __openai_startup_generated (command "$__openai_startup_executable" @completion fish --suggestions --suggestions-only__DOUBLE_TAB_ARGUMENT__ </dev/null 2>/dev/null | string collect)
        set -l __openai_startup_code $pipestatus[1]
        if test "$__openai_startup_code" = 0; and string match -rq '(?:^|\n)# openai-cli-suggestions-complete v1\z' -- "$__openai_startup_generated"
            set -g __openai_suggestions_loader_loaded 1
            # Evaluate only successful output from the pinned executable, never command records.
            eval "$__openai_startup_generated"
        end
        return 0
    end
end
