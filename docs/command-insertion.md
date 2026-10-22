# Insert suggested commands

The optional shell integration collects command snippets from CLI help, setup guidance, readable errors, and next steps.
The shortcut fills the shell's command line. It never submits the command.
Edit the inserted command, then press Enter when ready.
Replace placeholders such as `FILE`, `COMMAND`, and `SHELL` before submitting.
An Enter key already queued in the terminal follows normal shell behavior and can submit the inserted command.

## Set up future terminals

Install keyboard insertion once. The loader refreshes integration from your installed CLI when a new interactive shell starts.
New commands and suggestions from an updated CLI work without reinstalling the loader.
An already-open shell receives new suggestions on its next `openai` invocation.
Open a new terminal to load changes to the integration code itself.

Run the command for your current shell:

```sh
# zsh: the shell expands its actual startup directory.
openai @completion zsh --install-suggestions --profile "${ZDOTDIR-$HOME}/.zshrc"

# Bash 4.3 or later.
openai @completion bash --install-suggestions
```

```fish
# fish
openai @completion fish --install-suggestions
```

Without a shell argument, setup detects the immediate parent shell.
Zsh requires an explicit profile when the CLI cannot see an exported, absolute `ZDOTDIR`.
Setup preserves personal startup content, aliases, functions, custom bindings, and separate image-picker settings.
It installs the shortcut only when the binding is free.
The default shortcut is **Ctrl+X, then Tab**. Release Ctrl before pressing Tab.
Existing Tab completion remains available.
Command insertion and ordinary Tab completion use separate setup.

Add `--suggestions-double-tab` during installation to request double Tab as well.
Double Tab can delay ordinary Tab completion.
Persistent setup installs keyboard insertion; experimental mouse configuration remains separate.

Inspect configuration without changing files:

```sh
openai @completion zsh --suggestions-status --profile "${ZDOTDIR-$HOME}/.zshrc"
```

Remove only the managed insertion setup:

```sh
openai @completion zsh --uninstall-suggestions --profile "${ZDOTDIR-$HOME}/.zshrc"
```

For Bash and fish, use their shell name and omit `--profile` unless you installed into a custom file.
Setup and status support `--format json` and `--format jsonl` for scripts.
Status reports saved configuration, not the bindings inside an already-running shell.
After removal, open a new terminal or run `openai_command_insertion_disable` in the current session.

The loader preserves the stable launcher path that selected this CLI during installation.
Normal binary replacements and package-manager symlink updates refresh the integration automatically.
If you move the CLI to another installation, rerun setup through that `openai` command.
Missing or mismatched executables leave the startup integration inactive.
Failed or incomplete generation never becomes shell code.
Temporary `go run` builds cannot install persistent integration.

## Enable only this shell session

Zsh:

```sh
source <(openai @completion zsh --suggestions)
```

Bash 4.3 or later:

```sh
source <(openai @completion bash --suggestions)
```

Fish:

```fish
openai @completion fish --suggestions | source
```

These commands print and load shell integration. They do not edit shell profiles.
The integration preserves existing aliases and functions. It does not replace an existing `openai` alias or function.

Press **Ctrl+X, then Tab** to search commands from the last foreground `openai` invocation.
Type to filter. Arrow keys choose a command. Enter fills the prompt. Escape cancels and preserves your draft.
Press Enter separately at the shell prompt to run the command.
The next submitted command clears those suggestions.
A direct foreground `openai` wrapper invocation can replace them with new suggestions in the parent shell.
The integration installs its shortcut only when the key binding is free.

Add `--suggestions-double-tab` to enable the optional double-Tab shortcut:

```sh
source <(openai @completion zsh --suggestions --suggestions-double-tab)
```

Double Tab can delay ordinary single-Tab completion while the shell waits for the second key.

Use `openai_suggestion_next` and `openai_suggestion_restore` for custom shell bindings.
Disable this session's integration with:

```sh
openai_command_insertion_disable
```

## Search all commands

Run `openai explore` to search the CLI command tree and declared examples.
The catalog updates with the installed CLI. Search uses no API calls or model requests.
Use words such as `files upload`, `models`, or `response` to filter commands and descriptions.
Search ranks command names before arguments and descriptions. It matches whole words and word prefixes, in any order.
Every search word must match. One-letter and two-letter description searches require whole words, such as `ID`.
Unrelated word fragments and documentation URLs do not produce description matches.
Discovery omits retired operations. Syntax guides such as `COMMAND --help` are not selectable suggestions.
Examples remain editable. Replace sample paths and provide required inputs before running a selected command.
Arrow keys choose a command. Enter selects it. Escape cancels.

With zsh or fish command insertion enabled, selection fills the next editable prompt.
Bash stages the selection; Ctrl+X, then Tab inserts it without opening another picker.
Without command insertion, the picker prints the selected command and setup guidance.
The shell integration only intercepts bare `openai explore` in an interactive terminal.
Explicit flags or redirection use the CLI's normal output behavior.

The picker requires a terminal of at least 40 columns and 12 rows.
Redirecting standard error can disable the picker.
It supports `--format auto` and `--format text` only.
Structured formats, transformations, and raw output are unavailable for command search.
Command search also requires auto or text error output without error transformations.
The existing `--format explore` JSON viewer remains separate.

## Experimental click insertion in Kitty with zsh

The optional Kitty adapter fills the active zsh command line when you open a suggestion link.
It never submits the command. Kitty shows its normal link hover and underline effects.
This adapter remains experimental until actual Kitty loading and mouse dispatch receive native validation.
Native zsh editor tests and adapter IPC tests pass; they do not establish native mouse behavior.
Use Kitty's configured link gesture, normally **Ctrl+Shift+click**.
Other terminals keep the keyboard shortcut.

1. Save the adapter to a permanent location you control:

   ```sh
   openai @completion --suggestions-adapter kitty > openai-suggestions.py
   ```

2. Add this rule to Kitty's `open-actions.conf`, using the adapter's absolute path:

   ```conf
   protocol openai-insert
   action kitten /absolute/path/openai-suggestions.py ${URL}
   ```

3. Enable click insertion in a local Kitty zsh session:

   ```sh
   source <(openai @completion zsh --suggestions --suggestions-click)
   ```

4. Display a feature's suggestions:

   ```sh
   openai responses create --help
   ```

5. Open a suggested command with Kitty's link gesture.

The command appears in the editable prompt. Press Enter separately to run it.
The keyboard shortcut searches current commands. Escape preserves your original draft.
You can also add `--suggestions-double-tab` to the setup command.
The CLI prints the adapter and shell integration. It does not change terminal settings or shell profiles.

Examples across features include:

| Help or guidance | Clickable suggestion |
| --- | --- |
| Responses help | `openai responses create --model gpt-5.5 --input "Say hello"` |
| Files help | `openai files create --file ./example.txt --purpose assistants` |
| Models help | `openai models list` |
| Authentication recovery | `openai help setup` |
| Image model discovery | The displayed generation command with its selected model |
| Image picker print action | The command you explicitly chose to print |

Only suggestions from the current invocation remain active.
The adapter rejects old scrollback links, busy shells, other windows, and changed directories.
It also rejects continuation prompts, `vared`, recursive editors, and clicks with queued keyboard input.
SSH sessions and terminal multiplexers keep keyboard insertion only.
Unsupported or expired clicks leave the command line unchanged.

The link carries session, generation, and command identifiers. It contains no command text or arguments.
Kitty sends those identifiers through a private, bounded local channel.
The active zsh editor validates each request before changing its own command line.
The adapter never writes text or control keys into the terminal's input stream.
It does not require Kitty remote control.
See Kitty's [link actions](https://sw.kovidgoyal.net/kitty/open_actions/) and [custom kittens](https://sw.kovidgoyal.net/kitty/kittens/custom/) documentation.

## Support limits

- Experimental click insertion targets local Kitty with zsh and the configured adapter.
- Bash and fish support keyboard insertion. Other terminal adapters require separate implementation and native validation.
- PowerShell does not support this integration's inherited file-descriptor transport.
- The native Windows CLI cannot collect suggestions through that transport. Use the Linux CLI inside WSL.
- Persistent installation refuses native Windows builds before changing startup files.
- Only terminal output supplies snippets. Readable errors on terminal stderr still qualify when stdout is redirected.
- The integration collects explicit CLI suggestions. It does not inspect API responses or model text for commands.
- Changing directories clears the previous suggestions.
- Bash requires a writable `PROMPT_COMMAND` to expire suggestions safely.
- The integration bypasses capture while file descriptors 7, 8, or 9 are already open.
- Capture retains at most 128 commands, 16 KiB per command, and 2 MiB total, including separators.
- Capture accepts only single-line commands without control characters.
- Commands beyond these limits are omitted.
- A direct executable path bypasses the `openai` shell wrapper.
- The image picker's explicit print action supplies its reviewed command. Ordinary generation does not retain its prompt as a suggestion.

## Register commands in future features

Register each command where the human-readable presenter displays it.
Keep feature behavior in `internal/commands`, application wiring in `internal/app`, and help presentation in `internal/clihelp`.
Do not change generated commands in `generated/commands`.

For ordinary command output, use `commandsuggest.Record`:

```go
fmt.Fprintln(command.Root().Writer,
    commandsuggest.Record(command, "openai models list"))
```

Use `commandsuggest.RecordTo` when the presenter selects an explicit writer, especially stderr:

```go
suggestion := commandsuggest.RecordTo(command, output, "openai help setup")
fmt.Fprintln(output, "Key setup:", suggestion)
```

Use `commandsuggest.RecordFor(command, shell, text)` for commands specific to one shell.
The recorder filters shell annotations before deduplication.
Registration returns plain text by default, or an insertion link in an enabled terminal session.
Always use the returned string when displaying a suggestion.
When integration is inactive, registration does nothing.

Help templates can register commands through the `suggest` and `suggestFor` functions:

```gotemplate
{{suggest . (printf "%s models list" $bin)}}
{{suggestFor . "export OPENAI_API_KEY" "bash" "zsh"}}
```

These functions run when help renders. Do not register suggestions while eagerly configuring the command tree.
Command help derives navigation targets from the current command tree when it renders.
Every displayed, shell-safe command row gets its full `--help` target without a separate per-command registration table.
New visible commands inherit this behavior automatically. Hidden commands remain hidden.
Command names containing whitespace, shell metacharacters, or a leading dash keep plain labels without derived insertion targets.

Declare examples once through `clihelp.Content.Examples` in the command's `help-content` metadata.
The complete-help renderer displays and registers each example together.
It supplies the current executable invocation, including paths that require quoting.
New features using this shared content type need no separate shell code or suggestion list.

```go
command.Metadata["help-content"] = clihelp.Content{
    Examples: []clihelp.Example{{
        Description: "Inspect a widget:",
        Command: "widgets inspect --widget example",
    }},
}
```

Handwritten output must still declare its suggestions through the shared presenter.
Arbitrary prose does not become executable just because it contains a command name.
`commandsuggest.RenderText` decorates explicitly declared spans in typed recovery text.
It does not discover commands in arbitrary output.

Readable presenters use `clihelp.NewSuggestedText` to escape external text before adding trusted links.
Do not pass decorated strings through a terminal-control sanitizer or permit arbitrary escape sequences through it.
Use `commandsuggest.WriteLine` when printing one explicitly requested command, such as the image picker's print action.
A failed write invalidates that capture, so a partially displayed link cannot select a later command.

Readable errors register through `ShowCommandError` with its actual output writer.
Typed image recovery errors declare suggestions through their human-readable presenter.
Error constructors and JSON/YAML formatting must not register commands.

Do not parse output to discover suggestions.
Do not copy server error messages, API responses, model text, credentials, or hidden request arguments into suggestions.
Preserve the displayed executable invocation, shell quoting, placeholders, output format, and exit status.

Use `commandsuggest.SetRecorder` with a test recorder to verify producer behavior.
Test rendered help, structured errors, arbitrary external text, and the selected output writer.
Use native shell tests to verify insertion, cancellation, completion, and the absence of automatic execution.

## Upgrade contract

The shell keeps no catalog of CLI commands.
Completion queries the running CLI, and each invocation supplies its current authored suggestions.
Preserve the single-line capture protocol when adding features.
Generated integration ends with `# openai-cli-suggestions-complete v1` only after the complete script.
Managed loaders require that marker and a successful generator exit before evaluating trusted integration code.
They never evaluate captured command suggestions.

Upgrade regression tests install once, replace the CLI, and check both existing shells and new shell startup.
Keep those tests when extending command registration or changing the completion protocol.
