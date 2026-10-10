# Shell completion

Completion lists commands, help topics, flags, and file-input paths without an API key or network connection.
It also suggests [known format and Files purpose values](completion-values.md).

After upgrading, reload completion in your current shell to enable the new file-path behavior.
Older loaded scripts keep their previous behavior until refreshed.

If you use image-picker Tab shortcuts, reload the [picker completion script](image-picker-shortcuts.md) to preserve those shortcuts.

For ordinary completion, run the command for your shell.

Bash:

```bash
eval "$(openai @completion bash)"
```

Zsh, after completion has been initialized with `compinit`:

```zsh
source <(openai @completion zsh)
```

Fish:

```fish
openai @completion fish | source
```

PowerShell:

```powershell
openai @completion pwsh | Out-String | Invoke-Expression
```

These commands refresh the current shell session.
If your startup configuration loads a saved completion script, regenerate that script for future sessions too.
