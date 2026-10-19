package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Files can cross one verified Go launcher when help recognized a go-run
// invocation. Image and setup callers retain their immediate-parent lookup.
func fileReceiptShell(ctx context.Context, invocation fileInvocation) string {
	if ctx.Err() != nil || invocation.omitHint {
		return ""
	}
	pid := os.Getppid()
	name, err := cliterminal.ParentProcessName()
	if err != nil || os.Getppid() != pid {
		return ""
	}
	shell := fileReceiptShellName(ctx, invocation.goRun, name, func() (string, error) {
		return fileReceiptGoCallerName(pid)
	})
	if os.Getppid() != pid {
		return ""
	}
	return shell
}

func fileReceiptShellName(ctx context.Context, goRun bool, parent string, goCaller func() (string, error)) string {
	if ctx.Err() != nil {
		return ""
	}
	if shell := cliterminal.ShellName(parent); shell != "" {
		return shell
	}
	name := filepath.Base(strings.ReplaceAll(parent, `\`, "/"))
	if !goRun || strings.TrimSuffix(strings.ToLower(name), ".exe") != "go" {
		return ""
	}
	caller, err := goCaller()
	if err != nil || ctx.Err() != nil {
		return ""
	}
	// Unsupported wrappers stop resolution. Never search older ancestors or SHELL.
	return cliterminal.ShellName(caller)
}
