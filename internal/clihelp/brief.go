package clihelp

import (
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// Help is derived from the command definitions so regenerated flags remain
// available. Only the presentation changes; flag objects are never rewritten.
func configureCommandHelp(command *cli.Command, invocation, path string) {
	configureCommandHelpAtWidth(command, invocation, path, helpWidth(command))
}

func configureCommandHelpAtWidth(command *cli.Command, invocation, path string, width int) {
	if path != "" && command.CustomHelpTemplate == "" && allowsHelpTopic(command) {
		if command.Metadata == nil {
			command.Metadata = map[string]any{}
		}
		command.Metadata["brief-help"] = briefHelpAtWidth(command, invocation, path, width)
		command.CustomHelpTemplate = `{{index .Metadata "brief-help"}}`
	}
	for _, child := range command.Commands {
		configureCommandHelpAtWidth(child, invocation, strings.TrimSpace(path+" "+child.Name), width)
	}
}

func briefHelp(command *cli.Command, invocation, path string) string {
	return briefHelpAtWidth(command, invocation, path, helpWidth(command))
}

func briefHelpAtWidth(command *cli.Command, invocation, path string, width int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s %s\n", invocation, path)
	if command.Usage != "" {
		out.WriteString(wrapDescription(shortDescription(command.Usage), "", width))
	}
	if example := examples[path]; example != "" {
		fmt.Fprintf(&out, "\nEXAMPLE\n  %s %s\n", invocation, example)
		if path == "files create" || path == "files upload" {
			out.WriteString(wrapDescription("example.txt is the path to your existing file. Replace it with your file's path.", "", width))
		}
	}
	children := VisibleCommands(command)
	if len(children) > 0 {
		out.WriteByte('\n')
		out.WriteString(commandList(command, width))
		fmt.Fprintf(&out, "\nCommand help: %s %s COMMAND --help\n", invocation, path)
	} else {
		var required, optional []cli.Flag
		for _, flag := range command.Flags {
			if visible, ok := flag.(cli.VisibleFlag); ok && !visible.IsVisible() {
				continue
			}
			if isRequired(flag) {
				required = append(required, flag)
			} else {
				optional = append(optional, flag)
			}
		}
		if len(required) > 0 {
			out.WriteString("\nREQUIRED INPUTS\n")
			for _, flag := range required {
				writeBriefFlag(&out, flag, width)
			}
		}
		if len(optional) > 0 {
			out.WriteString("\nOPTIONAL INPUTS (more in full help)\n")
			// Put common choices first without changing the parser's flag order.
			shown := make(map[int]bool)
			for _, name := range []string{"model", "input", "size", "quality", "limit"} {
				for i, flag := range optional {
					if flag.Names()[0] == name && len(shown) < 3 {
						writeBriefFlag(&out, flag, width)
						shown[i] = true
					}
				}
			}
			for i, flag := range optional {
				if len(shown) == 3 {
					break
				}
				if !shown[i] {
					writeBriefFlag(&out, flag, width)
					shown[i] = true
				}
			}
		}
	}
	fmt.Fprintf(&out, "\nFull help: %s help --all %s\n", invocation, path)
	fmt.Fprintf(&out, "Key setup: %s help setup\n", invocation)
	return out.String()
}

var examples = map[string]string{
	"images":            `images generate --prompt "A tiny orange robot"`,
	"models list":       "models list",
	"models retrieve":   "models retrieve --model gpt-5.5",
	"responses create":  `responses create --model gpt-5.5 --input "Say hello"`,
	"files create":      `files create --file ./example.txt --purpose assistants`,
	"files upload":      `files upload --file ./example.txt --purpose assistants`,
	"images generate":   `images generate --prompt "A tiny orange robot"`,
	"images inline":     "images inline on",
	"images inline on":  "images inline on",
	"images inline off": "images inline off",
}

func isRequired(flag cli.Flag) bool {
	if required, ok := flag.(interface{ IsRequiredAsFlagOrStdin() bool }); ok {
		return required.IsRequiredAsFlagOrStdin()
	}
	required, ok := flag.(cli.RequiredFlag)
	return ok && required.IsRequired()
}

func writeBriefFlag(out *strings.Builder, flag cli.Flag, width int) {
	name := flag.Names()[0]
	if len(name) == 1 {
		name = "-" + name
	} else {
		name = "--" + name
	}
	var usage string
	if doc, ok := flag.(cli.DocGenerationFlag); ok {
		usage = shortDescription(doc.GetUsage())
		if doc.TakesValue() {
			name += " VALUE"
		}
	}
	// These generated descriptions are absent or describe an SDK object.
	// Explain the CLI input here without changing its API or flag definition.
	switch flag.Names()[0] {
	case "model":
		usage = "Model ID to use for this request."
	case "input":
		if usage == "" {
			usage = "Text or other input to send to the model."
		}
	}
	if input, ok := flag.(interface{ IsFileInput() bool }); ok && input.IsFileInput() {
		usage = "Path to the file to upload."
	}
	writeHelpEntry(out, name, usage, width)
}

func shortDescription(text string) string {
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\\n", " ")), " ")
	text = strings.ReplaceAll(text, "`", "")
	if end := strings.Index(text, ". "); end >= 0 {
		text = text[:end+1]
	}
	return text
}
