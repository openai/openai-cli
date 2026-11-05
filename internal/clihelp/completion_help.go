package clihelp

import (
	"fmt"
	"strings"

	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/urfave/cli/v3"
)

// Completion has a local reference. Use the root help layout without its API flags.
func completionHelp(command *cli.Command, invocation string, width, navigationWidth int) string {
	style := commandHelpStyle(command)
	var out strings.Builder
	heading := func(title string) { fmt.Fprintln(&out, style.render(style.section, title)) }
	note := func(text string) { out.WriteString(style.render(style.note, wrapDescription(text, "  ", width))) }
	native := NativeInvocation(command.Root(), invocation)

	heading("Shell completion")
	out.WriteString(style.synopsis(native+` "@completion"`, []string{"[SHELL]", "[FLAGS]"}, width))
	out.WriteByte('\n')

	heading("START HERE")
	explore := native + " explore"
	script := native + ` "@completion" zsh`
	column := helpDescriptionColumn(navigationWidth, []string{explore, script})
	out.WriteString(style.entry(explore, "Choose a command to edit at your prompt.", commandsuggest.Record(command, explore), navigationWidth, column))
	out.WriteString(style.entry(script, "Print a script for zsh, bash, fish, or pwsh.", commandsuggest.Record(command, script), navigationWidth, column))
	out.WriteByte('\n')

	heading("PROMPT INSERTION")
	note("The shell installer enables insertion.")
	note("Zsh and fish fill your prompt. In Bash 4.3+, press Ctrl+X, then Tab.")
	note("Manual zsh setup (for Bash or fish, change the shell and omit --profile):")
	fmt.Fprintln(&out, "    "+commandsuggest.RecordFor(command, "zsh", invocation+` @completion zsh --install-suggestions --profile "${ZDOTDIR-$HOME}/.zshrc"`))
	out.WriteByte('\n')

	heading("FLAGS")
	options := []struct{ name, description string }{
		{"--suggestions-status", "Inspect saved insertion setup."},
		{"--uninstall-suggestions", "Remove managed insertion setup."},
		{"--suggestions", "Include insertion in the printed script."},
		{"--suggestions-double-tab", "With --suggestions or --install-suggestions. May delay Tab."},
		{"--suggestions-click", "Enable Kitty clicks with zsh and --suggestions."},
		{"--suggestions-adapter=kitty", "Print the Kitty adapter. Omit the shell name."},
		{"--picker", "Include image shortcuts in the script."},
		{"--install-picker", "Install image shortcuts for future shells."},
		{"--uninstall-picker", "Remove managed image picker shortcuts."},
		{"--automatic", "With --install-picker, honor opt-outs. Omit the shell."},
		{"--profile=PATH", "Choose a startup file for setup."},
		{"--suggest-executable", "Print executable typo suggestions separately."},
	}
	names := make([]string, len(options))
	for i, option := range options {
		names[i] = option.name
	}
	column = helpDescriptionColumn(navigationWidth, names)
	for _, option := range options {
		name, value, _ := strings.Cut(option.name, "=")
		out.WriteString(style.entry(option.name, option.description, style.flag(name, value), navigationWidth, column))
	}
	return out.String()
}
