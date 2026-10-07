package clihelp

import (
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// Content supplies complete handwritten guidance without changing command behavior.
// Command examples omit the executable; the renderer supplies the safe invocation.
type Content struct {
	Description string
	Examples    []Example
}

type Example struct {
	Description string
	Command     string
}

const commandHelpTemplate = `{{call (index .Metadata "complete-help")}}`

// Configure callbacks now, but enumerate flags only when the framework renders help.
// Parent links, inherited flags, and automatic help flags do not exist here yet.
func configureCommandHelp(command *cli.Command, invocation, path string) {
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	command.Metadata["help-path"] = path
	command.Metadata["complete-help"] = func() string {
		return commandHelpAtWidth(command, invocation, path, helpWidth(command))
	}
	if full, _ := command.Metadata["local-help-full"].(string); full != "" {
		command.CustomHelpTemplate = full
	} else if command.CustomHelpTemplate == "" && allowsHelpTopic(command) {
		command.CustomHelpTemplate = commandHelpTemplate
	}
	for _, child := range command.Commands {
		configureCommandHelp(child, invocation, strings.TrimSpace(path+" "+child.Name))
	}
}

func commandHelpAtWidth(command *cli.Command, invocation, path string, width int) string {
	var out strings.Builder
	name := strings.TrimSpace(invocation + " " + path)
	fmt.Fprintf(&out, "NAME:\n   %s\n", name)
	usage := command.Usage
	if path == "" {
		usage = "Use the OpenAI API from your terminal."
	}
	out.WriteString(wrapDescription(usage, "   ", width))
	fmt.Fprintf(&out, "\nSYNOPSIS:\n   %s\n", commandSynopsis(command, name, invocation))

	content, supplied := command.Metadata["help-content"].(Content)
	if !supplied {
		content = Content{Description: command.Description, Examples: examples[path]}
	}
	if content.Description != "" {
		out.WriteString("\nDESCRIPTION:\n")
		out.WriteString(wrapDescription(content.Description, "   ", width))
	}
	if path == "" {
		fmt.Fprintf(&out, "\nSTART HERE\n   %s help setup\n   %s models list\n", invocation, invocation)
	}
	if len(content.Examples) > 0 {
		out.WriteString("\nEXAMPLES:\n")
		for i, example := range content.Examples {
			if i > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(wrapDescription(example.Description, "   ", width))
			fmt.Fprintf(&out, "     %s %s\n", invocation, example.Command)
		}
		if path == "files create" || path == "files upload" {
			out.WriteString(wrapDescription("example.txt is the path to your existing file. Replace it with your file's path.", "   ", width))
		}
	}
	if len(VisibleCommands(command)) > 0 {
		out.WriteByte('\n')
		out.WriteString(commandList(command, width))
		fmt.Fprintf(&out, "\nCommand help: %s COMMAND --help\n", name)
	}
	if flags := command.VisibleFlags(); len(flags) > 0 {
		heading := "OPTIONS:"
		if path == "" {
			heading = "GLOBAL OPTIONS:"
		}
		out.WriteString("\n" + heading)
		out.WriteString(fullFlagGroups(command, flags, width))
	}
	if flags := command.VisiblePersistentFlags(); len(flags) > 0 {
		out.WriteString("\nGLOBAL OPTIONS:")
		out.WriteString(fullFlagGroups(command, flags, width))
	}
	if path == "" {
		out.WriteString("\nAdd --help (or -h) to any command. Help needs no API key or internet.\n")
	}
	fmt.Fprintf(&out, "\nKey setup: %s help setup\n", invocation)
	return out.String()
}

func commandSynopsis(command *cli.Command, name, invocation string) string {
	if command.UsageText != "" {
		// Existing handwritten usage uses the public command name. Replace only
		// that leading executable, never filenames or arbitrary description text.
		return strings.Replace(command.UsageText, "openai ", invocation+" ", 1)
	}
	// Required request fields may also arrive through stdin. The required-input
	// reference describes those fields without making flags the only syntax.
	name += " [OPTIONS]"
	if len(VisibleCommands(command)) > 0 {
		name += " COMMAND"
	}
	if command.ArgsUsage != "" {
		name += " " + command.ArgsUsage
	}
	return name
}

var examples = map[string][]Example{
	"images": {{"Generate an image:", `images generate --prompt "A tiny orange robot"`}},
	"models list": {
		{"List available models:", "models list"},
		{"Return complete JSON:", "models list --format json"},
		{"Print model IDs for a script:", "models list --transform id --raw-output"},
	},
	"models retrieve":   {{"Inspect a model:", "models retrieve --model gpt-5.5"}},
	"responses create":  {{"Create a text response:", `responses create --model gpt-5.5 --input "Say hello"`}},
	"files create":      {{"Upload an existing file:", `files create --file ./example.txt --purpose assistants`}},
	"files upload":      {{"Upload an existing file:", `files upload --file ./example.txt --purpose assistants`}},
	"images generate":   {{"Generate an image:", `images generate --prompt "A tiny orange robot"`}},
	"images inline":     {{"Turn automatic image previews on:", "images inline on"}},
	"images inline on":  {{"Turn automatic image previews on:", "images inline on"}},
	"images inline off": {{"Turn automatic image previews off:", "images inline off"}},
}

func isRequired(flag cli.Flag) bool {
	if required, ok := flag.(interface{ IsRequiredAsFlagOrStdin() bool }); ok {
		return required.IsRequiredAsFlagOrStdin()
	}
	required, ok := flag.(cli.RequiredFlag)
	return ok && required.IsRequired()
}

func shortDescription(text string) string {
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\\n", " ")), " ")
	text = strings.ReplaceAll(text, "`", "")
	if end := strings.Index(text, ". "); end >= 0 {
		text = text[:end+1]
	}
	return text
}
