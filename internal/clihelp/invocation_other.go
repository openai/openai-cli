//go:build !windows

package clihelp

import (
	"context"
	"strings"

	"github.com/openai/openai-cli/internal/terminal"
)

func quoteInvocation(name, fallback string) string {
	switch terminal.ParentShell(context.Background()) {
	case "pwsh":
		return "& " + terminal.PowerShellQuote(name)
	case "fish":
		return terminal.FishQuote(name)
	}
	return "'" + strings.ReplaceAll(name, "'", "'\\''") + "'"
}
