package clihelp

import (
	"context"
	"os"
	"runtime"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

type setupGuideContext struct {
	Run, Shell string
	Full       bool
}

func setupGuide(command *cli.Command) setupGuideContext {
	root := command.Root()
	shell := terminal.ParentShell(context.Background())
	invocation, _ := root.Metadata["help-invocation"].(string)
	run, _ := helpInvocations(root, invocation, shell)
	// When the parent is a script or IDE, label a platform-appropriate
	// example. Only the detected shell may determine safe launcher quoting.
	if shell == "" {
		shell = "bash"
		if runtime.GOOS == "windows" {
			shell = "pwsh"
		}
	}
	return setupGuideContext{Run: run, Shell: shell, Full: fullReferenceRequested(command)}
}

// Format authored setup guides after template expansion. Preserve shell lines
// and insertion links; only prose wraps and odd indentation levels change.
func formatGuide(command *cli.Command, text string) string {
	style := commandHelpStyle(command)
	width := helpWidth(command)
	invocation := DisplayInvocation(command.Root())
	native, _ := commandHelpInvocations(command, invocation)
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n\n") {
				out.WriteByte('\n')
			}
			continue
		}
		spaces := len(line) - len(strings.TrimLeft(line, " "))
		indent := strings.Repeat(" ", spaces-spaces%2)
		plain := ansi.Strip(trimmed)
		if spaces >= 6 || strings.HasPrefix(plain, invocation+" ") || native != "" && strings.HasPrefix(plain, native+" ") {
			out.WriteString(indent + trimmed + "\n")
			continue
		}
		wrapped := strings.TrimSuffix(wrapPlainDescription(trimmed, indent, width), "\n")
		switch {
		case spaces == 0 && (out.Len() == 0 || strings.ToUpper(plain) == plain && strings.IndexFunc(plain, unicode.IsLetter) >= 0):
			wrapped = style.render(style.section, wrapped)
		case strings.HasSuffix(plain, ":"):
			wrapped = style.render(style.note, wrapped)
		}
		out.WriteString(wrapped)
		out.WriteByte('\n')
	}
	output := command.Root().Writer
	if output == nil {
		output = os.Stdout
	}
	if out.Len() > 0 && welcomeTerminal(output) && !strings.HasSuffix(out.String(), "\n\n") {
		out.WriteByte('\n')
	}
	return out.String()
}
