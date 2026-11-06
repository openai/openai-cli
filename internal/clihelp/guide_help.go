package clihelp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

type setupGuideContext struct {
	Run, AfterKeyEntry, Shell string
	Full, StartPowerShell     bool
}

func setupGuide(command *cli.Command) setupGuideContext {
	parent, _ := terminal.ParentProcessName()
	return setupGuideForParent(command, runtime.GOOS, parent)
}

func setupGuideForParent(command *cli.Command, platform, parent string) setupGuideContext {
	root := command.Root()
	shell := terminal.ShellName(parent)
	invocation, _ := root.Metadata["help-invocation"].(string)
	run, _ := helpInvocations(root, invocation, shell)
	guide := setupGuideContext{Run: run, AfterKeyEntry: run, Shell: shell, Full: fullReferenceRequested(command)}
	switch shell {
	case "bash", "zsh", "fish", "pwsh":
		return guide
	}
	if platform != "windows" {
		guide.Shell = "bash"
		return guide
	}

	// A cmd user enters their key and runs later examples in Windows PowerShell.
	// Keep the caller's spelling for instructions executed before that transition.
	guide.Shell = "pwsh"
	guide.StartPowerShell = !strings.EqualFold(filepath.Base(strings.ReplaceAll(parent, `\`, "/")), "powershell.exe")
	if name, ok := root.Metadata["help-executable-path"].(string); ok {
		quoted := terminal.PowerShellQuote(name)
		guide.AfterKeyEntry = "& " + quoted
		// The double-quoted form uses Unicode escapes unavailable in Windows PowerShell 5.1.
		if strings.HasPrefix(quoted, `"`) {
			guide.AfterKeyEntry = ""
		}
	}
	if root.Metadata["help-control-launcher"] == true {
		guide.AfterKeyEntry = ""
	}
	if !guide.StartPowerShell {
		guide.Run = guide.AfterKeyEntry
	}
	return guide
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
		// A guide opened in cmd.exe can print a quoted PowerShell invocation.
		// Its call operator identifies a command to keep on one line.
		powerShellCall := strings.HasPrefix(plain, "& ")
		if spaces >= 6 || powerShellCall || strings.HasPrefix(plain, invocation+" ") || native != "" && strings.HasPrefix(plain, native+" ") {
			out.WriteString(indent + trimmed + "\n")
			continue
		}
		wrapped := strings.TrimSuffix(wrapPlainDescription(trimmed, indent, width), "\n")
		switch {
		case spaces == 0 && out.Len() == 0:
			wrapped = style.render(style.title, wrapped)
		case spaces == 0 && strings.ToUpper(plain) == plain && strings.IndexFunc(plain, unicode.IsLetter) >= 0:
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
