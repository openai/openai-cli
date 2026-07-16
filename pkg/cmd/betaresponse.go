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

var betaResponsesCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Creates a model response. Provide\n[text](https://platform.openai.com/docs/guides/text) or\n[image](https://platform.openai.com/docs/guides/images) inputs to generate\n[text](https://platform.openai.com/docs/guides/text) or\n[JSON](https://platform.openai.com/docs/guides/structured-outputs) outputs. Have\nthe model call your own\n[custom code](https://platform.openai.com/docs/guides/function-calling) or use\nbuilt-in [tools](https://platform.openai.com/docs/guides/tools) like\n[web search](https://platform.openai.com/docs/guides/tools-web-search) or\n[file search](https://platform.openai.com/docs/guides/tools-file-search) to use\nyour own data as input for the model's response.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[*bool]{
			Name:     "background",
			Usage:    "Whether to run the model response in the background.\n[Learn more](https://platform.openai.com/docs/guides/background).\n",
			Default:  requestflag.Ptr[bool](false),
			BodyPath: "background",
		},
		&requestflag.Flag[any]{
			Name:     "context-management",
			Usage:    "Context management configuration for this request.\n",
			BodyPath: "context_management",
		},
		&requestflag.Flag[any]{
			Name:     "conversation",
			Usage:    "The conversation that this response belongs to. Items from this conversation are prepended to `input_items` for this response request.\nInput items and output items from this response are automatically added to this conversation after this response completes.\n",
			BodyPath: "conversation",
		},
		&requestflag.Flag[any]{
			Name:     "include",
			Usage:    "Specify additional output data to include in the model response. Currently supported values are:\n- `web_search_call.action.sources`: Include the sources of the web search tool call.\n- `code_interpreter_call.outputs`: Includes the outputs of python code execution in code interpreter tool call items.\n- `computer_call_output.output.image_url`: Include image urls from the computer call output.\n- `file_search_call.results`: Include the search results of the file search tool call.\n- `message.input_image.image_url`: Include image urls from the input message.\n- `message.output_text.logprobs`: Include logprobs with assistant messages.\n- `reasoning.encrypted_content`: Includes an encrypted version of reasoning tokens in reasoning item outputs. This enables reasoning items to be used in multi-turn conversations when using the Responses API statelessly (like when the `store` parameter is set to `false`, or when an organization is enrolled in the zero data retention program).",
			BodyPath: "include",
		},
		&requestflag.Flag[any]{
			Name:     "input",
			Usage:    "Text, image, or file inputs to the model, used to generate a response.\n\nLearn more:\n- [Text inputs and outputs](https://platform.openai.com/docs/guides/text)\n- [Image inputs](https://platform.openai.com/docs/guides/images)\n- [File inputs](https://platform.openai.com/docs/guides/pdf-files)\n- [Conversation state](https://platform.openai.com/docs/guides/conversation-state)\n- [Function calling](https://platform.openai.com/docs/guides/function-calling)\n",
			BodyPath: "input",
		},
		&requestflag.Flag[*string]{
			Name:     "instructions",
			Usage:    "A system (or developer) message inserted into the model's context.\n\nWhen using along with `previous_response_id`, the instructions from a previous\nresponse will not be carried over to the next response. This makes it simple\nto swap out system (or developer) messages in new responses.\n",
			BodyPath: "instructions",
		},
		&requestflag.Flag[*int64]{
			Name:     "max-output-tokens",
			Usage:    "An upper bound for the number of tokens that can be generated for a response, including visible output tokens and [reasoning tokens](https://platform.openai.com/docs/guides/reasoning).\n",
			BodyPath: "max_output_tokens",
		},
		&requestflag.Flag[*int64]{
			Name:     "max-tool-calls",
			Usage:    "The maximum number of total calls to built-in tools that can be processed in a response. This maximum number applies across all built-in tool calls, not per individual tool. Any further attempts to call a tool by the model will be ignored.\n",
			BodyPath: "max_tool_calls",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "metadata",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			BodyPath: "metadata",
		},
		&requestflag.Flag[string]{
			Name:     "model",
			Usage:    "Model ID used to generate the response, like `gpt-4o` or `o3`. OpenAI\noffers a wide range of models with different capabilities, performance\ncharacteristics, and price points. Refer to the [model guide](https://platform.openai.com/docs/models)\nto browse and compare available models.\n",
			BodyPath: "model",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "moderation",
			Usage:    "Configuration for running moderation on the input and output of this response.\n",
			BodyPath: "moderation",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "multi-agent",
			Usage:    "Configuration for server-hosted multi-agent execution.",
			BodyPath: "multi_agent",
		},
		&requestflag.Flag[*bool]{
			Name:     "parallel-tool-calls",
			Usage:    "Whether to allow the model to run tool calls in parallel.\n",
			Default:  requestflag.Ptr[bool](true),
			BodyPath: "parallel_tool_calls",
		},
		&requestflag.Flag[*string]{
			Name:     "previous-response-id",
			Usage:    "The unique ID of the previous response to the model. Use this to\ncreate multi-turn conversations. Learn more about\n[conversation state](https://platform.openai.com/docs/guides/conversation-state). Cannot be used in conjunction with `conversation`.\n",
			BodyPath: "previous_response_id",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "prompt",
			Usage:    "Reference to a prompt template and its variables.\n[Learn more](https://platform.openai.com/docs/guides/text?api-mode=responses#reusable-prompts).\n",
			BodyPath: "prompt",
		},
		&requestflag.Flag[string]{
			Name:     "prompt-cache-key",
			Usage:    "Used by OpenAI to cache responses for similar requests to optimize your cache hit rates. Replaces the `user` field. [Learn more](https://platform.openai.com/docs/guides/prompt-caching).\n",
			BodyPath: "prompt_cache_key",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "prompt-cache-options",
			Usage:    "Options for prompt caching. Supported for `gpt-5.6` and later models. By default, OpenAI automatically chooses one implicit cache breakpoint. You can add explicit breakpoints to content blocks with `prompt_cache_breakpoint`. Each request can write up to four breakpoints. For cache matching, OpenAI considers up to the latest 80 breakpoints in the conversation, without a content-block lookback limit. Set `mode` to `explicit` to disable the implicit breakpoint. The `ttl` defaults to `30m`, which is currently the only supported value. See the [prompt caching guide](https://platform.openai.com/docs/guides/prompt-caching) for current details.",
			BodyPath: "prompt_cache_options",
		},
		&requestflag.Flag[*string]{
			Name:     "prompt-cache-retention",
			Usage:    "Deprecated. Use `prompt_cache_options.ttl` instead.\n\nThe retention policy for the prompt cache. Set to `24h` to enable extended prompt caching, which keeps cached prefixes active for longer, up to a maximum of 24 hours. [Learn more](https://platform.openai.com/docs/guides/prompt-caching#prompt-cache-retention).\nThis field expresses a maximum retention policy, while\n`prompt_cache_options.ttl` expresses a minimum cache lifetime. The two\nfields are independent and do not interact.\nFor `gpt-5.5`, `gpt-5.5-pro`, and future models, only `24h` is supported.\n\nFor older models that support both `in_memory` and `24h`, the default depends on your organization's data retention policy:\n  - Organizations without ZDR enabled default to `24h`.\n  - Organizations with ZDR enabled default to `in_memory` when `prompt_cache_retention` is not specified.\n",
			BodyPath: "prompt_cache_retention",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "reasoning",
			Usage:    "**gpt-5 and o-series models only**\n\nConfiguration options for\n[reasoning models](https://platform.openai.com/docs/guides/reasoning).\n",
			BodyPath: "reasoning",
		},
		&requestflag.Flag[string]{
			Name:     "safety-identifier",
			Usage:    "A stable identifier used to help detect users of your application that may be violating OpenAI's usage policies.\nThe IDs should be a string that uniquely identifies each user, with a maximum length of 64 characters. We recommend hashing their username or email address, in order to avoid sending us any identifying information. [Learn more](https://platform.openai.com/docs/guides/safety-best-practices#safety-identifiers).\n",
			BodyPath: "safety_identifier",
		},
		&requestflag.Flag[*string]{
			Name:     "service-tier",
			Usage:    "Specifies the processing type used for serving the request.\n  - If set to 'auto', then the request will be processed with the service tier configured in the Project settings. Unless otherwise configured, the Project will use 'default'.\n  - If set to 'default', then the request will be processed with the standard pricing and performance for the selected model.\n  - If set to '[flex](https://platform.openai.com/docs/guides/flex-processing)' or '[priority](https://openai.com/api-priority-processing/)', then the request will be processed with the corresponding service tier.\n  - When not set, the default behavior is 'auto'.\n\n  When the `service_tier` parameter is set, the response body will include the `service_tier` value based on the processing mode actually used to serve the request. This response value may be different from the value set in the parameter.\n",
			Default:  requestflag.Ptr[string]("auto"),
			BodyPath: "service_tier",
		},
		&requestflag.Flag[*bool]{
			Name:     "store",
			Usage:    "Whether to store the generated model response for later retrieval via\nAPI.\n",
			Default:  requestflag.Ptr[bool](true),
			BodyPath: "store",
		},
		&requestflag.Flag[*bool]{
			Name:     "stream",
			Usage:    "If set to true, the model response data will be streamed to the client\nas it is generated using [server-sent events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events#Event_stream_format).\nSee the [Streaming section below](https://platform.openai.com/docs/api-reference/responses-streaming)\nfor more information.\n",
			Default:  requestflag.Ptr[bool](false),
			BodyPath: "stream",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "stream-options",
			Usage:    "Options for streaming responses. Only set this when you set `stream: true`.\n",
			BodyPath: "stream_options",
		},
		&requestflag.Flag[*float64]{
			Name:     "temperature",
			Usage:    "What sampling temperature to use, between 0 and 2. Higher values like 0.8 will make the output more random, while lower values like 0.2 will make it more focused and deterministic.\nWe generally recommend altering this or `top_p` but not both.\n",
			Default:  requestflag.Ptr[float64](1),
			BodyPath: "temperature",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "text",
			Usage:    "Configuration options for a text response from the model. Can be plain\ntext or structured JSON data. Learn more:\n- [Text inputs and outputs](https://platform.openai.com/docs/guides/text)\n- [Structured Outputs](https://platform.openai.com/docs/guides/structured-outputs)\n",
			BodyPath: "text",
		},
		&requestflag.Flag[any]{
			Name:     "tool-choice",
			Usage:    "How the model should select which tool (or tools) to use when generating\na response. See the `tools` parameter to see how to specify which tools\nthe model can call.\n",
			BodyPath: "tool_choice",
		},
		&requestflag.Flag[[]map[string]any]{
			Name:     "tool",
			Usage:    "An array of tools the model may call while generating a response. You\ncan specify which tool to use by setting the `tool_choice` parameter.\n\nWe support the following categories of tools:\n- **Built-in tools**: Tools that are provided by OpenAI that extend the\n  model's capabilities, like [web search](https://platform.openai.com/docs/guides/tools-web-search)\n  or [file search](https://platform.openai.com/docs/guides/tools-file-search). Learn more about\n  [built-in tools](https://platform.openai.com/docs/guides/tools).\n- **MCP Tools**: Integrations with third-party systems via custom MCP servers\n  or predefined connectors such as Google Drive and SharePoint. Learn more about\n  [MCP Tools](https://platform.openai.com/docs/guides/tools-connectors-mcp).\n- **Function calls (custom tools)**: Functions that are defined by you,\n  enabling the model to call your own code with strongly typed arguments\n  and outputs. Learn more about\n  [function calling](https://platform.openai.com/docs/guides/function-calling). You can also use\n  custom tools to call your own code.\n",
			BodyPath: "tools",
		},
		&requestflag.Flag[*int64]{
			Name:     "top-logprobs",
			Usage:    "An integer between 0 and 20 specifying the maximum number of most likely\ntokens to return at each token position, each with an associated log\nprobability. In some cases, the number of returned tokens may be fewer than\nrequested.\n",
			BodyPath: "top_logprobs",
		},
		&requestflag.Flag[*float64]{
			Name:     "top-p",
			Usage:    "An alternative to sampling with temperature, called nucleus sampling,\nwhere the model considers the results of the tokens with top_p probability\nmass. So 0.1 means only the tokens comprising the top 10% probability mass\nare considered.\n\nWe generally recommend altering this or `temperature` but not both.\n",
			Default:  requestflag.Ptr[float64](1),
			BodyPath: "top_p",
		},
		&requestflag.Flag[*string]{
			Name:     "truncation",
			Usage:    "The truncation strategy to use for the model response.\n- `auto`: If the input to this Response exceeds\n  the model's context window size, the model will truncate the\n  response to fit the context window by dropping items from the beginning of the conversation.\n- `disabled` (default): If the input size will exceed the context window\n  size for a model, the request will fail with a 400 error.\n",
			Default:  requestflag.Ptr[string]("disabled"),
			BodyPath: "truncation",
		},
		&requestflag.Flag[string]{
			Name:     "user",
			Usage:    "This field is being replaced by `safety_identifier` and `prompt_cache_key`. Use `prompt_cache_key` instead to maintain caching optimizations.\nA stable identifier for your end-users.\nUsed to boost cache hit rates by better bucketing similar requests and  to help OpenAI detect and prevent abuse. [Learn more](https://platform.openai.com/docs/guides/safety-best-practices#safety-identifiers).\n",
			BodyPath: "user",
		},
		&requestflag.Flag[[]string]{
			Name:       "beta",
			HeaderPath: "openai-beta",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleBetaResponsesCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"context-management": {
		&requestflag.InnerFlag[string]{
			Name:                  "context-management.type",
			Usage:                 "The context management entry type. Currently only 'compaction' is supported.",
			InnerField:            "type",
			OuterIsArrayOfObjects: true,
		},
		&requestflag.InnerFlag[*int64]{
			Name:                  "context-management.compact-threshold",
			Usage:                 "Token threshold at which compaction should be triggered for this entry.",
			InnerField:            "compact_threshold",
			OuterIsArrayOfObjects: true,
		},
	},
	"moderation": {
		&requestflag.InnerFlag[string]{
			Name:       "moderation.model",
			Usage:      "The moderation model to use for moderated completions, e.g. 'omni-moderation-latest'.",
			InnerField: "model",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "moderation.policy",
			Usage:      "The policy to apply to moderated response input and output.",
			InnerField: "policy",
		},
	},
	"multi-agent": {
		&requestflag.InnerFlag[bool]{
			Name:       "multi-agent.enabled",
			Usage:      "Whether to enable server-hosted multi-agent execution for this response.",
			InnerField: "enabled",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "multi-agent.max-concurrent-subagents",
			Usage:      "`max_concurrent_subagents` sets the maximum number of subagents that can be active simultaneously across the entire agent tree. It includes all descendants—children, grandchildren, and deeper subagents—but excludes the root agent.\nThe API does not impose a fixed upper bound on this setting. The default is `3`, which is recommended for most workloads. Multi-agent runs also have no fixed limit on tree depth or the total number of subagents created during a run.",
			InnerField: "max_concurrent_subagents",
		},
	},
	"prompt": {
		&requestflag.InnerFlag[string]{
			Name:       "prompt.id",
			Usage:      "The unique identifier of the prompt template to use.",
			InnerField: "id",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "prompt.variables",
			Usage:      "Optional map of values to substitute in for variables in your\nprompt. The substitution values can either be strings, or other\nResponse input types like images or files.\n",
			InnerField: "variables",
		},
		&requestflag.InnerFlag[*string]{
			Name:       "prompt.version",
			Usage:      "Optional version of the prompt template.",
			InnerField: "version",
		},
	},
	"prompt-cache-options": {
		&requestflag.InnerFlag[string]{
			Name:       "prompt-cache-options.mode",
			Usage:      "Controls whether OpenAI automatically creates an implicit cache breakpoint. Defaults to `implicit`. With `implicit`, OpenAI creates one implicit breakpoint and writes up to the latest three explicit breakpoints in the request. With `explicit`, OpenAI does not create an implicit breakpoint and writes up to the latest four explicit breakpoints. If there are no explicit breakpoints, the request does not use prompt caching.",
			InnerField: "mode",
		},
		&requestflag.InnerFlag[string]{
			Name:       "prompt-cache-options.ttl",
			Usage:      "The minimum lifetime applied to every implicit and explicit cache breakpoint written by the request. Defaults to `30m`, which is currently the only supported value. The backend may retain cache entries for longer.",
			InnerField: "ttl",
		},
	},
	"reasoning": {
		&requestflag.InnerFlag[*string]{
			Name:       "reasoning.context",
			Usage:      "Controls which reasoning items are rendered back to the model on later turns.\nWhen returned on a response, this is the effective reasoning context mode\nused for the response.\n",
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
	"stream-options": {
		&requestflag.InnerFlag[bool]{
			Name:       "stream-options.include-obfuscation",
			Usage:      "When true, stream obfuscation will be enabled. Stream obfuscation adds\nrandom characters to an `obfuscation` field on streaming delta events to\nnormalize payload sizes as a mitigation to certain side-channel attacks.\nThese obfuscation fields are included by default, but add a small amount\nof overhead to the data stream. You can set `include_obfuscation` to\nfalse to optimize for bandwidth if you trust the network links between\nyour application and the OpenAI API.\n",
			InnerField: "include_obfuscation",
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
			Usage:      "Constrains the verbosity of the model's response. Lower values will result in\nmore concise responses, while higher values will result in more verbose responses.\nCurrently supported values are `low`, `medium`, and `high`.\n",
			InnerField: "verbosity",
		},
	},
})

var betaResponsesRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves a model response with the given ID.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "response-id",
			Required:  true,
			PathParam: "response_id",
		},
		&requestflag.Flag[[]string]{
			Name:      "include",
			Usage:     "Additional fields to include in the response. See the `include`\nparameter for Response creation above for more information.\n",
			QueryPath: "include",
		},
		&requestflag.Flag[bool]{
			Name:      "include-obfuscation",
			Usage:     "When true, stream obfuscation will be enabled. Stream obfuscation adds\nrandom characters to an `obfuscation` field on streaming delta events\nto normalize payload sizes as a mitigation to certain side-channel\nattacks. These obfuscation fields are included by default, but add a\nsmall amount of overhead to the data stream. You can set\n`include_obfuscation` to false to optimize for bandwidth if you trust\nthe network links between your application and the OpenAI API.\n",
			QueryPath: "include_obfuscation",
		},
		&requestflag.Flag[int64]{
			Name:      "starting-after",
			Usage:     "The sequence number of the event after which to start streaming.\n",
			QueryPath: "starting_after",
		},
		&requestflag.Flag[bool]{
			Name:      "stream",
			Usage:     "If set to true, the model response data will be streamed to the client\nas it is generated using [server-sent events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events#Event_stream_format).\nSee the [Streaming section below](https://platform.openai.com/docs/api-reference/responses-streaming)\nfor more information.\n",
			QueryPath: "stream",
		},
		&requestflag.Flag[[]string]{
			Name:       "beta",
			HeaderPath: "openai-beta",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleBetaResponsesRetrieve,
	HideHelpCommand: true,
}

var betaResponsesDelete = cli.Command{
	Name:    "delete",
	Usage:   "Deletes a model response with the given ID.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "response-id",
			Required:  true,
			PathParam: "response_id",
		},
		&requestflag.Flag[[]string]{
			Name:       "beta",
			HeaderPath: "openai-beta",
		},
	},
	Action:          handleBetaResponsesDelete,
	HideHelpCommand: true,
}

var betaResponsesCancel = cli.Command{
	Name:    "cancel",
	Usage:   "Cancels a model response with the given ID. Only responses created with the\n`background` parameter set to `true` can be cancelled.\n[Learn more](https://platform.openai.com/docs/guides/background).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "response-id",
			Required:  true,
			PathParam: "response_id",
		},
		&requestflag.Flag[[]string]{
			Name:       "beta",
			HeaderPath: "openai-beta",
		},
	},
	Action:          handleBetaResponsesCancel,
	HideHelpCommand: true,
}

var betaResponsesCompact = requestflag.WithInnerFlags(cli.Command{
	Name:    "compact",
	Usage:   "Compact a conversation. Returns a compacted response object.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[*string]{
			Name:     "model",
			Usage:    "Model ID used to generate the response, like `gpt-5` or `o3`. OpenAI offers a wide range of models with different capabilities, performance characteristics, and price points. Refer to the [model guide](https://platform.openai.com/docs/models) to browse and compare available models.",
			Required: true,
			BodyPath: "model",
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
			Name:     "previous-response-id",
			Usage:    "The unique ID of the previous response to the model. Use this to create multi-turn conversations. Learn more about [conversation state](https://platform.openai.com/docs/guides/conversation-state). Cannot be used in conjunction with `conversation`.",
			BodyPath: "previous_response_id",
		},
		&requestflag.Flag[*string]{
			Name:     "prompt-cache-key",
			Usage:    "A key to use when reading from or writing to the prompt cache.",
			BodyPath: "prompt_cache_key",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "prompt-cache-options",
			Usage:    "Options for prompt caching. Supported for `gpt-5.6` and later models. By default, OpenAI automatically chooses one implicit cache breakpoint. You can add explicit breakpoints to content blocks with `prompt_cache_breakpoint`. Each request can write up to four breakpoints. For cache matching, OpenAI considers up to the latest 80 breakpoints in the conversation, without a content-block lookback limit. Set `mode` to `explicit` to disable the implicit breakpoint. The `ttl` defaults to `30m`, which is currently the only supported value. See the [prompt caching guide](https://platform.openai.com/docs/guides/prompt-caching) for current details.",
			BodyPath: "prompt_cache_options",
		},
		&requestflag.Flag[*string]{
			Name:     "prompt-cache-retention",
			Usage:    "How long to retain a prompt cache entry created by this request.",
			BodyPath: "prompt_cache_retention",
		},
		&requestflag.Flag[*string]{
			Name:     "service-tier",
			Usage:    "The service tier to use for this request.",
			BodyPath: "service_tier",
		},
		&requestflag.Flag[[]string]{
			Name:       "beta",
			HeaderPath: "openai-beta",
		},
	},
	Action:          handleBetaResponsesCompact,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"prompt-cache-options": {
		&requestflag.InnerFlag[string]{
			Name:       "prompt-cache-options.mode",
			Usage:      "Controls whether OpenAI automatically creates an implicit cache breakpoint. Defaults to `implicit`. With `implicit`, OpenAI creates one implicit breakpoint and writes up to the latest three explicit breakpoints in the request. With `explicit`, OpenAI does not create an implicit breakpoint and writes up to the latest four explicit breakpoints. If there are no explicit breakpoints, the request does not use prompt caching.",
			InnerField: "mode",
		},
		&requestflag.InnerFlag[string]{
			Name:       "prompt-cache-options.ttl",
			Usage:      "The minimum lifetime applied to every implicit and explicit cache breakpoint written by the request. Defaults to `30m`, which is currently the only supported value. The backend may retain cache entries for longer.",
			InnerField: "ttl",
		},
	},
})

func handleBetaResponsesCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.BetaResponseNewParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if requestflag.FlagBool(cmd, "stream") {
		stream := client.Beta.Responses.NewStreaming(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(stream, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:responses create",
			Transform:      transform,
		})
	} else {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Beta.Responses.New(ctx, params, options...)
		if err != nil {
			return err
		}

		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "beta:responses create",
			Transform:      transform,
		})
	}
}

func handleBetaResponsesRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("response-id") && len(unusedArgs) > 0 {
		cmd.Set("response-id", unusedArgs[0])
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

	params := openai.BetaResponseGetParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if cmd.Bool("stream") {
		stream := client.Beta.Responses.GetStreaming(
			ctx,
			cmd.Value("response-id").(string),
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
			Title:          "beta:responses retrieve",
			Transform:      transform,
		})
	} else {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Beta.Responses.Get(
			ctx,
			cmd.Value("response-id").(string),
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
			Title:          "beta:responses retrieve",
			Transform:      transform,
		})
	}
}

func handleBetaResponsesDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("response-id") && len(unusedArgs) > 0 {
		cmd.Set("response-id", unusedArgs[0])
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

	params := openai.BetaResponseDeleteParams{}

	return client.Beta.Responses.Delete(
		ctx,
		cmd.Value("response-id").(string),
		params,
		options...,
	)
}

func handleBetaResponsesCancel(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("response-id") && len(unusedArgs) > 0 {
		cmd.Set("response-id", unusedArgs[0])
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

	params := openai.BetaResponseCancelParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Beta.Responses.Cancel(
		ctx,
		cmd.Value("response-id").(string),
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
		Title:          "beta:responses cancel",
		Transform:      transform,
	})
}

func handleBetaResponsesCompact(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.BetaResponseCompactParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Beta.Responses.Compact(ctx, params, options...)
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
		Title:          "beta:responses compact",
		Transform:      transform,
	})
}
