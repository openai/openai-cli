package clihelp

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

// Content supplies complete handwritten guidance without changing command behavior.
// Command examples omit the executable; the renderer supplies the safe invocation.
type Content struct {
	Summary           string
	CompactNote       string // Operational guidance for the first page of a large command.
	InputNote         string
	Description       string
	Examples          []Example
	BashExamples      bool   // Examples explicitly labeled for Bash or zsh keep their authored shell syntax.
	GlobalOptionsNote string // Local commands describe their supported output options instead of listing API flags.
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
	if path == "@completion" {
		return completionHelp(command, invocation, width, navigationWidth)
	}
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
	if flag := requestedHelpFlag(command); flag != nil {
		if content.GlobalOptionsNote == "" || slices.Contains(command.Flags, flag) {
			return flagList(command, []cli.Flag{flag}, width)
		}
		// An inherited option can have a different meaning on a local command.
		// Show its local guidance, or fall back to the ordinary command page.
		words := strings.FieldsFunc(content.GlobalOptionsNote, func(r rune) bool {
			return r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, word := range words {
			if strings.HasPrefix(word, "-") && slices.Contains(flag.Names(), strings.TrimLeft(word, "-")) {
				return wrapDescription(content.GlobalOptionsNote, "  ", width)
			}
		}
	}
	plainUsage := terminalProse(usage)
	summary := terminalProse(content.Summary)
	// Every action has the same overview, regardless of how many flags or
	// descriptions it declares. Groups keep their child-command navigation.
	if path != "" && command != command.Root() && len(VisibleCommands(command)) == 0 &&
		!fullReferenceRequested(command) && command.CustomHelpTemplate == commandHelpTemplate {
		if summary == "" {
			// Later sentences in the opening paragraph can qualify an action's
			// effects or preconditions. Keep them together on the overview.
			summary = commandParagraph(plainUsage)
		}
		return compactCommandHelp(command, invocation, path, summary, content, supplied, width)
	}
	if summary == "" {
		summary = commandSummary(plainUsage)
	}
	native, exampleRun := commandHelpInvocations(command, invocation)
	if path == "" {
		banner := ""
		if command.Metadata["help-welcome"] == true {
			banner = welcomeHeader(command)
		}
		if banner != "" {
			out.WriteString(banner)
		} else {
			out.WriteString(style.render(style.title, welcomeTitle))
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
	} else if fullReferenceRequested(command) {
		out.WriteString(heading("NAME") + "\n")
		writeCommandIdentity(&out, style.render(style.title, name), strings.TrimSuffix(summary, "."), width)
		out.WriteString("\n" + heading("SYNOPSIS") + "\n")
		out.WriteString(commandSynopsis(command, name, invocation, width))
	} else {
		out.WriteString(commandIntroduction(command, invocation, path, summary, width))
	}
	note := content.InputNote
	if note == "" {
		note = synopsisInputNote(command)
	}
	if note != "" {
		out.WriteByte('\n')
		out.WriteString(wrapDescription(note, "  ", width))
	}
	if request.RequestFileFlag(command) != nil {
		out.WriteByte('\n')
		out.WriteString(wrapDescription("Read JSON or YAML from a pipe with --request-file -.", "  ", width))
		if example := requestPipeExample(native, path); example != "" {
			fmt.Fprintf(&out, "    $ %s\n", commandsuggest.Record(command, example))
		}
	}
	description := content.Description
	if strings.Join(strings.Fields(plainUsage), " ") != strings.Join(strings.Fields(summary), " ") {
		description = joinHelpDescriptions(usage, description)
	}
	if description = descriptionAfterSummary(terminalProse(description), summary); description != "" {
		out.WriteString("\n" + heading("DESCRIPTION") + "\n" + wrapPlainDescription(description, "  ", width))
	}
	if path == "" {
		content.Examples = []Example{{"Set up your API key:", "help setup"}, {"List available models:", "models list"}}
		if explore := command.Command("explore"); explore != nil && !explore.Hidden {
			content.Examples = append(content.Examples, Example{"Find image commands and examples offline:", "explore images"})
		}
		if help := command.Command("help"); help != nil && help.Command("piping") != nil {
			content.Examples = append(content.Examples, Example{"Pipe text, files, and API requests:", "help piping"})
		}
	}
	if len(content.Examples) > 0 && exampleRun != "" {
		run := exampleRun
		var names []string
		for _, child := range VisibleCommands(command) {
			names = append(names, child.Name)
		}
		for _, example := range content.Examples {
			names = append(names, run+" "+example.Command)
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
				label := commandsuggest.Record(command, run+" "+example.Command)
				out.WriteString(style.entry(run+" "+example.Command, strings.TrimSuffix(example.Description, ":")+".", label, style.note, navigationWidth, column))
				continue
			}
			if i > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(wrapDescription(example.Description, "  ", width))
			fmt.Fprintf(&out, "    $ %s\n", commandsuggest.Record(command, run+" "+example.Command))
		}
		if !supplied && (path == "files create" || path == "files upload") {
			out.WriteString(wrapDescription("example.txt is the path to your existing file. Replace it with your file's path.", "  ", width))
		}
	}
	if len(VisibleCommands(command)) > 0 {
		out.WriteByte('\n')
		out.WriteString(commandList(command, navigationWidth, navigationSuggestions(command, native, path)))
	}
	if flags := commandHelpFlags(command); len(flags) > 0 {
		title := "FLAGS"
		if path == "" {
			title = "GLOBAL FLAGS"
		}
		out.WriteString("\n" + heading(title) + "\n")
		out.WriteString(fullFlagGroups(command, flags, width))
	}
	full := fullReferenceRequested(command)
	if flags := command.VisiblePersistentFlags(); len(flags) > 0 {
		if full {
			out.WriteString("\n" + heading("GLOBAL FLAGS") + "\n")
		} else if content.GlobalOptionsNote != "" {
			out.WriteString("\n" + heading("OUTPUT") + "\n")
		}
		if full {
			out.WriteString(globalFlagIndex(flags, width))
			if content.GlobalOptionsNote != "" {
				out.WriteByte('\n')
			}
		}
		if content.GlobalOptionsNote != "" {
			out.WriteString(wrapDescription(content.GlobalOptionsNote, "  ", width))
		}
		if command.Root().Metadata["help-control-launcher"] == true {
			out.WriteString("\n" + wrapPlainDescription("For global flags, run the same executable with --help.", "", width))
		} else {
			globalHelp := commandsuggest.Record(command, native+" --help")
			if full {
				fmt.Fprintf(&out, "\n  Run $ %s for details.\n", globalHelp)
			} else {
				fmt.Fprintf(&out, "\n%s %s\n", style.render(style.note, "Global flags:"), globalHelp)
			}
		}
	}
	hasChildren := native != "" && (path == "" || full) && len(VisibleCommands(command)) > 0
	showKeySetup := native != "" && path != "" && full && content.GlobalOptionsNote == "" && content.Notes == ""
	if hasChildren || path == "" || showKeySetup || content.Notes != "" {
		out.WriteString("\n" + heading("NOTES") + "\n")
	}
	if hasChildren {
		name := strings.TrimSpace(native + " " + path)
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
	} else if showKeySetup {
		fmt.Fprintf(&out, "  Key setup: %s\n", commandsuggest.Record(command, native+" help setup"))
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

// Keep the first page useful on a terminal. The full reference owns secondary
// examples, generated API prose, and the complete list of flags.
func compactCommandHelp(command *cli.Command, invocation, path, summary string, content Content, supplied bool, width int) string {
	var out strings.Builder
	style := commandHelpStyle(command)
	out.WriteString(commandIntroduction(command, invocation, path, summary, width))

	native, exampleRun := commandHelpInvocations(command, invocation)
	var example string
	if len(content.Examples) != 0 && exampleRun != "" {
		example = exampleRun + " " + content.Examples[0].Command
	} else {
		example = requestPipeExample(native, path)
	}
	if example != "" {
		out.WriteString("\n" + style.render(style.section, "EXAMPLE") + "\n")
		fmt.Fprintf(&out, "  $ %s\n", commandsuggest.Record(command, example))
	}
	if content.InputNote == "" {
		if note := synopsisInputNote(command); note != "" {
			out.WriteByte('\n')
			out.WriteString(wrapDescription(note, "  ", width))
		}
	}
	// Handwritten guidance can describe saving, interactive inputs or other
	// operational constraints that users need before running the example.
	if !supplied && content.Description != "" {
		if note := commandSummary(descriptionAfterSummary(terminalProse(content.Description), summary)); note != "" {
			out.WriteByte('\n')
			out.WriteString(wrapPlainDescription(note, "  ", width))
		}
	}
	if supplied {
		notes := terminalProse(content.CompactNote)
		if notes == "" {
			notes = joinHelpDescriptions(terminalProse(content.InputNote), descriptionAfterSummary(terminalProse(content.Description), summary))
		}
		for _, text := range []string{notes, terminalProse(content.GlobalOptionsNote), terminalProse(content.Notes)} {
			if text != "" {
				out.WriteByte('\n')
				out.WriteString(wrapPlainDescription(text, "  ", width))
			}
		}
	}
	referenceTitle := "FULL REFERENCE"
	rows, complete := commonFlagRows(command, path, example, width)
	if rows != "" {
		section := "FLAGS"
		if !complete {
			section, referenceTitle = "COMMON FLAGS", "ALL FLAGS"
		}
		out.WriteString("\n" + style.render(style.section, section) + "\n" + rows)
	}
	// If the overview already contains the command's entire local guidance,
	// there is no additional reference for its user to discover.
	if rows == "" && len(content.Examples) <= 1 && content.CompactNote == "" &&
		command.Metadata["help-command-section"] != "Retired commands" &&
		descriptionAfterSummary(terminalProse(command.Usage), summary) == "" &&
		(supplied || strings.TrimSpace(command.Description) == "") {
		return out.String()
	}
	out.WriteString("\n" + style.render(style.note, referenceTitle) + "\n")
	if reference := fullReferenceCommand(command, invocation, path); reference != "" {
		fmt.Fprintf(&out, "  %s\n", commandsuggest.Record(command, reference))
	} else {
		out.WriteString(wrapPlainDescription("Use the same executable with help --all "+path+".", "  ", width))
	}
	return out.String()
}

func commandIntroduction(command *cli.Command, invocation, path, summary string, width int) string {
	if display, _ := command.Root().Metadata["help-display-invocation"].(string); display != "" {
		invocation = display
	}
	name := strings.TrimSpace(invocation + " " + path)
	if command.Metadata["help-command-section"] == "Retired commands" {
		style := commandHelpStyle(command)
		return style.render(style.title, name) + "\n" + wrapPlainDescription(summary, "  ", width)
	}
	return strings.TrimPrefix(commandSynopsis(command, name, invocation, width), "  ") + wrapPlainDescription(summary, "  ", width)
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

func commandParagraph(usage string) string {
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
	// A trailing "Learn more" link belongs in the reference with its URL.
	return strings.TrimSuffix(strings.Join(strings.Fields(paragraph), " "), " Learn more.")
}

func commandSummary(usage string) string {
	paragraph := commandParagraph(usage)
	for i := range len(paragraph) - 1 {
		if strings.ContainsRune(".!?", rune(paragraph[i])) && paragraph[i+1] == ' ' {
			if strings.HasSuffix(paragraph[:i+1], "i.e.") || strings.HasSuffix(paragraph[:i+1], "e.g.") {
				continue
			}
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

// Remove the sentence already shown in NAME. Keep references and any details
// attached to that sentence when the complete text differs from the summary.
func descriptionAfterSummary(description, summary string) string {
	if summary == "" {
		return description
	}
	first, rest, _ := strings.Cut(description, "\n")
	if strings.TrimSuffix(first, ".") == strings.TrimSuffix(summary, ".") {
		return strings.TrimLeft(rest, "\n")
	}
	if !strings.HasSuffix(summary, ".") && !strings.HasSuffix(summary, "?") && !strings.HasSuffix(summary, "!") {
		summary += "."
	}
	return strings.TrimPrefix(description, summary+" ")
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
