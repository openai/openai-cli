package clihelp

import (
	"strings"

	"github.com/urfave/cli/v3"
)

// SearchCommandText selects the same static prose as compact and full help.
// Examples have their own catalog entries and should not supply route keywords.
func SearchCommandText(command *cli.Command, path string) (summary, detail string) {
	content, supplied := commandContent(command, path)
	if !supplied {
		return command.Usage, content.Description
	}
	summary = content.Summary
	if summary == "" {
		summary = command.Usage
	}
	return summary, strings.Join([]string{content.Description, content.CompactNote, content.InputNote, content.Notes, content.GlobalOptionsNote}, "\n")
}

// SearchFlagText returns public brief and reference prose without rendering
// a flag, reading its value/default, or appending environment variables.
func SearchFlagText(command *cli.Command, flag cli.Flag) string {
	doc, ok := flag.(cli.DocGenerationFlag)
	if !ok {
		return configuredFlagBrief(command, flag)
	}
	full := flagUsageText(command, flag, doc)
	if brief := configuredFlagBrief(command, flag); brief != "" && brief != full {
		return brief + "\n" + full
	}
	return full
}
