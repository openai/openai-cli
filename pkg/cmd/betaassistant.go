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

var betaAssistantsCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Create an assistant with a model and instructions.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "model",
			Usage:    "ID of the model to use. You can use the [List models](https://platform.openai.com/docs/api-reference/models/list) API to see all of your available models, or see our [Model overview](https://platform.openai.com/docs/models) for descriptions of them.\n",
			Required: true,
			BodyPath: "model",
		},
		&requestflag.Flag[*string]{
			Name:     "description",
			Usage:    "The description of the assistant. The maximum length is 512 characters.\n",
			BodyPath: "description",
		},
		&requestflag.Flag[*string]{
			Name:     "instructions",
			Usage:    "The system instructions that the assistant uses. The maximum length is 256,000 characters.\n",
			BodyPath: "instructions",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "metadata",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			BodyPath: "metadata",
		},
		&requestflag.Flag[*string]{
			Name:     "name",
			Usage:    "The name of the assistant. The maximum length is 256 characters.\n",
			BodyPath: "name",
		},
		&requestflag.Flag[*string]{
			Name:     "reasoning-effort",
			Usage:    "Constrains effort on reasoning for reasoning models. Currently supported\nvalues are `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`.\nReducing reasoning effort can result in faster responses and fewer tokens\nused on reasoning in a response. Not all reasoning models support every\nvalue. See the\n[reasoning guide](https://platform.openai.com/docs/guides/reasoning)\nfor model-specific support.\n",
			Default:  requestflag.Ptr[string]("medium"),
			BodyPath: "reasoning_effort",
		},
		&requestflag.Flag[any]{
			Name:     "response-format",
			Usage:    "Specifies the format that the model must output. Compatible with [GPT-4o](https://platform.openai.com/docs/models#gpt-4o), [GPT-4 Turbo](https://platform.openai.com/docs/models#gpt-4-turbo-and-gpt-4), and all GPT-3.5 Turbo models since `gpt-3.5-turbo-1106`.\n\nSetting to `{ \"type\": \"json_schema\", \"json_schema\": {...} }` enables Structured Outputs which ensures the model will match your supplied JSON schema. Learn more in the [Structured Outputs guide](https://platform.openai.com/docs/guides/structured-outputs).\n\nSetting to `{ \"type\": \"json_object\" }` enables JSON mode, which ensures the message the model generates is valid JSON.\n\n**Important:** when using JSON mode, you **must** also instruct the model to produce JSON yourself via a system or user message. Without this, the model may generate an unending stream of whitespace until the generation reaches the token limit, resulting in a long-running and seemingly \"stuck\" request. Also note that the message content may be partially cut off if `finish_reason=\"length\"`, which indicates the generation exceeded `max_tokens` or the conversation exceeded the max context length.\n",
			BodyPath: "response_format",
		},
		&requestflag.Flag[*float64]{
			Name:     "temperature",
			Usage:    "What sampling temperature to use, between 0 and 2. Higher values like 0.8 will make the output more random, while lower values like 0.2 will make it more focused and deterministic.\n",
			Default:  requestflag.Ptr[float64](1),
			BodyPath: "temperature",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "tool-resources",
			Usage:    "A set of resources that are used by the assistant's tools. The resources are specific to the type of tool. For example, the `code_interpreter` tool requires a list of file IDs, while the `file_search` tool requires a list of vector store IDs.\n",
			BodyPath: "tool_resources",
		},
		&requestflag.Flag[[]map[string]any]{
			Name:     "tool",
			Usage:    "A list of tool enabled on the assistant. There can be a maximum of 128 tools per assistant. Tools can be of types `code_interpreter`, `file_search`, or `function`.\n",
			Default:  []map[string]any{},
			BodyPath: "tools",
		},
		&requestflag.Flag[*float64]{
			Name:     "top-p",
			Usage:    "An alternative to sampling with temperature, called nucleus sampling, where the model considers the results of the tokens with top_p probability mass. So 0.1 means only the tokens comprising the top 10% probability mass are considered.\n\nWe generally recommend altering this or temperature but not both.\n",
			Default:  requestflag.Ptr[float64](1),
			BodyPath: "top_p",
		},
	},
	Action:          handleBetaAssistantsCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"tool-resources": {
		&requestflag.InnerFlag[map[string]any]{
			Name:       "tool-resources.code-interpreter",
			InnerField: "code_interpreter",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "tool-resources.file-search",
			InnerField: "file_search",
		},
	},
})

var betaAssistantsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves an assistant.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "assistant-id",
			Required:  true,
			PathParam: "assistant_id",
		},
	},
	Action:          handleBetaAssistantsRetrieve,
	HideHelpCommand: true,
}

var betaAssistantsUpdate = requestflag.WithInnerFlags(cli.Command{
	Name:    "update",
	Usage:   "Modifies an assistant.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "assistant-id",
			Required:  true,
			PathParam: "assistant_id",
		},
		&requestflag.Flag[*string]{
			Name:     "description",
			Usage:    "The description of the assistant. The maximum length is 512 characters.\n",
			BodyPath: "description",
		},
		&requestflag.Flag[*string]{
			Name:     "instructions",
			Usage:    "The system instructions that the assistant uses. The maximum length is 256,000 characters.\n",
			BodyPath: "instructions",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "metadata",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			BodyPath: "metadata",
		},
		&requestflag.Flag[string]{
			Name:     "model",
			Usage:    "ID of the model to use. You can use the [List models](https://platform.openai.com/docs/api-reference/models/list) API to see all of your available models, or see our [Model overview](https://platform.openai.com/docs/models) for descriptions of them.\n",
			BodyPath: "model",
		},
		&requestflag.Flag[*string]{
			Name:     "name",
			Usage:    "The name of the assistant. The maximum length is 256 characters.\n",
			BodyPath: "name",
		},
		&requestflag.Flag[*string]{
			Name:     "reasoning-effort",
			Usage:    "Constrains effort on reasoning for reasoning models. Currently supported\nvalues are `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`.\nReducing reasoning effort can result in faster responses and fewer tokens\nused on reasoning in a response. Not all reasoning models support every\nvalue. See the\n[reasoning guide](https://platform.openai.com/docs/guides/reasoning)\nfor model-specific support.\n",
			Default:  requestflag.Ptr[string]("medium"),
			BodyPath: "reasoning_effort",
		},
		&requestflag.Flag[any]{
			Name:     "response-format",
			Usage:    "Specifies the format that the model must output. Compatible with [GPT-4o](https://platform.openai.com/docs/models#gpt-4o), [GPT-4 Turbo](https://platform.openai.com/docs/models#gpt-4-turbo-and-gpt-4), and all GPT-3.5 Turbo models since `gpt-3.5-turbo-1106`.\n\nSetting to `{ \"type\": \"json_schema\", \"json_schema\": {...} }` enables Structured Outputs which ensures the model will match your supplied JSON schema. Learn more in the [Structured Outputs guide](https://platform.openai.com/docs/guides/structured-outputs).\n\nSetting to `{ \"type\": \"json_object\" }` enables JSON mode, which ensures the message the model generates is valid JSON.\n\n**Important:** when using JSON mode, you **must** also instruct the model to produce JSON yourself via a system or user message. Without this, the model may generate an unending stream of whitespace until the generation reaches the token limit, resulting in a long-running and seemingly \"stuck\" request. Also note that the message content may be partially cut off if `finish_reason=\"length\"`, which indicates the generation exceeded `max_tokens` or the conversation exceeded the max context length.\n",
			BodyPath: "response_format",
		},
		&requestflag.Flag[*float64]{
			Name:     "temperature",
			Usage:    "What sampling temperature to use, between 0 and 2. Higher values like 0.8 will make the output more random, while lower values like 0.2 will make it more focused and deterministic.\n",
			Default:  requestflag.Ptr[float64](1),
			BodyPath: "temperature",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "tool-resources",
			Usage:    "A set of resources that are used by the assistant's tools. The resources are specific to the type of tool. For example, the `code_interpreter` tool requires a list of file IDs, while the `file_search` tool requires a list of vector store IDs.\n",
			BodyPath: "tool_resources",
		},
		&requestflag.Flag[[]map[string]any]{
			Name:     "tool",
			Usage:    "A list of tool enabled on the assistant. There can be a maximum of 128 tools per assistant. Tools can be of types `code_interpreter`, `file_search`, or `function`.\n",
			Default:  []map[string]any{},
			BodyPath: "tools",
		},
		&requestflag.Flag[*float64]{
			Name:     "top-p",
			Usage:    "An alternative to sampling with temperature, called nucleus sampling, where the model considers the results of the tokens with top_p probability mass. So 0.1 means only the tokens comprising the top 10% probability mass are considered.\n\nWe generally recommend altering this or temperature but not both.\n",
			Default:  requestflag.Ptr[float64](1),
			BodyPath: "top_p",
		},
	},
	Action:          handleBetaAssistantsUpdate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"tool-resources": {
		&requestflag.InnerFlag[map[string]any]{
			Name:       "tool-resources.code-interpreter",
			InnerField: "code_interpreter",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "tool-resources.file-search",
			InnerField: "file_search",
		},
	},
})

var betaAssistantsList = cli.Command{
	Name:    "list",
	Usage:   "Returns a list of assistants.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "A cursor for use in pagination. `after` is an object ID that defines your place in the list. For instance, if you make a list request and receive 100 objects, ending with obj_foo, your subsequent call can include after=obj_foo in order to fetch the next page of the list.\n",
			QueryPath: "after",
		},
		&requestflag.Flag[string]{
			Name:      "before",
			Usage:     "A cursor for use in pagination. `before` is an object ID that defines your place in the list. For instance, if you make a list request and receive 100 objects, starting with obj_foo, your subsequent call can include before=obj_foo in order to fetch the previous page of the list.\n",
			QueryPath: "before",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of objects to be returned. Limit can range between 1 and 100, and the default is 20.\n",
			Default:   20,
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     "Sort order by the `created_at` timestamp of the objects. `asc` for ascending order and `desc` for descending order.\n",
			Default:   "desc",
			QueryPath: "order",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleBetaAssistantsList,
	HideHelpCommand: true,
}

var betaAssistantsDelete = cli.Command{
	Name:    "delete",
	Usage:   "Delete an assistant.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "assistant-id",
			Required:  true,
			PathParam: "assistant_id",
		},
	},
	Action:          handleBetaAssistantsDelete,
	HideHelpCommand: true,
}

func handleBetaAssistantsCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.BetaAssistantNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Beta.Assistants.New(ctx, params, options...)
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
		Title:          "beta:assistants create",
		Transform:      transform,
	})
}

func handleBetaAssistantsRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("assistant-id") && len(unusedArgs) > 0 {
		cmd.Set("assistant-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Beta.Assistants.Get(ctx, cmd.Value("assistant-id").(string), options...)
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
		Title:          "beta:assistants retrieve",
		Transform:      transform,
	})
}

func handleBetaAssistantsUpdate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("assistant-id") && len(unusedArgs) > 0 {
		cmd.Set("assistant-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
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

	params := openai.BetaAssistantUpdateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Beta.Assistants.Update(
		ctx,
		cmd.Value("assistant-id").(string),
		params,
		options...,
	)
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
		Title:          "beta:assistants update",
		Transform:      transform,
	})
}

func handleBetaAssistantsList(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	params := openai.BetaAssistantListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Beta.Assistants.List(ctx, params, options...)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:assistants list",
			Transform:      transform,
		})
	} else {
		iter := client.Beta.Assistants.ListAutoPaging(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:assistants list",
			Transform:      transform,
		})
	}
}

func handleBetaAssistantsDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("assistant-id") && len(unusedArgs) > 0 {
		cmd.Set("assistant-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Beta.Assistants.Delete(ctx, cmd.Value("assistant-id").(string), options...)
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
		Title:          "beta:assistants delete",
		Transform:      transform,
	})
}
