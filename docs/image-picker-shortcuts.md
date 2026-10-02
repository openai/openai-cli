# Image picker shortcuts

The CLI can [save setup for future shells](image-picker-shell-setup.md), including
quiet setup on eligible first use. The commands below apply only to the current
session.

`openai images generate` opens the image picker when you press Enter in an
interactive terminal. To also open it with Tab, load the optional hook in your
current shell session:

```bash
# Bash 4.3 or newer
source <(openai @completion bash --picker)
```

```zsh
# zsh, after completion has been initialized
autoload -Uz compinit && compinit
source <(openai @completion zsh --picker)
```

```fish
# fish
openai @completion fish --picker | source
```

Type `openai images generate` and press Tab at the end of the line. Ctrl+C
leaves the picker and keeps the command editable. Other command lines retain
ordinary completion. The shortcut requires `openai` on PATH as an executable;
aliases, shell functions, extra arguments, redirections and compound commands
use ordinary completion. Existing custom Bash Tab bindings are preserved.

Run `openai_picker_disable` to turn off the hook in this session. Closing the
shell also removes it. These commands do not edit startup files. Ordinary
completion scripts are still available without `--picker`.

PowerShell and Bash older than 4.3 keep normal Tab completion. Press Enter after
`openai images generate` to open the picker. Hooks are inactive outside an
interactive terminal or when `TERM=dumb`.
