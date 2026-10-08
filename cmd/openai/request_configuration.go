package main

import (
	"context"
	"errors"
	"net/url"
	"os"

	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

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
