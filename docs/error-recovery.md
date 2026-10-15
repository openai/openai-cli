# Recovering from command errors

Command errors go to stderr and keep their failing exit status.
The CLI suggests help for the corrected command but never runs it.
The shell must first find the `openai` executable.
For example, `opneai images generate` fails inside the shell before the CLI starts.
Correct the executable name to `openai`; keep the remaining arguments unchanged.
Optional shell integration is described below.

```text
$ openai modles list
Unknown command. Did you mean: openai help models list?
To run it, correct only the command spelling. Keep your original options and arguments.

$ openai help modles list
Unknown help topic. Did you mean: openai help models list?

$ openai models list --file missing.txt
The --file option is not available for this command.
Options and examples: openai models list --help

$ openai files list --limit banana
Invalid value for --limit. Expected an integer, such as 1.
Options and examples: openai files list --help

$ openai files upload missing.txt --purpose user_data
Could not open the file for --file. Check the path and permissions.

$ openai uploads parts create --upload-id upload_demo --data missing.bin
Could not open the file for --data. Check the path and permissions.
```

A help suggestion can retain declared subcommands after the corrected command.
To run the operation, correct only its spelling in your original command.
Keep the original options and arguments, including project and endpoint overrides.
Suggestions omit these private values.
They stop before any remaining argument that is not a declared subcommand.
It never repeats option values, private paths, credentials, or request contents.

If an option belongs to another public command, the error identifies that option.
It links to help for the current command without guessing a different command.
Existing suggestions for similar options in the current command remain available.

Numeric type errors include an example value showing the expected syntax.
The option's help describes its purpose and constraints.
Piped JSON and YAML errors identify the declared option that rejected a value.
They use the same scoped help and type examples as command-line errors.
An explicit option still overrides its piped value.

File errors identify the declared option that supplied the failed file input.
This includes file references in request options and piped JSON or YAML.
Unknown fields keep general file guidance.
Read and cleanup failures retain their underlying causes.

Existing flag guidance still identifies missing values, expected types, and required options.
Use the suggested help command to inspect options in the correct scope.
Nested options inherit the same recovery through their declared parent options.

New registered commands and flags enter the shared recovery path automatically.
The declaration audit checks the production command tree, including compatibility routes.
Its coverage guard rejects unclassified declarations during testing, without rejecting new API fields at runtime.
Operation-specific failures still need caller-supplied context when the shared handler cannot determine a safe repair.

Local utility errors preserve their specific repair instructions.
Manpage errors identify `--output` and report potentially incomplete files.
Completion setup errors identify conflicting or missing setup actions.
Preference errors identify the settings file through a symbolic configuration location.
They do not repeat the resolved private path.

Explicit error formats remain available:

```sh
openai --format-error json modles list
openai --format-error jsonl files upload missing.txt --purpose user_data
openai --format-error json --transform-error message modles list
```

The first command emits a JSON object with a `message` field.
The final command extracts that field.
These local errors do not invent an HTTP status or request ID.


## Executable typo suggestions

Optional shell integration can identify nearby spellings of `openai`, including `opneai`.
It prints advice without executing the corrected command.
The setup command prints a standalone hook. It does not require or enable ordinary completion.
Shell advice uses stderr independently of CLI output options because the CLI has not started.
It inspects only the executable name and never reads stdin or shell history.
Only previous handlers receive the original arguments; the integration sends no arguments to CLI helpers.
Existing CLI recovery handles subcommands and flags after `openai` starts, including future declarations.

Bash, zsh, and fish activate only in interactive sessions.
Bash and zsh require unused command-not-found hooks.
If another plugin owns the hook, sourcing keeps it unchanged and returns status 2.
The CLI can still generate the script successfully; the shell reports this conflict during sourcing.
Keep that handler and type `openai` correctly to use CLI recovery.
Run the command for your shell to enable suggestions in the current session:

```sh
# zsh 5.8 or later
source <(openai @completion zsh --suggest-executable)

# Bash 4 or later
source <(openai @completion bash --suggest-executable)

# fish 4.0 or later
openai @completion fish --suggest-executable | source
```

PowerShell activates wherever you explicitly source the script, including noninteractive sessions.
For PowerShell 7 or later:

```powershell
openai '@completion' pwsh --suggest-executable | Out-String | Invoke-Expression
```

To enable future sessions, add your shell's command to the startup file you already use.
A PowerShell profile can also load during script execution; add the line only where you want this behavior.
To disable future sessions, remove that line.
To disable the current session, run:

```sh
openai_suggestions_disable
```

Disable restores the previous hook state when this integration still owns the hook.
It preserves a later replacement by another plugin.
Enabling picker shortcuts does not enable executable suggestions.
Do not combine `--suggest-executable` with picker options.

The integration preserves existing handler precedence.
Bash and zsh leave existing handlers installed without adding advice.
A handler that exits or triggers shell error termination can prevent later advice.
Fish retains its native missing-command result regardless of its handler's return status.
Fish preserves ordinary handlers through native function copies.
Fish 4.0 or later preserves the original handler's source location during these copies.
Earlier Fish versions cannot enable this opt-in.
Handlers with event registrations, completion wrappers, or `--no-scope-shadowing` remain unchanged; sourcing explains the conflict.
Fish also refuses ambiguous source metadata, including source paths with newline characters.
PowerShell advice appears only when its previous callback leaves lookup unresolved.

Without a previous handler, Bash and zsh use a fixed fallback message on stderr and return 127.
Their locale and script-location prefixes can differ from the shell's original diagnostic.

Suggestions require the declared `openai` executable on PATH.
A shadowing alias or function prevents advice.
Shell hooks with unsupported ownership or event metadata remain unchanged; setup explains the conflict.
They cover one insertion, deletion, substitution, or adjacent transposition in a simple ASCII executable name.
They do not correct filesystem paths, renamed binaries, distant names, shell syntax, redirections, or interpreter failures.
They cannot intercept failures inside `sudo`, `env`, another process, or a remote shell.
Bash 3.2, Fish 3.x, POSIX sh, cmd.exe, and Windows PowerShell 5.1 are unsupported by this opt-in.
Ordinary completion remains available on its existing supported shells.
