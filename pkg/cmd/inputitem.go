// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

var responsesInputItemsList = cli.Command{
	Name:    "list",
	Usage:   "Returns a list of input items for a given response.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "response-id",
			Required:  true,
			PathParam: "response_id",
		},
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "An item ID to list items after, used in pagination.\n",
			QueryPath: "after",
		},
		&requestflag.Flag[[]string]{
			Name:      "include",
			Usage:     "Additional fields to include in the response. See the `include`\nparameter for Response creation above for more information.\n",
			QueryPath: "include",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of objects to be returned. Limit can range between\n1 and 100, and the default is 20.\n",
			Default:   20,
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     "The order to return the input items in. Default is `desc`.\n- `asc`: Return the input items in ascending order.\n- `desc`: Return the input items in descending order.\n",
			QueryPath: "order",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleResponsesInputItemsList,
	HideHelpCommand: true,
}

func handleResponsesInputItemsList(ctx context.Context, cmd *cli.Command) error {
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

	params := responses.InputItemListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Responses.InputItems.List(
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
			Title:          "responses:input-items list",
			Transform:      transform,
		})
	} else {
		iter := client.Responses.InputItems.ListAutoPaging(
			ctx,
			cmd.Value("response-id").(string),
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
			Title:          "responses:input-items list",
			Transform:      transform,
		})
	}
}
