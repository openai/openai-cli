// Package app owns the CLI invocation lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/openai/openai-cli/generated/commands"
	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

// Run executes one CLI process invocation and returns its exit status.
// The generated command tree is process-scoped; Run is called once per process.
func Run(ctx context.Context, argv []string) int {
	if handled, err := terminalimage.RunKittyOutputHelper(argv); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "Image output helper failed.")
			return 1
		}
		return 0
	}
	if handled, err := batchcommands.RunBatchOutputHelper(argv); handled {
		if err != nil {
			return 1
		}
		return 0
	}
	if handled, err := batchcommands.RunBatchDirectoryHelper(argv); handled {
		if err != nil {
			return 1
		}
		return 0
	}
	app := commands.Command
	custom.ConfigureCommand(app)
	app.Flags = append(app.Flags, requestconfig.NewRequestHeaderFlag())

	completing := len(argv) > 1 && argv[1] == "__complete"
	if completing {
		prepareForAutocomplete(app)
	}

	args, _, err := ConfigureHelp(app, argv)
	custom.ConfigureCommandErrors(app)

	if err == nil {
		custom.SetupImagePickerShellOnFirstRun(ctx, argv, app)
		err = custom.RunWithOutputPolicy(ctx, app, func(ctx context.Context) error {
			return runWithRequestConfiguration(ctx, app, args, completing)
		})
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
		return exitCode
	}
	return 0
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
		localUtility := custom.IsLocalUtilityCommand(command)
		if explicitURL := command.Root().String("base-url"); !localUtility && explicitURL != "" {
			// The SDK parses environment defaults before applying explicit options.
			if _, parseErr := url.Parse(baseURL); baseURLSet && parseErr != nil {
				if err := os.Setenv("OPENAI_BASE_URL", explicitURL); err != nil {
					return ctx, err
				}
				restoreBaseURL = true
			}
		} else if !localUtility && baseURLSet {
			if err := requestconfig.ValidateBaseURL(baseURL, "OPENAI_BASE_URL"); err != nil {
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
