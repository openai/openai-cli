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

var betaResponsesInputTokensCount = requestflag.WithInnerFlags(cli.Command{
	Name:    "count",
	Usage:   "Returns input token counts of the request.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[any]{
			Name:     "conversation",
			Usage:    "The conversation that this response belongs to. Items from this conversation are prepended to `input_items` for this response request.\nInput items and output items from this response are automatically added to this conversation after this response completes.\n",
			BodyPath: "conversation",
		},
		&requestflag.Flag[any]{
			Name:     "input",
			Usage:    "Text, image, or file inputs to the model, used to generate a response",
			BodyPath: "input",
		},
		&requestflag.Flag[*string]{
			Name:     "instructions",
			Usage:    "A system (or developer) message inserted into the model's context.\nWhen used along with `previous_response_id`, the instructions from a previous response will not be carried over to the next response. This makes it simple to swap out system (or developer) messages in new responses.",
			BodyPath: "instructions",
		},
		&requestflag.Flag[*string]{
			Name:     "model",
			Usage:    "Model ID used to generate the response, like `gpt-4o` or `o3`. OpenAI offers a wide range of models with different capabilities, performance characteristics, and price points. Refer to the [model guide](https://platform.openai.com/docs/models) to browse and compare available models.",
			BodyPath: "model",
		},
		&requestflag.Flag[*bool]{
			Name:     "parallel-tool-calls",
			Usage:    "Whether to allow the model to run tool calls in parallel.",
			BodyPath: "parallel_tool_calls",
		},
		&requestflag.Flag[string]{
			Name:     "personality",
			Usage:    "A model-owned style preset to apply to this request. Omit this parameter to use the model's default style. Supported values may expand over time. Values must be at most 64 characters.",
			BodyPath: "personality",
		},
		&requestflag.Flag[*string]{
			Name:     "previous-response-id",
			Usage:    "The unique ID of the previous response to the model. Use this to create multi-turn conversations. Learn more about [conversation state](https://platform.openai.com/docs/guides/conversation-state). Cannot be used in conjunction with `conversation`.",
			BodyPath: "previous_response_id",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "reasoning",
			Usage:    "**gpt-5 and o-series models only** Configuration options for [reasoning models](https://platform.openai.com/docs/guides/reasoning).",
			BodyPath: "reasoning",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "text",
			Usage:    "Configuration options for a text response from the model. Can be plain\ntext or structured JSON data. Learn more:\n- [Text inputs and outputs](https://platform.openai.com/docs/guides/text)\n- [Structured Outputs](https://platform.openai.com/docs/guides/structured-outputs)\n",
			BodyPath: "text",
		},
		&requestflag.Flag[any]{
			Name:     "tool-choice",
			Usage:    "Controls which tool the model should use, if any.",
			BodyPath: "tool_choice",
		},
		&requestflag.Flag[any]{
			Name:     "tool",
			Usage:    "An array of tools the model may call while generating a response. You can specify which tool to use by setting the `tool_choice` parameter.",
			BodyPath: "tools",
		},
		&requestflag.Flag[string]{
			Name:     "truncation",
			Usage:    "The truncation strategy to use for the model response. - `auto`: If the input to this Response exceeds the model's context window size, the model will truncate the response to fit the context window by dropping items from the beginning of the conversation. - `disabled` (default): If the input size will exceed the context window size for a model, the request will fail with a 400 error.",
			BodyPath: "truncation",
		},
		&requestflag.Flag[[]string]{
			Name:       "beta",
			HeaderPath: "openai-beta",
		},
	},
	Action:          handleBetaResponsesInputTokensCount,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"reasoning": {
		&requestflag.InnerFlag[*string]{
			Name:       "reasoning.context",
			Usage:      "Controls which reasoning items are rendered back to the model on later turns.\nIf omitted or set to `auto`, the model determines the context mode. The\n`gpt-5.6` model family defaults to `all_turns`; earlier models default to\n`current_turn`.\n\nWhen returned on a response, this is the effective reasoning context mode\nused for the response.\n",
			InnerField: "context",
		},
		&requestflag.InnerFlag[*string]{
			Name:       "reasoning.effort",
			Usage:      "Constrains effort on reasoning for reasoning models. Currently supported\nvalues are `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`.\nReducing reasoning effort can result in faster responses and fewer tokens\nused on reasoning in a response. Not all reasoning models support every\nvalue. See the\n[reasoning guide](https://platform.openai.com/docs/guides/reasoning)\nfor model-specific support.\n",
			InnerField: "effort",
		},
		&requestflag.InnerFlag[*string]{
			Name:       "reasoning.generate-summary",
			Usage:      "**Deprecated:** use `summary` instead.\n\nA summary of the reasoning performed by the model. This can be\nuseful for debugging and understanding the model's reasoning process.\nOne of `auto`, `concise`, or `detailed`.\n",
			InnerField: "generate_summary",
		},
		&requestflag.InnerFlag[string]{
			Name:       "reasoning.mode",
			Usage:      "Controls the reasoning execution mode for the request.\n\nWhen returned on a response, this is the effective execution mode.\n",
			InnerField: "mode",
		},
		&requestflag.InnerFlag[*string]{
			Name:       "reasoning.summary",
			Usage:      "A summary of the reasoning performed by the model. This can be\nuseful for debugging and understanding the model's reasoning process.\nOne of `auto`, `concise`, or `detailed`.\n\n`concise` is supported for `computer-use-preview` models and all reasoning models after `gpt-5`.\n",
			InnerField: "summary",
		},
	},
	"text": {
		&requestflag.InnerFlag[map[string]any]{
			Name:       "text.format",
			Usage:      "An object specifying the format that the model must output.\n\nConfiguring `{ \"type\": \"json_schema\" }` enables Structured Outputs, \nwhich ensures the model will match your supplied JSON schema. Learn more in the \n[Structured Outputs guide](https://platform.openai.com/docs/guides/structured-outputs).\n\nThe default format is `{ \"type\": \"text\" }` with no additional options.\n\n**Not recommended for gpt-4o and newer models:**\n\nSetting to `{ \"type\": \"json_object\" }` enables the older JSON mode, which\nensures the message the model generates is valid JSON. Using `json_schema`\nis preferred for models that support it.\n",
			InnerField: "format",
		},
		&requestflag.InnerFlag[*string]{
			Name:       "text.verbosity",
			Usage:      "Constrains the verbosity of the model's response. Lower values will result in\nmore concise responses, while higher values will result in more verbose responses.\nCurrently supported values are `low`, `medium`, and `high`. The default is\n`medium`.\n",
			InnerField: "verbosity",
		},
	},
})

func handleBetaResponsesInputTokensCount(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.BetaResponseInputTokenCountParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Beta.Responses.InputTokens.Count(ctx, params, options...)
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
		Title:          "beta:responses:input-tokens count",
		Transform:      transform,
	})
}
