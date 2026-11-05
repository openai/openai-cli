package clihelp

import (
	"context"

	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// CommandExamples returns the same declared examples that complete help renders.
// The returned slice can be modified without changing help's definitions.
func CommandExamples(command *cli.Command, path string) []Example {
	source := examples[path]
	if content, ok := command.Metadata["help-content"].(Content); ok {
		source = content.Examples
	}
	return append([]Example(nil), source...)
}

// ExampleInvocation keeps Bash-specific examples copyable in their stated shell.
func ExampleInvocation(command *cli.Command, fallback, native string) string {
	if content, _ := command.Metadata["help-content"].(Content); content.BashExamples {
		return fallback
	}
	return native
}

// Use the detected shell only when a launcher contains characters that need
// shell-specific escapes. An unrecognized shell cannot safely copy that path.
func commandHelpInvocations(command *cli.Command, invocation string) (native, example string) {
	root := command.Root()
	shell := ""
	if root.Metadata["help-control-launcher"] == true {
		shell = terminal.ParentShell(context.Background())
	}
	native, bash := helpInvocations(root, invocation, shell)
	return native, ExampleInvocation(command, bash, native)
}
