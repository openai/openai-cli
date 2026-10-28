//go:build ignore

// Run with go run scripts/help-catalog-audit.go in a credential-free environment.
// This inventory supports offline help audits; it never executes API commands.
package main

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
	"github.com/urfave/cli/v3"
)

type flagRecord struct {
	Names      []string
	Usage      string
	Default    string
	Required   bool
	TakesValue bool
}

type commandRecord struct {
	Path        []string
	Aliases     []string
	Hidden      bool
	Usage       string
	UsageText   string
	Description string
	Section     string
	Flags       []flagRecord
	Children    []string
}

func main() {
	root := commands.Command
	root.Writer = io.Discard
	root.ErrWriter = io.Discard
	if code := app.Run(context.Background(), []string{"openai", "--help"}, root); code != 0 {
		os.Exit(code)
	}
	var records []commandRecord
	var visit func(*cli.Command, []string, bool)
	visit = func(command *cli.Command, path []string, hidden bool) {
		record := commandRecord{Path: path, Aliases: command.Aliases, Hidden: hidden || command.Hidden,
			Usage: command.Usage, UsageText: command.UsageText, Description: command.Description}
		record.Section, _ = command.Metadata["help-command-section"].(string)
		for _, flag := range command.VisibleFlags() {
			item := flagRecord{Names: flag.Names()}
			if doc, ok := flag.(cli.DocGenerationFlag); ok {
				item.Usage, item.Default, item.TakesValue = doc.GetUsage(), doc.GetDefaultText(), doc.TakesValue()
			}
			if required, ok := flag.(interface{ IsRequiredAsFlagOrStdin() bool }); ok {
				item.Required = required.IsRequiredAsFlagOrStdin()
			} else if required, ok := flag.(cli.RequiredFlag); ok {
				item.Required = required.IsRequired()
			}
			record.Flags = append(record.Flags, item)
		}
		for _, child := range command.VisibleCommands() {
			record.Children = append(record.Children, child.Name)
		}
		records = append(records, record)
		for _, child := range command.Commands {
			visit(child, append(append([]string{}, path...), child.Name), record.Hidden)
		}
	}
	visit(root, []string{}, false)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(records); err != nil {
		panic(err)
	}
}
