package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
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

	ctx := context.Background()
	if err == nil {
		err = app.Run(ctx, args)
	}
	if err != nil {
		exitCode := 1

		// Check if error has a custom exit code
		if exitErr, ok := err.(cli.ExitCoder); ok {
			exitCode = exitErr.ExitCode()
		}

		var apierr *openai.Error
		if errors.As(err, &apierr) {
			fmt.Fprintf(os.Stderr, "%s %q: %d %s\n", apierr.Request.Method, apierr.Request.URL, apierr.Response.StatusCode, http.StatusText(apierr.Response.StatusCode))
			format := app.String("format-error")
			json := gjson.Parse(apierr.RawJSON())
			show_err := cmd.ShowJSON(json, cmd.ShowJSONOpts{
				// Error output has no successful-operation transformer routing.
				Context:        ctx,
				Operation:      "",
				OutputKind:     custom.OutputUnspecified,
				ExplicitFormat: app.IsSet("format-error"),
				Format:         format,
				Title:          "Error",
				Transform:      app.String("transform-error"),
			})
			if show_err != nil {
				// Just print the original error:
				fmt.Fprintf(os.Stderr, "%s\n", err.Error())
			}
		} else {
			if cmd.CommandErrorBuffer.Len() > 0 {
				os.Stderr.Write(cmd.CommandErrorBuffer.Bytes())
			} else {
				fmt.Fprintf(os.Stderr, "%s\n", err.Error())
			}
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
