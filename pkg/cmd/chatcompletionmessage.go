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

var chatCompletionsMessagesList = cli.Command{
	Name:    "list",
	Usage:   "Get the messages in a stored chat completion. Only Chat Completions that have\nbeen created with the `store` parameter set to `true` will be returned.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "completion-id",
			Required:  true,
			PathParam: "completion_id",
		},
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "Identifier for the last message from the previous pagination request.",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Number of messages to retrieve.",
			Default:   20,
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     "Sort order for messages by timestamp. Use `asc` for ascending order or `desc` for descending order. Defaults to `asc`.",
			Default:   "asc",
			QueryPath: "order",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleChatCompletionsMessagesList,
	HideHelpCommand: true,
}

func handleChatCompletionsMessagesList(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("completion-id") && len(unusedArgs) > 0 {
		cmd.Set("completion-id", unusedArgs[0])
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

	params := openai.ChatCompletionMessageListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Chat.Completions.Messages.List(
			ctx,
			cmd.Value("completion-id").(string),
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
			Title:          "chat:completions:messages list",
			Transform:      transform,
		})
	} else {
		iter := client.Chat.Completions.Messages.ListAutoPaging(
			ctx,
			cmd.Value("completion-id").(string),
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
			Title:          "chat:completions:messages list",
			Transform:      transform,
		})
	}
}
