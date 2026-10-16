// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package commands

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

var decisionsCreate = cli.Command{
	Name:    "create",
	Usage:   "Use this endpoint to quickly answer classification or scoring questions about\nsome input. You can ask multiple questions in one request.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[any]{
			Name:     "input",
			Usage:    "The text or images to evaluate for every question. Provide a text string or user messages containing text and images. Images can be base64 data URLs or publicly accessible HTTP(S) URLs; at most 128 images are allowed across all messages in one request. Files, audio, tools, and item references are not supported.",
			Required: true,
			BodyPath: "input",
		},
		&requestflag.Flag[string]{
			Name:     "model",
			Usage:    "The model to use. Only `gpt-6-luna` is supported.",
			Required: true,
			BodyPath: "model",
		},
		&requestflag.Flag[[]map[string]any]{
			Name:     "question",
			Usage:    "Ask 1–200 questions about the input. Each question can be a predicate, choice, or score. Answers come back in the same order.",
			Required: true,
			BodyPath: "questions",
		},
		&requestflag.Flag[*string]{
			Name:     "safety-identifier",
			Usage:    "Opaque caller-provided end-user identifier, scoped by the verified org. Match Responses' limit; this is never the authenticated user identity.",
			BodyPath: "safety_identifier",
		},
	},
	Action:          handleDecisionsCreate,
	HideHelpCommand: true,
}

func handleDecisionsCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.DecisionNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Decisions.New(ctx, params, options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) decisions > (method) create",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "decisions create",
		Transform:      transform,
	})
}
