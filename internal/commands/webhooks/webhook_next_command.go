package webhooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/terminal"
)

type webhookInvocation struct {
	display     string
	executable  string
	goRun       bool
	goRunDir    string
	requestArgs []string
	omitHint    bool
}

// Follow one verified Go launcher only when help recognized a go-run invocation.
func webhookCommandShell(ctx context.Context, invocation webhookInvocation) string {
	if ctx.Err() != nil || invocation.omitHint {
		return ""
	}
	pid := os.Getppid()
	name, err := terminal.ParentProcessName()
	if err != nil || os.Getppid() != pid {
		return ""
	}
	shell := webhookCommandShellName(ctx, invocation.goRun, name, func() (string, error) {
		return terminal.GoCallerName(pid)
	})
	if os.Getppid() != pid {
		return ""
	}
	return shell
}

func webhookCommandShellName(ctx context.Context, goRun bool, parent string, goCaller func() (string, error)) string {
	if ctx.Err() != nil {
		return ""
	}
	if shell := terminal.ShellName(parent); shell != "" {
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
	return terminal.ShellName(caller)
}

// Keep help's executable selection, using the actual shell for quoted paths.
func webhookCommandInvocation(invocation webhookInvocation, shell string) string {
	if !utf8.ValidString(invocation.executable) {
		return ""
	}
	if invocation.goRun {
		quote := terminal.ShellQuoter(shell)
		packagePath, recognized := strings.CutPrefix(invocation.display, "go run ")
		if quote == nil || !recognized || packagePath == "" || !filepath.IsAbs(invocation.goRunDir) || !utf8.ValidString(invocation.goRunDir) || strings.ContainsRune(invocation.goRunDir, 0) {
			return ""
		}
		return "go -C " + quote(invocation.goRunDir) + " run " + quote(packagePath)
	}
	if invocation.display == "" {
		return "openai"
	}
	plain, _ := terminal.QuoteProperties(invocation.display)
	if plain || invocation.executable == "" {
		return invocation.display
	}
	quote := terminal.ShellQuoter(shell)
	if quote == nil {
		return ""
	}
	command := quote(invocation.executable)
	if shell == "pwsh" {
		command = "& " + command
	}
	return command
}
