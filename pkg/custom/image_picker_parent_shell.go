package custom

import (
	"context"
	"path/filepath"
	"strings"
)

// imagePickerParentShell identifies the immediate caller. The login shell in
// SHELL can differ from an interactive subshell, so it is not a fallback here.
func imagePickerParentShell(ctx context.Context) string {
	if ctx.Err() != nil {
		return ""
	}
	name, err := imagePickerParentProcessName()
	if err != nil || ctx.Err() != nil {
		return ""
	}
	return imagePickerShellName(name)
}

func imagePickerShellName(name string) string {
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
