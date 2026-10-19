package images_test

import (
	"context"
	"strings"

	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func assemblyForTest() images.AssemblyForTest {
	return images.AssemblyForTest{
		Configure: custom.ConfigureCommand,
		ShowError: custom.ShowCommandError,
		ErrorMessage: func(command *cli.Command, failure error) string {
			if command == nil {
				command = &cli.Command{}
			}
			var out strings.Builder
			if err := custom.ShowCommandError(command, failure, &out); err != nil {
				panic(err)
			}
			return strings.TrimSpace(out.String())
		},
		WrapError: func(command *cli.Command, failure error) error {
			// Exercise the actual usage-error wrapper without changing the parsed action.
			custom.ConfigureCommandErrors(command)
			return command.OnUsageError(context.Background(), command, failure, false)
		},
		ShowJSON: func(value gjson.Result, opts images.PresentationForTest) error {
			return custom.ShowJSON(value, custom.ShowJSONOpts(opts))
		},
		ShowJSONIterator: func(iter jsonview.Iterator[clioutput.JSON], count int64, opts images.PresentationForTest) error {
			return custom.ShowJSONIterator(iter, count, custom.ShowJSONOpts(opts))
		},
		Saving: custom.ImageSavingOptions(), Models: custom.ImageModelOptions(), Preview: custom.ImagePreviewOptions(),
	}
}
