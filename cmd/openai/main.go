package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

func main() {
	app := cmd.Command
	app.Flags = append(app.Flags, cmd.NewRequestHeaderFlag())

	if len(os.Args) > 1 && os.Args[1] == "__complete" {
		prepareForAutocomplete(app)
	}

	// Request configuration must not prevent local help from opening.
	requestSetup := app.Before
	app.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
		if baseURL, ok := os.LookupEnv("OPENAI_BASE_URL"); ok {
			if err := cmd.ValidateBaseURL(baseURL, "OPENAI_BASE_URL"); err != nil {
				return ctx, err
			}
		}
		if requestSetup != nil {
			return requestSetup(ctx, command)
		}
		return ctx, nil
	}
	args, _, err := custom.ConfigureHelp(app, os.Args)
	custom.ConfigureCommandErrors(app)

	ctx := context.Background()
	if err == nil {
		err = app.Run(ctx, args)
	}
	if err != nil {
		exitCode := 1

		// Preserve custom exit codes through command-context wrappers.
		var exitErr cli.ExitCoder
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if showErr := custom.ShowCommandError(app, err, os.Stderr); showErr != nil {
			fmt.Fprintln(os.Stderr, "Could not display the error.")
		}
		os.Exit(exitCode)
	}
}

func prepareForAutocomplete(cmd *cli.Command) {
	// urfave/cli does not handle flag completions and will print an error if we inspect a command with invalid flags.
	// This skips that sort of validation
	cmd.SkipFlagParsing = true
	for _, child := range cmd.Commands {
		prepareForAutocomplete(child)
	}
}
