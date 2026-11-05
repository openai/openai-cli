package clihelp

import (
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

// Synopses use declarations, never parsed values. Handwritten commands can keep
// an explicit UsageText. Verified positional aliases use help-positional-flags.
func commandSynopsis(command *cli.Command, name, invocation string, width int) string {
	style := commandHelpStyle(command)
	if command.UsageText != "" {
		var lines []string
		for _, line := range strings.Split(command.UsageText, "\n") {
			if suffix, found := strings.CutPrefix(line, "openai "); found {
				line = invocation + " " + suffix
			}
			lines = append(lines, "  "+line)
		}
		return strings.Join(lines, "\n") + "\n"
	}
	var arguments []string
	var groups, actions bool
	for _, child := range VisibleCommands(command) {
		if commandGroup(child) {
			groups = true
		} else {
			actions = true
		}
	}
	switch {
	case groups && actions:
		arguments = append(arguments, style.render(style.value, "GROUP")+" | "+style.render(style.value, "COMMAND"))
	case groups:
		arguments = append(arguments, style.render(style.value, "GROUP"))
	case actions:
		arguments = append(arguments, style.render(style.value, "COMMAND"))
	}
	if optional, _ := command.Metadata["help-optional-command"].(bool); optional && len(arguments) > 0 {
		arguments[0] = "[" + arguments[0] + "]"
	}
	flags := command.VisibleFlags()
	positionals := positionalHelpFlags(command, flags)
	for _, flag := range positionals {
		label := commandFlagValueLabel(command, flag)
		arguments = append(arguments, "["+style.render(style.value, label)+" | "+synopsisFlag(command, flag)+"]")
	}
	if command.ArgsUsage != "" {
		arguments = append(arguments, style.render(style.value, command.ArgsUsage))
	}
	for _, flag := range flags {
		if slices.Contains(positionals, flag) || len(flag.Names()) == 0 {
			continue
		}
		if inner, ok := flag.(interface{ GetOuterFlag() cli.Flag }); ok && slices.Contains(flags, inner.GetOuterFlag()) {
			// The parent value covers these nested fields in the synopsis.
			// FLAGS still documents every visible nested field separately.
			continue
		}
		argument := synopsisFlag(command, flag)
		required, _ := flag.(cli.RequiredFlag)
		if required == nil || !required.IsRequired() {
			argument = "[" + argument + "]"
		}
		if multi, ok := flag.(cli.DocGenerationMultiValueFlag); ok && multi.IsMultiValueFlag() {
			argument += "..."
		}
		arguments = append(arguments, argument)
	}
	if len(command.VisiblePersistentFlags()) > 0 {
		arguments = append(arguments, "["+style.render(style.value, "GLOBAL FLAG ...")+"]")
	}
	return style.synopsis(style.render(style.section, name), arguments, width)
}

// Wrap complete arguments without splitting command names or quoted paths.
func (style helpStyle) synopsis(name string, arguments []string, width int) string {
	var out strings.Builder
	out.WriteString("  " + name)
	column := 2 + ansi.StringWidth(name)
	for _, argument := range arguments {
		if ansi.StringWidth(argument)+4 > width && strings.Contains(argument, " | ") {
			// Alternatives can wrap at the separator without splitting a flag,
			// placeholder, or quoted executable path.
			argument = strings.ReplaceAll(argument, " | ", "\n    | ")
		}
		if column+1+ansi.StringWidth(argument) > width {
			out.WriteString("\n    " + argument)
			column = 4 + ansi.StringWidth(argument)
		} else {
			out.WriteString(" " + argument)
			column += 1 + ansi.StringWidth(argument)
		}
		if last := strings.LastIndexByte(argument, '\n'); last >= 0 {
			column = ansi.StringWidth(argument[last+1:])
		}
	}
	out.WriteByte('\n')
	return out.String()
}

func synopsisFlag(command *cli.Command, flag cli.Flag) string {
	name := flag.Names()[0]
	prefix := "--"
	if len(name) == 1 {
		prefix = "-"
	}
	return commandHelpStyle(command).flag(prefix+name, commandFlagValueLabel(command, flag))
}

func positionalHelpFlags(command *cli.Command, flags []cli.Flag) []cli.Flag {
	names, _ := command.Metadata["help-positional-flags"].([]string)
	var positionals []cli.Flag
	for _, name := range names {
		for _, flag := range flags {
			path, ok := flag.(interface{ GetPathParam() string })
			if ok && path.GetPathParam() != "" && len(flag.Names()) > 0 && flag.Names()[0] == name &&
				slices.Contains(command.Flags, flag) && !slices.Contains(positionals, flag) {
				positionals = append(positionals, flag)
			}
		}
	}
	return positionals
}

func synopsisInputNote(command *cli.Command) string {
	var names, keys []string
	for _, flag := range command.VisibleFlags() {
		required, ok := flag.(interface{ IsRequiredAsFlagOrStdin() bool })
		if !ok || !required.IsRequiredAsFlagOrStdin() || len(flag.Names()) == 0 {
			continue
		}
		input, ok := flag.(interface {
			GetBodyPath() string
			GetQueryPath() string
			GetHeaderPath() string
			GetPathParam() string
			IsBodyRoot() bool
		})
		if !ok {
			continue
		}
		var key string
		for _, candidate := range []string{input.GetBodyPath(), input.GetPathParam(), input.GetQueryPath(), input.GetHeaderPath()} {
			if candidate != "" {
				key = candidate
				break
			}
		}
		if key == "" && !input.IsBodyRoot() {
			continue
		}
		names = append(names, "--"+flag.Names()[0])
		if key != "" && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	if len(names) == 0 {
		return ""
	}
	note := "Required request inputs: " + strings.Join(names, ", ") + ". Supply them through flags or piped JSON/YAML."
	if len(positionalHelpFlags(command, command.VisibleFlags())) > 0 {
		note += " The shown positional arguments can replace their matching flags."
	}
	if len(keys) > 0 {
		note += " JSON/YAML keys: " + strings.Join(keys, ", ") + "."
	}
	return note
}
