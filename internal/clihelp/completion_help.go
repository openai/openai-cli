package clihelp

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

const completionReferenceArguments = "Use the same executable with the arguments help, --all, and @completion for setup options."

// CompletionSetupGuidance preserves the launching executable when suggesting setup help.
func CompletionSetupGuidance(ctx context.Context, root *cli.Command) string {
	shell := terminal.ParentShell(ctx)
	native, _ := ShellInvocations(root, DisplayInvocation(root), shell)
	if native == "" {
		return completionReferenceArguments
	}
	return "Run " + native + " help --all " + completionTopic(shell) + " for setup options."
}

func completionTopic(shell string) string {
	if runtime.GOOS == "windows" || shell == "pwsh" {
		return `"@completion"` // Bare @completion is a splatted variable in PowerShell.
	}
	return "@completion"
}

// Completion help describes Explore first. Its full reference lists the declared local flags.
func completionHelp(command *cli.Command, invocation string, width, navigationWidth int) string {
	style := commandHelpStyle(command)
	var out strings.Builder
	heading := func(title string) { fmt.Fprintln(&out, style.render(style.section, title)) }
	note := func(text string) { out.WriteString(style.render(style.note, wrapDescription(text, "  ", width))) }
	shell := terminal.ParentShell(context.Background())
	native, bash := ShellInvocations(command.Root(), invocation, shell)
	topic := completionTopic(shell)
	fmt.Fprintln(&out, style.render(style.title, "Shell completion"))
	if fullReferenceRequested(command) {
		if native == "" {
			note("Pass @completion and a shell name to this executable to print its completion script.")
		} else {
			out.WriteString(style.synopsis(native+" "+topic, []string{"[SHELL]", "[FLAGS]"}, width))
			note("Choose bash, zsh, fish, or pwsh to print its completion script.")
			out.WriteByte('\n')
			heading("EXAMPLES")
			script := native + " " + topic + " zsh"
			fmt.Fprintln(&out, "  "+commandsuggest.Record(command, script))
			note("To enable insertion manually, run this command in zsh.")
			fmt.Fprintln(&out, "  "+commandsuggest.RecordFor(command, "zsh", bash+` @completion zsh --install-suggestions --profile "${ZDOTDIR-$HOME}/.zshrc"`))
		}
		out.WriteByte('\n')
		heading("FLAGS")
		flags := command.VisibleFlags()
		names := make([]string, len(flags))
		descriptions := make([]string, len(flags))
		for i, flag := range flags {
			names[i], descriptions[i] = flagReference(command, flag)
			if doc, ok := flag.(cli.DocGenerationFlag); ok {
				descriptions[i] = doc.GetUsage()
			}
		}
		column := helpDescriptionColumn(navigationWidth, names)
		for i, label := range names {
			name, value, _ := strings.Cut(label, "=")
			out.WriteString(style.entry(label, descriptions[i], style.flag(name, value), style.text, navigationWidth, column))
		}
		return out.String()
	}

	if native == "" {
		note(completionReferenceArguments)
		return out.String()
	}
	explore := native + " explore"
	options := native + " help --all " + topic
	column := helpDescriptionColumn(navigationWidth, []string{explore, options})
	out.WriteString(style.entry(explore, "Select a command and press Enter.", commandsuggest.Record(command, explore), style.text, navigationWidth, column))
	out.WriteByte('\n')
	switch shell {
	case "zsh":
		note("With the CLI shell installer, zsh fills your prompt automatically.")
	case "fish":
		if runtime.GOOS == "linux" {
			note("With the Linux fish package, selections fill your prompt in new terminals.")
		} else {
			note("With shell insertion enabled, fish fills your prompt automatically.")
		}
	case "bash":
		note("With Bash 4.3+ shell setup, press Ctrl+X, then Tab to insert your selection.")
	default:
		note("If Explore prints the command, copy it to your prompt.")
	}
	note("Edit the command, then press Enter to run it.")
	out.WriteByte('\n')
	out.WriteString(style.entry(options, "Show setup options.", commandsuggest.Record(command, options), style.note, navigationWidth, column))
	return out.String()
}
