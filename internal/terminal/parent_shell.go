package terminal

import (
	"context"
	"path/filepath"
	"strings"
)

// ParentShell identifies the immediate caller. The login shell in
// SHELL can differ from an interactive subshell, so it is not a fallback here.
func ParentShell(ctx context.Context) string {
	if ctx.Err() != nil {
		return ""
	}
	name, err := ParentProcessName()
	if err != nil || ctx.Err() != nil {
		return ""
	}
	return ShellName(name)
}

// ShellName recognizes supported shells from a process name or executable path.
func ShellName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.TrimPrefix(strings.ToLower(name), "-")
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "bash", "zsh", "fish", "pwsh":
		return name
	default:
		return ""
	}
}
