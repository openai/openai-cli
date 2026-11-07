package clihelp

import (
	"context"

	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// CommandExamples returns the same declared examples that complete help renders.
// The returned slice can be modified without changing help's definitions.
func CommandExamples(command *cli.Command, path string) []Example {
	content, _ := commandContent(command, path)
	return append([]Example(nil), content.Examples...)
}

// ExampleInvocation keeps Bash-specific examples copyable in their stated shell.
func ExampleInvocation(command *cli.Command, bash, native string) string {
	if content, _ := command.Metadata["help-content"].(Content); content.BashExamples {
		return bash
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
	native, bash := ShellInvocations(root, invocation, shell)
	return native, ExampleInvocation(command, bash, native)
}

// Keep a visible example available for insertion only in shells that accept it.
func recordHelpExample(command *cli.Command, text string, bashOnly bool) string {
	if bashOnly {
		return recordForShells(command, text, "bash", "zsh")
	}
	return commandsuggest.Record(command, text)
}

func recordForShells(command *cli.Command, text string, shells ...string) string {
	rendered := text
	for _, shell := range shells {
		if candidate := commandsuggest.RecordFor(command, shell, text); candidate != text {
			rendered = candidate
		}
	}
	return rendered
}
