// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

var fineTuningAlphaGradersRun = cli.Command{
	Name:    "run",
	Usage:   "Run a grader.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[map[string]any]{
			Name:     "grader",
			Usage:    "The grader used for the fine-tuning job.",
			Required: true,
			BodyPath: "grader",
		},
		&requestflag.Flag[string]{
			Name:     "model-sample",
			Usage:    "The model sample to be evaluated. This value will be used to populate \nthe `sample` namespace. See [the guide](https://platform.openai.com/docs/guides/graders) for more details.\nThe `output_json` variable will be populated if the model sample is a \nvalid JSON string.\n \n",
			Required: true,
			BodyPath: "model_sample",
		},
		&requestflag.Flag[any]{
			Name:     "item",
			Usage:    "The dataset item provided to the grader. This will be used to populate \nthe `item` namespace. See [the guide](https://platform.openai.com/docs/guides/graders) for more details. \n",
			BodyPath: "item",
		},
	},
	Action:          handleFineTuningAlphaGradersRun,
	HideHelpCommand: true,
}

var fineTuningAlphaGradersValidate = cli.Command{
	Name:    "validate",
	Usage:   "Validate a grader.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[map[string]any]{
			Name:     "grader",
			Usage:    "The grader used for the fine-tuning job.",
			Required: true,
			BodyPath: "grader",
		},
	},
	Action:          handleFineTuningAlphaGradersValidate,
	HideHelpCommand: true,
}

func handleFineTuningAlphaGradersRun(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		ApplicationJSON,
		false,
	)
	if err != nil {
		return err
	}

	params := openai.FineTuningAlphaGraderRunParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.FineTuning.Alpha.Graders.Run(ctx, params, options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "fine-tuning:alpha:graders run",
		Transform:      transform,
	})
}

func handleFineTuningAlphaGradersValidate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		ApplicationJSON,
		false,
	)
	if err != nil {
		return err
	}

	params := openai.FineTuningAlphaGraderValidateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.FineTuning.Alpha.Graders.Validate(ctx, params, options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "fine-tuning:alpha:graders validate",
		Transform:      transform,
	})
}
