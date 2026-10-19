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

var decisionsCreate = requestflag.WithInnerFlags(cli.Command{
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
			Name:     "prompt-cache-key",
			Usage:    "Partition prompt cache reuse by caller-provided key.",
			BodyPath: "prompt_cache_key",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "prompt-cache-options",
			Usage:    "Options for prompt caching. Supported for `gpt-5.6` and later models. By default, OpenAI automatically chooses one implicit cache breakpoint. You can add explicit breakpoints to content blocks with `prompt_cache_breakpoint`. Each request can write up to four breakpoints. For cache matching, OpenAI considers up to the latest 80 breakpoints in the conversation, without a content-block lookback limit. Set `mode` to `explicit` to disable the implicit breakpoint. The `ttl` defaults to `30m`, which is currently the only supported value. See the [prompt caching guide](https://developers.openai.com/api/docs/guides/prompt-caching) for current details.",
			BodyPath: "prompt_cache_options",
		},
		&requestflag.Flag[*string]{
			Name:     "safety-identifier",
			Usage:    "Opaque caller-provided end-user identifier, scoped by the verified org. Match Responses' limit; this is never the authenticated user identity.",
			BodyPath: "safety_identifier",
		},
	},
	Action:          handleDecisionsCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"prompt-cache-options": {
		&requestflag.InnerFlag[*string]{
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
