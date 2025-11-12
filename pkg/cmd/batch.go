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

var batchesCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Creates and executes a batch from an uploaded file of requests",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "completion-window",
			Usage:    "The time frame within which the batch should be processed. Currently only `24h` is supported.",
			Required: true,
			BodyPath: "completion_window",
		},
		&requestflag.Flag[string]{
			Name:     "endpoint",
			Usage:    "The endpoint to be used for all requests in the batch. Currently `/v1/responses`, `/v1/chat/completions`, `/v1/embeddings`, `/v1/completions`, `/v1/moderations`, `/v1/images/generations`, `/v1/images/edits`, and `/v1/videos` are supported. Note that `/v1/embeddings` batches are also restricted to a maximum of 50,000 embedding inputs across all requests in the batch.",
			Required: true,
			BodyPath: "endpoint",
		},
		&requestflag.Flag[string]{
			Name:     "input-file-id",
			Usage:    "The ID of an uploaded file that contains requests for the new batch.\n\nSee [upload file](https://platform.openai.com/docs/api-reference/files/create) for how to upload a file.\n\nYour input file must be formatted as a [JSONL file](https://platform.openai.com/docs/api-reference/batch/request-input), and must be uploaded with the purpose `batch`. The file can contain up to 50,000 requests, and can be up to 200 MB in size.\n",
			Required: true,
			BodyPath: "input_file_id",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "metadata",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			BodyPath: "metadata",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "output-expires-after",
			Usage:    "The expiration policy for the output and/or error file that are generated for a batch.",
			BodyPath: "output_expires_after",
		},
	},
	Action:          handleBatchesCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"output-expires-after": {
		&requestflag.InnerFlag[string]{
			Name:       "output-expires-after.anchor",
			Usage:      "Anchor timestamp after which the expiration policy applies. Supported anchors: `created_at`. Note that the anchor is the file creation time, not the time the batch is created.",
			InnerField: "anchor",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "output-expires-after.seconds",
			Usage:      "The number of seconds after the anchor time that the file will expire. Must be between 3600 (1 hour) and 2592000 (30 days).",
			InnerField: "seconds",
		},
	},
})

var batchesRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves a batch.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "batch-id",
			Required:  true,
			PathParam: "batch_id",
		},
	},
	Action:          handleBatchesRetrieve,
	HideHelpCommand: true,
}

var batchesList = cli.Command{
	Name:    "list",
	Usage:   "List your organization's batches.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "A cursor for use in pagination. `after` is an object ID that defines your place in the list. For instance, if you make a list request and receive 100 objects, ending with obj_foo, your subsequent call can include after=obj_foo in order to fetch the next page of the list.\n",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of objects to be returned. Limit can range between 1 and 100, and the default is 20.\n",
			Default:   20,
			QueryPath: "limit",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleBatchesList,
	HideHelpCommand: true,
}

var batchesCancel = cli.Command{
	Name:    "cancel",
	Usage:   "Cancels an in-progress batch. The batch will be in status `cancelling` for up to\n10 minutes, before changing to `cancelled`, where it will have partial results\n(if any) available in the output file.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "batch-id",
			Required:  true,
			PathParam: "batch_id",
		},
	},
	Action:          handleBatchesCancel,
	HideHelpCommand: true,
}

func handleBatchesCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.BatchNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Batches.New(ctx, params, options...)
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
		Title:          "batches create",
		Transform:      transform,
	})
}

func handleBatchesRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("batch-id") && len(unusedArgs) > 0 {
		cmd.Set("batch-id", unusedArgs[0])
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
	_, err = client.Batches.Get(ctx, cmd.Value("batch-id").(string), options...)
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
		Title:          "batches retrieve",
		Transform:      transform,
	})
}

func handleBatchesList(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.BatchListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Batches.List(ctx, params, options...)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "batches list",
			Transform:      transform,
		})
	} else {
		iter := client.Batches.ListAutoPaging(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "batches list",
			Transform:      transform,
		})
	}
}

func handleBatchesCancel(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("batch-id") && len(unusedArgs) > 0 {
		cmd.Set("batch-id", unusedArgs[0])
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
	_, err = client.Batches.Cancel(ctx, cmd.Value("batch-id").(string), options...)
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
		Title:          "batches cancel",
		Transform:      transform,
	})
}
