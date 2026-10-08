package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

func main() {
	if handled, err := terminalimage.RunKittyOutputHelper(os.Args); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "Image output helper failed.")
			os.Exit(1)
		}
		return
	}
	if handled, err := custom.RunBatchOutputHelper(os.Args); handled {
		if err != nil {
			os.Exit(1)
		}
		return
	}
	app := cmd.Command
	app.Flags = append(app.Flags, cmd.NewRequestHeaderFlag())

	completing := len(os.Args) > 1 && os.Args[1] == "__complete"
	if completing {
		prepareForAutocomplete(app)
	}

	args, _, err := custom.ConfigureHelp(app, os.Args)
	custom.ConfigureCommandErrors(app)

	ctx := context.Background()
	if err == nil {
		custom.SetupImagePickerShellOnFirstRun(ctx, os.Args)
		err = runWithRequestConfiguration(ctx, app, args, completing)
	}
	if err != nil {
		exitCode := 1

		// Preserve custom exit codes through command-context wrappers.
		var exitErr cli.ExitCoder
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if showErr := custom.ShowCommandError(app, err, os.Stderr); showErr != nil {
			_ = custom.ShowCommandErrorFallback(err, showErr, os.Stderr)
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

// runWithRequestConfiguration confines SDK environment normalization to one CLI invocation.
// Helper and pager children inherit the effective URL; the parent shell is unchanged.
func runWithRequestConfiguration(ctx context.Context, app *cli.Command, args []string, completing bool) (err error) {
	baseURL, baseURLSet := os.LookupEnv("OPENAI_BASE_URL")
	restoreBaseURL := false
	requestSetup := app.Before
	app.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
		// Inspect parsed command selection, never arbitrary flag values.
		if completing || command.Args().First() == "@completion" || command.Args().First() == "help" {
			return ctx, nil
		}
		if explicitURL := command.Root().String("base-url"); explicitURL != "" {
			// The SDK parses environment defaults before applying explicit options.
			if _, parseErr := url.Parse(baseURL); baseURLSet && parseErr != nil {
				if err := os.Setenv("OPENAI_BASE_URL", explicitURL); err != nil {
					return ctx, err
				}
				restoreBaseURL = true
			}
		} else if baseURLSet {
			if err := custom.ValidateBaseURL(baseURL, "OPENAI_BASE_URL"); err != nil {
				return ctx, err
			}
		}
		if requestSetup != nil {
			return requestSetup(ctx, command)
		}
		return ctx, nil
	}
	defer func() {
		app.Before = requestSetup
		if restoreBaseURL {
			if restoreErr := os.Setenv("OPENAI_BASE_URL", baseURL); restoreErr != nil {
				err = errors.Join(err, restoreErr)
			}
		}
	}()
	return app.Run(ctx, args)
}
