package clihelp

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/urfave/cli/v3"
)

// Content supplies complete handwritten guidance without changing command behavior.
// Command examples omit the executable; the renderer supplies the safe invocation.
type Content struct {
	Summary           string
	InputNote         string
	Description       string
	Examples          []Example
	GlobalOptionsNote string
	Notes             string
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
		width := helpNavigationWidth(command)
		return commandHelpAtWidths(command, invocation, path, min(width, 79), width)
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
	return commandHelpAtWidths(command, invocation, path, width, width)
}

func commandHelpAtWidths(command *cli.Command, invocation, path string, width, navigationWidth int) string {
	var out strings.Builder
	style := commandHelpStyle(command)
	heading := func(text string) string { return style.render(style.section, text) }
	name := strings.TrimSpace(invocation + " " + path)
	usage := command.Usage
	if path == "" {
		usage = "Use the OpenAI API from your terminal."
	}
	content, supplied := command.Metadata["help-content"].(Content)
	if !supplied {
		content = Content{Description: command.Description, Examples: examples[path]}
	}
	plainUsage := terminalProse(usage)
	summary := terminalProse(content.Summary)
	if summary == "" {
		summary = commandSummary(plainUsage)
	}
	if path == "" {
		banner := ""
		if command.Metadata["help-welcome"] == true {
			banner = welcomeHeader(command)
		}
		if banner != "" {
			out.WriteString(banner)
		} else {
			out.WriteString(heading(welcomeTitle))
			if version := welcomeVersion(command.Version); version != "" {
				separator := "  "
				if ansi.StringWidth(welcomeTitle+separator+version) > width {
					separator = "\n"
				}
				out.WriteString(separator + style.render(style.note, version))
			}
			out.WriteByte('\n')
		}
		out.WriteString(style.render(style.note, wrapDescription(usage, "", width)))
		out.WriteString("\n" + heading("USAGE") + "\n")
		out.WriteString(style.synopsis(name, []string{"[GLOBAL FLAGS]", "COMMAND"}, width))
	} else {
		out.WriteString(heading("NAME") + "\n")
		writeCommandIdentity(&out, name, strings.TrimSuffix(summary, "."), width)
		out.WriteString("\n" + heading("SYNOPSIS") + "\n")
		out.WriteString(commandSynopsis(command, name, invocation, width))
	}
	note := content.InputNote
	if note == "" {
		note = synopsisInputNote(command)
	}
	if note != "" {
		out.WriteByte('\n')
		out.WriteString(wrapDescription(note, "  ", width))
	}
	description := content.Description
	if strings.Join(strings.Fields(plainUsage), " ") != strings.Join(strings.Fields(summary), " ") {
		description = joinHelpDescriptions(usage, description)
	}
	if description == "" {
		description = usage
	}
	if description != "" && (path != "" || description != usage) {
		out.WriteString("\n" + heading("DESCRIPTION") + "\n")
		out.WriteString(wrapDescription(description, "  ", width))
	}
	if path == "" {
		content.Examples = []Example{{"Set up your API key:", "help setup"}, {"List available models:", "models list"}}
		if explore := command.Command("explore"); explore != nil && !explore.Hidden {
			content.Examples = append(content.Examples, Example{"Find image commands and examples offline:", "explore images"})
		}
	}
	if len(content.Examples) > 0 {
		var names []string
		for _, child := range VisibleCommands(command) {
			names = append(names, child.Name)
		}
		for _, example := range content.Examples {
			names = append(names, invocation+" "+example.Command)
		}
		column := helpDescriptionColumn(navigationWidth, names)
		title := "EXAMPLES"
		if path == "" {
			title = "START HERE"
		}
		out.WriteString("\n" + heading(title) + "\n")
		for i, example := range content.Examples {
			if path == "" {
				if column == 0 && i > 0 {
					out.WriteByte('\n')
				}
				label := commandsuggest.Record(command, invocation+" "+example.Command)
				out.WriteString(style.entry(invocation+" "+example.Command, strings.TrimSuffix(example.Description, ":")+".", label, navigationWidth, column))
				continue
			}
			if i > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(wrapDescription(example.Description, "  ", width))
			fmt.Fprintf(&out, "    $ %s\n", commandsuggest.Record(command, invocation+" "+example.Command))
		}
		if !supplied && (path == "files create" || path == "files upload") {
			out.WriteString(wrapDescription("example.txt is the path to your existing file. Replace it with your file's path.", "  ", width))
		}
	}
	if len(VisibleCommands(command)) > 0 {
		out.WriteByte('\n')
		out.WriteString(commandList(command, navigationWidth, navigationSuggestions(command, invocation, path)))
	}
	if flags := command.VisibleFlags(); len(flags) > 0 {
		title := "FLAGS"
		if path == "" {
			title = "GLOBAL FLAGS"
		}
		out.WriteString("\n" + heading(title) + "\n")
		out.WriteString(fullFlagGroups(command, flags, width))
	}
	if flags := command.VisiblePersistentFlags(); len(flags) > 0 {
		out.WriteString("\n" + heading("GLOBAL FLAGS") + "\n")
		out.WriteString(globalFlagIndex(flags, width))
		if content.GlobalOptionsNote != "" {
			out.WriteByte('\n')
			out.WriteString(wrapDescription(content.GlobalOptionsNote, "  ", width))
		}
		fmt.Fprintf(&out, "\n  Run $ %s for details.\n", commandsuggest.Record(command, invocation+" --help"))
	}
	out.WriteString("\n" + heading("NOTES") + "\n")
	if len(VisibleCommands(command)) > 0 {
		if ansi.StringWidth("  Command help: "+name+" COMMAND --help") <= width {
			fmt.Fprintf(&out, "  Command help: %s COMMAND --help\n", name)
		} else {
			out.WriteString("  Command help:\n" + style.synopsis(name, []string{"COMMAND", "--help"}, width))
		}
	}
	if content.Notes != "" {
		out.WriteString(style.render(style.note, wrapDescription(content.Notes, "  ", width)))
	} else if path == "" {
		out.WriteString(style.render(style.note, wrapDescription("Help needs no API key or internet.", "  ", width)))
	} else {
		fmt.Fprintf(&out, "  Key setup: %s\n", commandsuggest.Record(command, invocation+" help setup"))
	}
	output := command.Root().Writer
	if output == nil {
		output = os.Stdout
	}
	if welcomeTerminal(output) {
		out.WriteByte('\n')
	}
	return out.String()
}

func writeCommandIdentity(out *strings.Builder, name, summary string, width int) {
	out.WriteString("  " + name)
	if summary == "" {
		out.WriteByte('\n')
		return
	}
	out.WriteString(" -")
	column := 4 + ansi.StringWidth(name)
	for _, word := range strings.Fields(summary) {
		if column+1+ansi.StringWidth(word) > width {
			out.WriteString("\n  ")
			column = 2
		} else {
			out.WriteByte(' ')
			column++
		}
		out.WriteString(word)
		column += ansi.StringWidth(word)
	}
	out.WriteByte('\n')
}

func commandSummary(usage string) string {
	paragraph, _, _ := strings.Cut(usage, "\n\n")
	// The complete description retains references. Summaries name the operation
	// without allowing documentation URLs to overwhelm command indexes.
	for start := 0; start < len(paragraph); {
		index := strings.Index(paragraph[start:], " (http")
		if index < 0 {
			break
		}
		index += start
		end := inlineClosing(paragraph, index+1, '(', ')')
		if end < 0 {
			break
		}
		reference := paragraph[index+2 : end]
		if (strings.HasPrefix(reference, "https://") || strings.HasPrefix(reference, "http://")) && !strings.ContainsAny(reference, " \t\n") {
			paragraph = paragraph[:index] + paragraph[end+1:]
			start = index
		} else {
			start = end + 1
		}
	}
	paragraph = strings.Join(strings.Fields(paragraph), " ")
	for i := range len(paragraph) - 1 {
		if strings.ContainsRune(".!?", rune(paragraph[i])) && paragraph[i+1] == ' ' {
			return paragraph[:i+1]
		}
	}
	return paragraph
}

func joinHelpDescriptions(descriptions ...string) string {
	var paragraphs []string
	seen := map[string]bool{}
	for _, description := range descriptions {
		for _, paragraph := range strings.Split(description, "\n\n") {
			key := strings.Join(strings.Fields(paragraph), " ")
			if key != "" && !seen[key] {
				paragraphs = append(paragraphs, paragraph)
				seen[key] = true
			}
		}
	}
	return strings.Join(paragraphs, "\n\n")
}

func globalFlagIndex(flags []cli.Flag, width int) string {
	var names []string
	seen := map[string]bool{}
	for _, flag := range flags {
		for _, name := range flag.Names() {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			prefix := "--"
			if len(name) == 1 {
				prefix = "-"
			}
			names = append(names, prefix+name)
		}
	}
	return wrapPlainDescription(strings.Join(names, ", "), "  ", width)
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
	return commandSummary(terminalProse(text))
}
