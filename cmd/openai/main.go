// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"

	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func main() {
	app := cmd.Command

	if slices.Contains(os.Args, "__complete") {
		prepareForAutocomplete(app)
	}

	if baseURL, ok := os.LookupEnv("OPENAI_BASE_URL"); ok {
		if err := cmd.ValidateBaseURL(baseURL, "OPENAI_BASE_URL"); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err.Error())
			os.Exit(1)
		}
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
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
