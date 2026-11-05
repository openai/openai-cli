package clihelp

import "github.com/urfave/cli/v3"

// CommandExamples returns the same declared examples that complete help renders.
// The returned slice can be modified without changing help's definitions.
func CommandExamples(command *cli.Command, path string) []Example {
	source := examples[path]
	if content, ok := command.Metadata["help-content"].(Content); ok {
		source = content.Examples
	}
	return append([]Example(nil), source...)
}
