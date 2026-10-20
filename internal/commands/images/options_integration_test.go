package images_test

import (
	"context"
	"strings"

	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

func imageErrorMessage(command *cli.Command, failure error) string {
	if command == nil {
		command = &cli.Command{}
	}
	var out strings.Builder
	if err := custom.ShowCommandError(command, failure, &out); err != nil {
		panic(err)
	}
	return strings.TrimSpace(out.String())
}

func imageWrapError(command *cli.Command, failure error) error {
	custom.ConfigureCommandErrors(command)
	return command.OnUsageError(context.Background(), command, failure, false)
}
