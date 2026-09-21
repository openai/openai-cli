package cmd

import (
	"fmt"
	"time"

	"github.com/urfave/cli/v3"
)

const multipartTestTimeout = 5 * time.Second

// Resolve generated commands through the public root instead of depending on
// generated variable names. Each test configures its own root command instance.
func runtimeTestCommand(group, name string) cli.Command {
	for _, resource := range Command.Commands {
		if resource.Name != group {
			continue
		}
		for _, command := range resource.Commands {
			if command.Name == name {
				return *command
			}
		}
	}
	panic(fmt.Sprintf("command %s %s not found", group, name))
}
