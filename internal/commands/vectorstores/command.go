package vectorstores

import (
	"context"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/urfave/cli/v3"
)

// Options keeps shared request configuration and safe error wording in assembly.
type Options struct {
	NewService   func(context.Context, *cli.Command) (Service, error)
	ErrorMessage func(error) string
}

const configuredMetadata = "openai-vector-store-workspace"

type workspaceCommand struct {
	options  Options
	terminal func(*cli.Command) (*os.File, *os.File, bool)
	run      func(context.Context, Service, func(error) string, *os.File, *os.File) error
}

// Configure adds the interactive workspace without replacing generated operations.
func Configure(root *cli.Command, options Options) {
	workspaceCommand{options: options, terminal: workspaceTerminal, run: runWorkspace}.configure(root)
}

func (w workspaceCommand) configure(root *cli.Command) {
	group := root.Command("vector-stores")
	if group == nil || group.Metadata[configuredMetadata] == true || group.Command("manage") != nil {
		return
	}
	if group.Metadata == nil {
		group.Metadata = map[string]any{}
	}
	group.Metadata[configuredMetadata] = true
	previous := group.Action
	group.Action = func(ctx context.Context, command *cli.Command) error {
		input, output, terminal := w.terminal(command)
		if command.Args().Present() || !terminal || !workspaceFlagsAllowed(command, false) {
			if previous != nil {
				return previous(ctx, command)
			}
			return cli.ShowSubcommandHelp(command)
		}
		return w.launch(ctx, command, input, output)
	}
	group.Commands = append(group.Commands, &cli.Command{
		Name: "manage", Usage: "Create vector stores, upload files, and search interactively",
		Description: "Open the vector store workspace with terminal input and output. " +
			"Choose a store, create a store, upload a file, or search its contents. " +
			"Existing vector-store commands remain available for scripts and structured output.",
		HideHelpCommand: true,
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Present() {
				return workspaceFailure("The vector store workspace takes no positional arguments.", nil)
			}
			if !workspaceFlagsAllowed(command, true) {
				return workspaceFailure("The vector store workspace needs ordinary terminal output. Use direct vector-store commands for output formats, extraction, quiet, or debug options.", nil)
			}
			input, output, terminal := w.terminal(command)
			if !terminal {
				return workspaceFailure("The vector store workspace needs terminal input and output with cursor support. Use vector-stores list, create, files, or search in scripts.", nil)
			}
			return w.launch(ctx, command, input, output)
		},
	})
}

func (w workspaceCommand) launch(ctx context.Context, command *cli.Command, input, output *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.options.NewService == nil {
		return workspaceFailure("The vector store workspace is unavailable.", nil)
	}
	service, err := w.options.NewService(ctx, command)
	if err != nil {
		return err
	}
	return w.run(ctx, service, w.options.ErrorMessage, input, output)
}

func workspaceTerminal(command *cli.Command) (*os.File, *os.File, bool) {
	root := command.Root()
	input, _ := root.Reader.(*os.File)
	output, _ := root.Writer.(*os.File)
	return input, output, input != nil && output != nil &&
		term.IsTerminal(input.Fd()) && term.IsTerminal(output.Fd()) &&
		!strings.EqualFold(os.Getenv("TERM"), "dumb")
}

func workspaceFlagsAllowed(command *cli.Command, explicit bool) bool {
	root := command.Root()
	for _, name := range []string{"format", "format-error", "transform", "transform-error", "raw-output", "quiet", "debug", "verbose"} {
		if !root.IsSet(name) {
			continue
		}
		if explicit && (name == "format" || name == "format-error") {
			value := strings.ToLower(root.String(name))
			if value == "auto" || value == "text" {
				continue
			}
		}
		return false
	}
	return true
}

func workspaceFailure(message string, cause error) error {
	return &localcommands.LocalError{Message: message, Cause: cause}
}
