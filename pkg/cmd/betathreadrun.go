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

var betaThreadsRunsCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Create a run.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "thread-id",
			Required:  true,
			PathParam: "thread_id",
		},
		&requestflag.Flag[string]{
			Name:     "assistant-id",
			Usage:    "The ID of the [assistant](https://platform.openai.com/docs/api-reference/assistants) to use to execute this run.",
			Required: true,
			BodyPath: "assistant_id",
		},
		&requestflag.Flag[[]string]{
			Name:      "include",
			Usage:     "A list of additional fields to include in the response. Currently the only supported value is `step_details.tool_calls[*].file_search.results[*].content` to fetch the file search result content.\n\nSee the [file search tool documentation](https://platform.openai.com/docs/assistants/tools/file-search#customizing-file-search-settings) for more information.\n",
			QueryPath: "include",
		},
		&requestflag.Flag[*string]{
			Name:     "additional-instructions",
			Usage:    "Appends additional instructions at the end of the instructions for the run. This is useful for modifying the behavior on a per-run basis without overriding other instructions.",
			BodyPath: "additional_instructions",
		},
		&requestflag.Flag[any]{
			Name:     "additional-message",
			Usage:    "Adds additional messages to the thread before creating the run.",
			BodyPath: "additional_messages",
		},
		&requestflag.Flag[*string]{
			Name:     "instructions",
			Usage:    "Overrides the [instructions](https://platform.openai.com/docs/api-reference/assistants/createAssistant) of the assistant. This is useful for modifying the behavior on a per-run basis.",
			BodyPath: "instructions",
		},
		&requestflag.Flag[*int64]{
			Name:     "max-completion-tokens",
			Usage:    "The maximum number of completion tokens that may be used over the course of the run. The run will make a best effort to use only the number of completion tokens specified, across multiple turns of the run. If the run exceeds the number of completion tokens specified, the run will end with status `incomplete`. See `incomplete_details` for more info.\n",
			BodyPath: "max_completion_tokens",
		},
		&requestflag.Flag[*int64]{
			Name:     "max-prompt-tokens",
			Usage:    "The maximum number of prompt tokens that may be used over the course of the run. The run will make a best effort to use only the number of prompt tokens specified, across multiple turns of the run. If the run exceeds the number of prompt tokens specified, the run will end with status `incomplete`. See `incomplete_details` for more info.\n",
			BodyPath: "max_prompt_tokens",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "metadata",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			BodyPath: "metadata",
		},
		&requestflag.Flag[*string]{
			Name:     "model",
			Usage:    "The ID of the [Model](https://platform.openai.com/docs/api-reference/models) to be used to execute this run. If a value is provided here, it will override the model associated with the assistant. If not, the model associated with the assistant will be used.",
			BodyPath: "model",
		},
		&requestflag.Flag[bool]{
			Name:     "parallel-tool-calls",
			Usage:    "Whether to enable [parallel function calling](https://platform.openai.com/docs/guides/function-calling#configuring-parallel-function-calling) during tool use.",
			Default:  true,
			BodyPath: "parallel_tool_calls",
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
		&requestflag.Flag[*bool]{
			Name:     "stream",
			Usage:    "If `true`, returns a stream of events that happen during the Run as server-sent events, terminating when the Run enters a terminal state with a `data: [DONE]` message.\n",
			BodyPath: "stream",
		},
		&requestflag.Flag[*float64]{
			Name:     "temperature",
			Usage:    "What sampling temperature to use, between 0 and 2. Higher values like 0.8 will make the output more random, while lower values like 0.2 will make it more focused and deterministic.\n",
			Default:  requestflag.Ptr[float64](1),
			BodyPath: "temperature",
		},
		&requestflag.Flag[any]{
			Name:     "tool-choice",
			Usage:    "Controls which (if any) tool is called by the model.\n`none` means the model will not call any tools and instead generates a message.\n`auto` is the default value and means the model can pick between generating a message or calling one or more tools.\n`required` means the model must call one or more tools before responding to the user.\nSpecifying a particular tool like `{\"type\": \"file_search\"}` or `{\"type\": \"function\", \"function\": {\"name\": \"my_function\"}}` forces the model to call that tool.\n",
			BodyPath: "tool_choice",
		},
		&requestflag.Flag[any]{
			Name:     "tool",
			Usage:    "Override the tools the assistant can use for this run. This is useful for modifying the behavior on a per-run basis.",
			BodyPath: "tools",
		},
		&requestflag.Flag[*float64]{
			Name:     "top-p",
			Usage:    "An alternative to sampling with temperature, called nucleus sampling, where the model considers the results of the tokens with top_p probability mass. So 0.1 means only the tokens comprising the top 10% probability mass are considered.\n\nWe generally recommend altering this or temperature but not both.\n",
			Default:  requestflag.Ptr[float64](1),
			BodyPath: "top_p",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "truncation-strategy",
			Usage:    "Controls for how a thread will be truncated prior to the run. Use this to control the initial context window of the run.",
			BodyPath: "truncation_strategy",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleBetaThreadsRunsCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"additional-message": {
		&requestflag.InnerFlag[any]{
			Name:                  "additional-message.content",
			Usage:                 "The text contents of the message.",
			InnerField:            "content",
			OuterIsArrayOfObjects: true,
		},
		&requestflag.InnerFlag[string]{
			Name:                  "additional-message.role",
			Usage:                 "The role of the entity that is creating the message. Allowed values include:\n- `user`: Indicates the message is sent by an actual user and should be used in most cases to represent user-generated messages.\n- `assistant`: Indicates the message is generated by the assistant. Use this value to insert messages from the assistant into the conversation.\n",
			InnerField:            "role",
			OuterIsArrayOfObjects: true,
		},
		&requestflag.InnerFlag[any]{
			Name:                  "additional-message.attachments",
			Usage:                 "A list of files attached to the message, and the tools they should be added to.",
			InnerField:            "attachments",
			OuterIsArrayOfObjects: true,
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:                  "additional-message.metadata",
			Usage:                 "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			InnerField:            "metadata",
			OuterIsArrayOfObjects: true,
		},
	},
	"truncation-strategy": {
		&requestflag.InnerFlag[string]{
			Name:       "truncation-strategy.type",
			Usage:      "The truncation strategy to use for the thread. The default is `auto`. If set to `last_messages`, the thread will be truncated to the n most recent messages in the thread. When set to `auto`, messages in the middle of the thread will be dropped to fit the context length of the model, `max_prompt_tokens`.",
			InnerField: "type",
		},
		&requestflag.InnerFlag[*int64]{
			Name:       "truncation-strategy.last-messages",
			Usage:      "The number of most recent messages from the thread when constructing the context for the run.",
			InnerField: "last_messages",
		},
	},
})

var betaThreadsRunsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves a run.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "thread-id",
			Required:  true,
			PathParam: "thread_id",
		},
		&requestflag.Flag[string]{
			Name:      "run-id",
			Required:  true,
			PathParam: "run_id",
		},
	},
	Action:          handleBetaThreadsRunsRetrieve,
	HideHelpCommand: true,
}

var betaThreadsRunsUpdate = cli.Command{
	Name:    "update",
	Usage:   "Modifies a run.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "thread-id",
			Required:  true,
			PathParam: "thread_id",
		},
		&requestflag.Flag[string]{
			Name:      "run-id",
			Required:  true,
			PathParam: "run_id",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "metadata",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			BodyPath: "metadata",
		},
	},
	Action:          handleBetaThreadsRunsUpdate,
	HideHelpCommand: true,
}

var betaThreadsRunsList = cli.Command{
	Name:    "list",
	Usage:   "Returns a list of runs belonging to a thread.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "thread-id",
			Required:  true,
			PathParam: "thread_id",
		},
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
	Action:          handleBetaThreadsRunsList,
	HideHelpCommand: true,
}

var betaThreadsRunsCancel = cli.Command{
	Name:    "cancel",
	Usage:   "Cancels a run that is `in_progress`.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "thread-id",
			Required:  true,
			PathParam: "thread_id",
		},
		&requestflag.Flag[string]{
			Name:      "run-id",
			Required:  true,
			PathParam: "run_id",
		},
	},
	Action:          handleBetaThreadsRunsCancel,
	HideHelpCommand: true,
}

var betaThreadsRunsSubmitToolOutputs = requestflag.WithInnerFlags(cli.Command{
	Name:    "submit-tool-outputs",
	Usage:   "When a run has the `status: \"requires_action\"` and `required_action.type` is\n`submit_tool_outputs`, this endpoint can be used to submit the outputs from the\ntool calls once they're all completed. All outputs must be submitted in a single\nrequest.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "thread-id",
			Required:  true,
			PathParam: "thread_id",
		},
		&requestflag.Flag[string]{
			Name:      "run-id",
			Required:  true,
			PathParam: "run_id",
		},
		&requestflag.Flag[[]map[string]any]{
			Name:     "tool-output",
			Usage:    "A list of tools for which the outputs are being submitted.",
			Required: true,
			BodyPath: "tool_outputs",
		},
		&requestflag.Flag[*bool]{
			Name:     "stream",
			Usage:    "If `true`, returns a stream of events that happen during the Run as server-sent events, terminating when the Run enters a terminal state with a `data: [DONE]` message.\n",
			BodyPath: "stream",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleBetaThreadsRunsSubmitToolOutputs,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"tool-output": {
		&requestflag.InnerFlag[string]{
			Name:       "tool-output.output",
			Usage:      "The output of the tool call to be submitted to continue the run.",
			InnerField: "output",
		},
		&requestflag.InnerFlag[string]{
			Name:       "tool-output.tool-call-id",
			Usage:      "The ID of the tool call in the `required_action` object within the run object the output is being submitted for.",
			InnerField: "tool_call_id",
		},
	},
})

func handleBetaThreadsRunsCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("thread-id") && len(unusedArgs) > 0 {
		cmd.Set("thread-id", unusedArgs[0])
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

	params := openai.BetaThreadRunNewParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if cmd.Bool("stream") {
		stream := client.Beta.Threads.Runs.NewStreaming(
			ctx,
			cmd.Value("thread-id").(string),
			params,
			options...,
		)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(stream, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:threads:runs create",
			Transform:      transform,
		})
	} else {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Beta.Threads.Runs.New(
			ctx,
			cmd.Value("thread-id").(string),
			params,
			options...,
		)
		if err != nil {
			return err
		}

		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:threads:runs create",
			Transform:      transform,
		})
	}
}

func handleBetaThreadsRunsRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("thread-id") && len(unusedArgs) > 0 {
		cmd.Set("thread-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("run-id") && len(unusedArgs) > 0 {
		cmd.Set("run-id", unusedArgs[0])
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
	_, err = client.Beta.Threads.Runs.Get(
		ctx,
		cmd.Value("thread-id").(string),
		cmd.Value("run-id").(string),
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
		Title:          "beta:threads:runs retrieve",
		Transform:      transform,
	})
}

func handleBetaThreadsRunsUpdate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("thread-id") && len(unusedArgs) > 0 {
		cmd.Set("thread-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("run-id") && len(unusedArgs) > 0 {
		cmd.Set("run-id", unusedArgs[0])
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

	params := openai.BetaThreadRunUpdateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Beta.Threads.Runs.Update(
		ctx,
		cmd.Value("thread-id").(string),
		cmd.Value("run-id").(string),
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
		Title:          "beta:threads:runs update",
		Transform:      transform,
	})
}

func handleBetaThreadsRunsList(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("thread-id") && len(unusedArgs) > 0 {
		cmd.Set("thread-id", unusedArgs[0])
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

	params := openai.BetaThreadRunListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Beta.Threads.Runs.List(
			ctx,
			cmd.Value("thread-id").(string),
			params,
			options...,
		)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:threads:runs list",
			Transform:      transform,
		})
	} else {
		iter := client.Beta.Threads.Runs.ListAutoPaging(
			ctx,
			cmd.Value("thread-id").(string),
			params,
			options...,
		)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:threads:runs list",
			Transform:      transform,
		})
	}
}

func handleBetaThreadsRunsCancel(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("thread-id") && len(unusedArgs) > 0 {
		cmd.Set("thread-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("run-id") && len(unusedArgs) > 0 {
		cmd.Set("run-id", unusedArgs[0])
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
	_, err = client.Beta.Threads.Runs.Cancel(
		ctx,
		cmd.Value("thread-id").(string),
		cmd.Value("run-id").(string),
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
		Title:          "beta:threads:runs cancel",
		Transform:      transform,
	})
}

func handleBetaThreadsRunsSubmitToolOutputs(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("thread-id") && len(unusedArgs) > 0 {
		cmd.Set("thread-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("run-id") && len(unusedArgs) > 0 {
		cmd.Set("run-id", unusedArgs[0])
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

	params := openai.BetaThreadRunSubmitToolOutputsParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if cmd.Bool("stream") {
		stream := client.Beta.Threads.Runs.SubmitToolOutputsStreaming(
			ctx,
			cmd.Value("thread-id").(string),
			cmd.Value("run-id").(string),
			params,
			options...,
		)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(stream, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:threads:runs submit-tool-outputs",
			Transform:      transform,
		})
	} else {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Beta.Threads.Runs.SubmitToolOutputs(
			ctx,
			cmd.Value("thread-id").(string),
			cmd.Value("run-id").(string),
			params,
			options...,
		)
		if err != nil {
			return err
		}

		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:threads:runs submit-tool-outputs",
			Transform:      transform,
		})
	}
}
