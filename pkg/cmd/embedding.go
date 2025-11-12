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

var embeddingsCreate = cli.Command{
	Name:    "create",
	Usage:   "Creates an embedding vector representing the input text.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[any]{
			Name:     "input",
			Usage:    "Input text to embed, encoded as a string or array of tokens. To embed multiple inputs in a single request, pass an array of strings or array of token arrays. The input must not exceed the max input tokens for the model (8192 tokens for all embedding models), cannot be an empty string, and any array must be 2048 dimensions or less. [Example Python code](https://cookbook.openai.com/examples/how_to_count_tokens_with_tiktoken) for counting tokens. In addition to the per-input token limit, all embedding  models enforce a maximum of 300,000 tokens summed across all inputs in a  single request.\n",
			Required: true,
			BodyPath: "input",
		},
		&requestflag.Flag[string]{
			Name:     "model",
			Usage:    "ID of the model to use. You can use the [List models](https://platform.openai.com/docs/api-reference/models/list) API to see all of your available models, or see our [Model overview](https://platform.openai.com/docs/models) for descriptions of them.\n",
			Required: true,
			BodyPath: "model",
		},
		&requestflag.Flag[int64]{
			Name:     "dimensions",
			Usage:    "The number of dimensions the resulting output embeddings should have. Only supported in `text-embedding-3` and later models.\n",
			BodyPath: "dimensions",
		},
		&requestflag.Flag[string]{
			Name:     "encoding-format",
			Usage:    "The format to return the embeddings in. Can be either `float` or [`base64`](https://pypi.org/project/pybase64/).",
			Default:  "float",
			BodyPath: "encoding_format",
		},
		&requestflag.Flag[string]{
			Name:     "user",
			Usage:    "A unique identifier representing your end-user, which can help OpenAI to monitor and detect abuse. [Learn more](https://platform.openai.com/docs/guides/safety-best-practices#end-user-ids).\n",
			BodyPath: "user",
		},
	},
	Action:          handleEmbeddingsCreate,
	HideHelpCommand: true,
}

func handleEmbeddingsCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.EmbeddingNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Embeddings.New(ctx, params, options...)
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
		Title:          "embeddings create",
		Transform:      transform,
	})
}
