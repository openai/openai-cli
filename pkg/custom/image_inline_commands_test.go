package custom

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// These workflow checks use synthetic files and an in-memory font bridge.
// They never access Terminal, register native fonts, or call an API.
func TestImageInlineSetupAndRepairHaveNoProfileCreationOptions(t *testing.T) {
	var commands []*cli.Command
	root := &cli.Command{Commands: []*cli.Command{{Name: "images"}}}
	registerImageInline(root)
	for _, resource := range root.Commands {
		if resource.Name != "images" {
			continue
		}
		for _, group := range resource.Commands {
			if group.Name != "inline" {
				continue
			}
			for _, command := range group.Commands {
				if command.Name == "setup" || command.Name == "repair" {
					commands = append(commands, command)
				}
			}
		}
	}
	if len(commands) != 2 {
		t.Fatalf("expected setup and repair, found %d", len(commands))
	}
	for _, command := range commands {
		for _, argument := range []string{"--new-window", "--no-open", "--help"} {
			t.Run(command.Name+argument, func(t *testing.T) {
				var output bytes.Buffer
				called := false
				// Use the registered command's flags/help, with a harmless action
				// so a regression cannot reach native Terminal APIs during tests.
				probe := &cli.Command{Name: command.Name, Usage: command.Usage, Description: command.Description, Flags: command.Flags, Writer: &output, ErrWriter: &output,
					Action: func(context.Context, *cli.Command) error { called = true; return nil }}
				err := probe.Run(context.Background(), []string{command.Name, argument})
				if called {
					t.Fatal("profile option reached the command action")
				}
				if argument == "--help" {
					if err != nil {
						t.Fatal(err)
					}
					for _, obsolete := range []string{"--new-window", "--no-open", "Open this profile", "NEW window"} {
						if strings.Contains(output.String(), obsolete) {
							t.Fatalf("help advertises removed profile workflow: %q", output.String())
						}
					}
				} else if err == nil {
					t.Fatalf("removed profile option %s was accepted", argument)
				}
			})
		}
	}
}
