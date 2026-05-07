// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/realtime"
	"github.com/urfave/cli/v3"
)

var realtimeCallsAccept = requestflag.WithInnerFlags(cli.Command{
	Name:    "accept",
	Usage:   "Accept an incoming SIP call and configure the realtime session that will handle\nit.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "call-id",
			Required:  true,
			PathParam: "call_id",
		},
		&requestflag.Flag[string]{
			Name:     "type",
			Usage:    "The type of session to create. Always `realtime` for the Realtime API.\n",
			Default:  "realtime",
			Const:    true,
			BodyPath: "type",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "audio",
			Usage:    "Configuration for input and output audio.\n",
			BodyPath: "audio",
		},
		&requestflag.Flag[[]string]{
			Name:     "include",
			Usage:    "Additional fields to include in server outputs.\n\n`item.input_audio_transcription.logprobs`: Include logprobs for input audio transcription.\n",
			BodyPath: "include",
		},
		&requestflag.Flag[string]{
			Name:     "instructions",
			Usage:    "The default system instructions (i.e. system message) prepended to model calls. This field allows the client to guide the model on desired responses. The model can be instructed on response content and format, (e.g. \"be extremely succinct\", \"act friendly\", \"here are examples of good responses\") and on audio behavior (e.g. \"talk quickly\", \"inject emotion into your voice\", \"laugh frequently\"). The instructions are not guaranteed to be followed by the model, but they provide guidance to the model on the desired behavior.\n\nNote that the server sets default instructions which will be used if this field is not set and are visible in the `session.created` event at the start of the session.\n",
			BodyPath: "instructions",
		},
		&requestflag.Flag[any]{
			Name:     "max-output-tokens",
			Usage:    "Maximum number of output tokens for a single assistant response,\ninclusive of tool calls. Provide an integer between 1 and 4096 to\nlimit output tokens, or `inf` for the maximum available tokens for a\ngiven model. Defaults to `inf`.\n",
			BodyPath: "max_output_tokens",
		},
		&requestflag.Flag[string]{
			Name:     "model",
			Usage:    "The Realtime model used for this session.\n",
			BodyPath: "model",
		},
		&requestflag.Flag[[]string]{
			Name:     "output-modality",
			Usage:    "The set of modalities the model can respond with. It defaults to `[\"audio\"]`, indicating\nthat the model will respond with audio plus a transcript. `[\"text\"]` can be used to make\nthe model respond with text only. It is not possible to request both `text` and `audio` at the same time.\n",
			Default:  []string{"audio"},
			BodyPath: "output_modalities",
		},
		&requestflag.Flag[bool]{
			Name:     "parallel-tool-calls",
			Usage:    "Whether the model may call multiple tools in parallel. Only supported by\nreasoning Realtime models such as `gpt-realtime-2`.\n",
			BodyPath: "parallel_tool_calls",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "prompt",
			Usage:    "Reference to a prompt template and its variables.\n[Learn more](https://platform.openai.com/docs/guides/text?api-mode=responses#reusable-prompts).\n",
			BodyPath: "prompt",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "reasoning",
			Usage:    "Configuration for reasoning-capable Realtime models such as `gpt-realtime-2`.\n",
			BodyPath: "reasoning",
		},
		&requestflag.Flag[any]{
			Name:     "tool-choice",
			Usage:    "How the model chooses tools. Provide one of the string modes or force a specific\nfunction/MCP tool.\n",
			Default:  "auto",
			BodyPath: "tool_choice",
		},
		&requestflag.Flag[[]map[string]any]{
			Name:     "tool",
			Usage:    "Tools available to the model.",
			BodyPath: "tools",
		},
		&requestflag.Flag[any]{
			Name:     "tracing",
			Usage:    "Realtime API can write session traces to the [Traces Dashboard](https://platform.openai.com/logs?api=traces). Set to null to disable tracing. Once\ntracing is enabled for a session, the configuration cannot be modified.\n\n`auto` will create a trace for the session with default values for the\nworkflow name, group id, and metadata.\n",
			BodyPath: "tracing",
		},
		&requestflag.Flag[any]{
			Name:     "truncation",
			Usage:    "When the number of tokens in a conversation exceeds the model's input token limit, the conversation be truncated, meaning messages (starting from the oldest) will not be included in the model's context. A 32k context model with 4,096 max output tokens can only include 28,224 tokens in the context before truncation occurs.\n\nClients can configure truncation behavior to truncate with a lower max token limit, which is an effective way to control token usage and cost.\n\nTruncation will reduce the number of cached tokens on the next turn (busting the cache), since messages are dropped from the beginning of the context. However, clients can also configure truncation to retain messages up to a fraction of the maximum context size, which will reduce the need for future truncations and thus improve the cache rate.\n\nTruncation can be disabled entirely, which means the server will never truncate but would instead return an error if the conversation exceeds the model's input token limit.\n",
			BodyPath: "truncation",
		},
	},
	Action:          handleRealtimeCallsAccept,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"audio": {
		&requestflag.InnerFlag[map[string]any]{
			Name:       "audio.input",
			InnerField: "input",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "audio.output",
			InnerField: "output",
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
	"reasoning": {
		&requestflag.InnerFlag[string]{
			Name:       "reasoning.effort",
			Usage:      "Constrains effort on reasoning for reasoning-capable Realtime models such as\n`gpt-realtime-2`.\n",
			InnerField: "effort",
		},
	},
})

var realtimeCallsHangup = cli.Command{
	Name:    "hangup",
	Usage:   "End an active Realtime API call, whether it was initiated over SIP or WebRTC.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "call-id",
			Required:  true,
			PathParam: "call_id",
		},
	},
	Action:          handleRealtimeCallsHangup,
	HideHelpCommand: true,
}

var realtimeCallsRefer = cli.Command{
	Name:    "refer",
	Usage:   "Transfer an active SIP call to a new destination using the SIP REFER verb.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "call-id",
			Required:  true,
			PathParam: "call_id",
		},
		&requestflag.Flag[string]{
			Name:     "target-uri",
			Usage:    "URI that should appear in the SIP Refer-To header. Supports values like\n`tel:+14155550123` or `sip:agent@example.com`.",
			Required: true,
			BodyPath: "target_uri",
		},
	},
	Action:          handleRealtimeCallsRefer,
	HideHelpCommand: true,
}

var realtimeCallsReject = cli.Command{
	Name:    "reject",
	Usage:   "Decline an incoming SIP call by returning a SIP status code to the caller.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "call-id",
			Required:  true,
			PathParam: "call_id",
		},
		&requestflag.Flag[int64]{
			Name:     "status-code",
			Usage:    "SIP response code to send back to the caller. Defaults to `603` (Decline)\nwhen omitted.",
			BodyPath: "status_code",
		},
	},
	Action:          handleRealtimeCallsReject,
	HideHelpCommand: true,
}

func handleRealtimeCallsAccept(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("call-id") && len(unusedArgs) > 0 {
		cmd.Set("call-id", unusedArgs[0])
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

	params := realtime.CallAcceptParams{}

	return client.Realtime.Calls.Accept(
		ctx,
		cmd.Value("call-id").(string),
		params,
		options...,
	)
}

func handleRealtimeCallsHangup(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("call-id") && len(unusedArgs) > 0 {
		cmd.Set("call-id", unusedArgs[0])
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

	return client.Realtime.Calls.Hangup(ctx, cmd.Value("call-id").(string), options...)
}

func handleRealtimeCallsRefer(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("call-id") && len(unusedArgs) > 0 {
		cmd.Set("call-id", unusedArgs[0])
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

	params := realtime.CallReferParams{}

	return client.Realtime.Calls.Refer(
		ctx,
		cmd.Value("call-id").(string),
		params,
		options...,
	)
}

func handleRealtimeCallsReject(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("call-id") && len(unusedArgs) > 0 {
		cmd.Set("call-id", unusedArgs[0])
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

	params := realtime.CallRejectParams{}

	return client.Realtime.Calls.Reject(
		ctx,
		cmd.Value("call-id").(string),
		params,
		options...,
	)
}
