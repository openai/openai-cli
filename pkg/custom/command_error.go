package custom

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"
)

// commandError retains only the command needed to map API parameter paths to
// locally declared flags. It never copies flag values or changes the cause.
type commandError struct {
	command *cli.Command
	err     error
}

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }

func withCommandError(command *cli.Command, err error) error {
	if err == nil {
		return nil
	}
	var existing *commandError
	if errors.As(err, &existing) {
		return err
	}
	return &commandError{command: command, err: err}
}

// ConfigureCommandErrors composes with success wrappers without owning success
// output. Calling it again after help setup covers newly added local commands
// without wrapping existing actions or usage handlers twice.
func ConfigureCommandErrors(root *cli.Command) {
	// Let main present returned errors and exit, including errors from default
	// actions installed later by the framework. Preserve any explicit handler.
	if root.ExitErrHandler == nil {
		root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
	}
	var visit func(*cli.Command)
	visit = func(command *cli.Command) {
		if command.Name == "__complete" {
			return
		}
		if command.Metadata == nil {
			command.Metadata = map[string]any{}
		}
		if configured, _ := command.Metadata["openai-error-context"].(bool); !configured {
			command.Metadata["openai-error-context"] = true
			if command.Action != nil {
				next := command.Action
				command.Action = func(ctx context.Context, command *cli.Command) error {
					return withCommandError(command, next(ctx, command))
				}
			}
			previousUsage := command.OnUsageError
			command.OnUsageError = func(ctx context.Context, command *cli.Command, err error, subcommand bool) error {
				if previousUsage != nil {
					err = previousUsage(ctx, command, err, subcommand)
				}
				// The default handler also prints help to stdout. Return the failure
				// instead so the error presenter owns all diagnostic output.
				return withCommandError(command, err)
			}
		}
		for _, child := range command.Commands {
			visit(child)
		}
	}
	visit(root)
}
