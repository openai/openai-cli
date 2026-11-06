package clihelp

import (
	"context"
	"path/filepath"

	"github.com/openai/openai-cli/internal/commandsuggest"
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
		root := command.Root()
		if name, ok := root.Metadata["help-executable-path"].(string); ok {
			return terminal.ShellQuote(filepath.ToSlash(name))
		}
		if root.Metadata["help-control-launcher"] == true && fallback != "" {
			// Explore can supply a display name here. Resolve the actual
			// executable before presenting a command to paste into Bash.
			_, bash := helpInvocations(root, fallback, "bash")
			return bash
		}
		return filepath.ToSlash(fallback)
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
