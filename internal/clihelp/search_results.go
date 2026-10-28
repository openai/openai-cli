package clihelp

import (
	"strings"

	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/urfave/cli/v3"
)

// SearchResults uses the help palette and layout without opening a terminal picker.
func SearchResults(command *cli.Command, matches []commandsuggest.Candidate) string {
	style := commandHelpStyle(command)
	if len(matches) == 0 {
		return style.render(style.note, "No matching commands. Try fewer words.") + "\n"
	}
	var out strings.Builder
	out.WriteString(style.render(style.section, "MATCHING COMMANDS") + "\n")
	width := helpNavigationWidth(command)
	var names []string
	for _, match := range matches {
		names = append(names, match.Command)
	}
	column := helpDescriptionColumn(width, names)
	for i, match := range matches {
		if i > 0 {
			out.WriteByte('\n')
		}
		label := commandsuggest.Record(command, match.Command)
		out.WriteString(style.entry(match.Command, shortDescription(match.Description), label, width, column))
	}
	return out.String()
}
