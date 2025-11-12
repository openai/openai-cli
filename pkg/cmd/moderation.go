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

var moderationsCreate = cli.Command{
	Name:    "create",
	Usage:   "Classifies if text and/or image inputs are potentially harmful. Learn more in\nthe [moderation guide](https://platform.openai.com/docs/guides/moderation).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[any]{
			Name:     "input",
			Usage:    "Input (or inputs) to classify. Can be a single string, an array of strings, or\nan array of multi-modal input objects similar to other models.\n",
			Required: true,
			BodyPath: "input",
		},
		&requestflag.Flag[string]{
			Name:     "model",
			Usage:    "The content moderation model you would like to use. Learn more in\n[the moderation guide](https://platform.openai.com/docs/guides/moderation), and learn about\navailable models [here](https://platform.openai.com/docs/models#moderation).\n",
			BodyPath: "model",
		},
	},
	Action:          handleModerationsCreate,
	HideHelpCommand: true,
}

func handleModerationsCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.ModerationNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Moderations.New(ctx, params, options...)
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
		Title:          "moderations create",
		Transform:      transform,
	})
}
