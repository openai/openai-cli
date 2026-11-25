package app

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

// commandError retains only the command needed to map API parameter paths to
// locally declared flags. It never copies flag values or changes the cause.
type commandError struct {
	command *cli.Command
	err     error
	missing string
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
				missing := requiredUsageMessage(command, err)
				if previousUsage != nil {
					original := err
					err = previousUsage(ctx, command, err, subcommand)
					if !errors.Is(err, original) {
						missing = ""
					}
				}
				// The default handler also prints help to stdout. Return the failure
				// instead so the error presenter owns all diagnostic output.
				if missing != "" {
					return &commandError{command: command, err: err, missing: missing}
				}
				return withCommandError(command, err)
			}
		}
		for _, child := range command.Commands {
			visit(child)
		}
	}
	visit(root)
}

// urfave keeps its required-input errors private. Interpret their diagnostic
// grammar at the usage hook and only use names from eligible declarations.
func requiredUsageMessage(command *cli.Command, err error) string {
	message := err.Error()
	for _, prefix := range []string{"Required flag ", "Required flags ", "Required argument ", "Required arguments "} {
		rest, ok := strings.CutPrefix(message, prefix)
		if !ok {
			continue
		}
		quoted, err := strconv.QuotedPrefix(rest)
		if err != nil || rest[len(quoted):] != " not set" {
			return ""
		}
		value, _ := strconv.Unquote(quoted)
		names := strings.Split(value, ", ")
		if strings.HasPrefix(prefix, "Required flag") {
			declared := parserErrorFlags(command)
			for _, name := range names {
				if !slices.ContainsFunc(declared, func(flag cli.Flag) bool {
					required, ok := flag.(cli.RequiredFlag)
					return ok && required.IsRequired() && slices.Contains(flag.Names(), name)
				}) {
					return ""
				}
			}
			return missingOptionsMessage(command, names)
		}
		for _, name := range names {
			if !slices.ContainsFunc(command.Arguments, func(arg cli.Argument) bool { return arg.HasName(name) }) {
				return ""
			}
		}
		return "Missing required arguments: " + strings.Join(names, ", ") + "."
	}
	return ""
}

func missingOptionsMessage(command *cli.Command, names []string) string {
	if len(names) == 0 {
		return ""
	}
	var flags []string
	for _, name := range names {
		flag := declaredErrorFlag(command, name)
		if flag == "" {
			return ""
		}
		flags = append(flags, flag)
	}
	return "Missing required options: " + strings.Join(flags, ", ") + "."
}

func missingRequiredInputMessage(command *cli.Command, err error) string {
	var missing *requestconfig.MissingRequiredFlagsError
	if errors.As(err, &missing) {
		return missingOptionsMessage(command, missing.Names)
	}
	var contextual *commandError
	if errors.As(err, &contextual) {
		return contextual.missing
	}
	return ""
}
