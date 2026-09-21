package custom

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Run executes the CLI lifecycle for a generated command tree and returns its
// process exit code. The executable supplies the tree; custom behavior never
// imports the generated command package.
func Run(app *cli.Command, argv []string) int {
	app.Flags = append(app.Flags, NewRequestHeaderFlag())
	requestSetup := app.Before
	app.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
		if baseURL, ok := os.LookupEnv("OPENAI_BASE_URL"); ok {
			if err := ValidateBaseURL(baseURL, "OPENAI_BASE_URL"); err != nil {
				return ctx, err
			}
		}
		if requestSetup != nil {
			return requestSetup(ctx, command)
		}
		return ctx, nil
	}
	args, _, err := ConfigureHelp(app, argv)
	if err != nil {
		fmt.Fprintln(os.Stderr, readable.Text(err.Error()))
		return commandExitCode(err)
	}

	if len(argv) > 1 && argv[1] == "__complete" {
		prepareForAutocomplete(app)
	}

	ctx := context.Background()
	if err := app.Run(ctx, args); err != nil {
		exitCode := commandExitCode(err)
		if ShowFriendlyImageError(app, err, os.Stderr) {
			return exitCode
		}
		if showReadableError(app, err, os.Stderr) {
			return exitCode
		}

		var apierr *openai.Error
		if errors.As(err, &apierr) {
			showErr := ShowJSON(gjson.Parse(apierr.RawJSON()), ShowJSONOpts{
				// Errors must not enter successful-operation transformers.
				Context:        ctx,
				Operation:      "",
				OutputKind:     OutputUnspecified,
				ExplicitFormat: app.IsSet("format-error"),
				Format:         errorOutputFormat(app),
				Stdout:         os.Stderr,
				Title:          "Error",
				Transform:      app.String("transform-error"),
			})
			if showErr != nil {
				fmt.Fprintln(os.Stderr, "Could not display the API error:", readable.Text(showErr.Error()))
			}
		} else if buffer, ok := app.ErrWriter.(interface {
			Len() int
			Bytes() []byte
		}); ok && buffer.Len() > 0 {
			_, _ = os.Stderr.Write([]byte(readable.Text(string(buffer.Bytes()))))
		} else {
			fmt.Fprintln(os.Stderr, readable.Text(err.Error()))
		}
		return exitCode
	}
	return 0
}

func commandExitCode(err error) int {
	if exitErr, ok := err.(cli.ExitCoder); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func prepareForAutocomplete(command *cli.Command) {
	// Completion inspects incomplete flags, which normal parsing would reject.
	command.SkipFlagParsing = true
	for _, child := range command.Commands {
		prepareForAutocomplete(child)
	}
}
