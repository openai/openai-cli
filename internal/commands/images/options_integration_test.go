package images_test

import (
	"context"
	"strings"

	appconfig "github.com/openai/openai-cli/internal/app"
	"github.com/urfave/cli/v3"
)

func imageErrorMessage(command *cli.Command, failure error) string {
	if command == nil {
		command = &cli.Command{}
	}
	var out strings.Builder
	if err := appconfig.ShowCommandError(command, failure, &out); err != nil {
		panic(err)
	}
	return strings.TrimSpace(out.String())
}

func imageWrapError(command *cli.Command, failure error) error {
	appconfig.ConfigureCommandErrors(command)
	return command.OnUsageError(context.Background(), command, failure, false)
}
