# Save image picker shortcuts

The CLI quietly configures future Bash, zsh, and fish sessions on the first
eligible interactive `openai` run. Open a new terminal to activate the shortcut;
the current session keeps its existing bindings. Bash needs version 4.3 or later
for the Tab shortcut. Pressing Enter opens the picker without shell integration,
including in PowerShell and Bash 3.2.

For zsh, automatic setup requires an exported, absolute `ZDOTDIR`. A child
process cannot distinguish an unset variable from an unexported zsh parameter,
so ambiguous default locations are skipped. Use the explicit commands below
when `ZDOTDIR` is not exported.

First-use setup requires the running executable to be the `openai` on PATH and
all three standard streams to be terminals. It skips CI, root/sudo, unsupported
shells, `TERM=dumb`, completion and manpage requests. It respects an earlier
opt-out and refreshes intact saved scripts when their generated content changes,
including in shells with active integration. Open a new terminal to load updated
scripts. Setup failures leave the ordinary command running. The command waits
at most 250 milliseconds for optional setup, including slow filesystem calls.
An I/O operation already in progress, including an atomic profile replacement,
may finish in the background. Later changes check cancellation; the worker
cleans up its uncommitted files when I/O returns. If the process exits first,
inert staging files can remain, as with an interrupted explicit installation.
At most one setup worker runs in a CLI process.

To configure or remove persistent shortcuts explicitly:

```sh
openai @completion zsh --install-picker --profile "${ZDOTDIR-$HOME}/.zshrc"
openai @completion zsh --uninstall-picker --profile "${ZDOTDIR-$HOME}/.zshrc"
```

Run those commands in zsh so it expands its own startup location, including an
unexported `ZDOTDIR`. Bash and fish can use `--install-picker` or
`--uninstall-picker` without `--profile`. Open a new terminal afterward.
`--profile PATH` selects a different startup file. Without a shell argument,
explicit setup uses the immediate parent shell. Installer integrations can use
`openai @completion --install-picker --automatic` to select the preferred shell
and preserve an existing opt-out; this mode does not accept shell or profile
overrides.

On Windows, a Unix shell's nonempty `HOME` must match `USERPROFILE` for default
setup. Different or MSYS-style home paths require an explicit `--profile PATH`;
automatic setup skips these cases rather than guessing another startup location.

Default zsh setup uses an absolute exported `ZDOTDIR/.zshrc`; missing, empty or
relative values require an explicit profile. Other defaults use Bash's `.bashrc` and first existing
login startup file, or fish's `conf.d/openai-picker.fish`. Fish follows
`XDG_CONFIG_HOME/fish/conf.d`, falling back to `HOME/.config/fish/conf.d`,
including on Windows. Windows scripts remain in `APPDATA/openai/shell`.
New macOS scripts use `HOME/Library/Application Support/openai/shell`. Setup's
decision lock stays beside the CLI preferences on every platform, so differing
XDG settings cannot bypass the shared opt-out decision.
Existing profiles keep their recorded script location for refresh and removal
after the configuration root changes; new profiles use the current root.
After an interrupted refresh, later setup retries cleanup of unchanged obsolete
scripts in that profile's verified namespace. Modified or unproven files remain.
Scripts use the current
`openai` on PATH. If the executable is removed, the guarded source block becomes
inactive. Existing startup content and custom key bindings are preserved.
Fish installs its wrapper once at the first prompt, after `config.fish` and
`fish_user_key_bindings`. Later custom bindings and `openai_picker_disable`
remain in effect; calling disable from `config.fish` also cancels pending setup.

Removal deletes only intact CLI-owned blocks and verified scripts, and records
an empty per-shell opt-out marker beside `image-picker.json`. Later automatic
setup keeps that shell disabled. Explicit installation clears the opt-out.
When setup created the profile itself, removal restores its absence if the
intact managed block is still its only content. Preexisting empty files and
profiles containing personal changes remain. Older installations without
creation metadata conservatively preserve the profile.
Repeating either command is safe. Modified blocks or scripts stay available for
inspection. Default Bash removal checks all three login profiles as well as
`.bashrc`, so a newer, higher-priority login profile cannot hide earlier setup.
If one profile cannot be safely cleaned, removal continues through the others,
records the opt-out when possible, and reports failure after the cleanup.
An explicit `--profile` still limits removal to that file.
Bash configures its two startup modes separately; if a later write
fails, the error reports partial setup and rerunning the command is safe.

Setup checks the directory ancestry of configuration and startup paths before
creating files. It keeps symlinked or hard-linked profiles, special permission
bits, unsafe permissions, and protected file metadata unchanged. On macOS and
Linux this includes profiles with ACLs or extended attributes. Windows junction
or reparse-point ancestors require manual setup. Equivalent Windows home-directory
spellings are recognized by filesystem identity. Profile aliases use their
canonical path for setup locks and new script names, including trusted symlinked
parent directories on Unix. When an older script records
an unprovable path spelling, refresh or removal preserves that script and any empty
profile; that profile no longer sources the old script after refresh or removal.
Windows replacement also checks alternate streams and file-specific
permissions. Windows managed directories, scripts and staging files require a current-user
or local Administrators owner and a DACL that grants mutation only to that user,
SYSTEM or local Administrators. Lock files also reject access by other readers.
Profiles must be UTF-8 text and at most 1 MiB including the managed
block.

Setup treats configured `HOME`, `XDG_CONFIG_HOME`, `ZDOTDIR`, and explicit profile
locations as trusted. It checks the immediate profile parent and both managed
`openai` and `openai/shell` directories for ownership and unsafe permissions.
On macOS, a profile parent may have deny-only ACLs such as the normal home
directory deny-delete rule; ACLs granting permissions are left for manual setup.
On macOS and Linux, both managed directories must be free of protected metadata,
including ACLs.

Use the [current-session shortcut](image-picker-shortcuts.md) when a profile
cannot be managed automatically. PowerShell retains ordinary completion and
does not use persistent picker setup.
