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

var vectorStoresFileBatchesCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Create a vector store file batch.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "vector-store-id",
			Required:  true,
			PathParam: "vector_store_id",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "attributes",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard. Keys are strings\nwith a maximum length of 64 characters. Values are strings with a maximum\nlength of 512 characters, booleans, or numbers.\n",
			BodyPath: "attributes",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "chunking-strategy",
			Usage:    "The chunking strategy used to chunk the file(s). If not set, will use the `auto` strategy. Only applicable if `file_ids` is non-empty.",
			BodyPath: "chunking_strategy",
		},
		&requestflag.Flag[[]string]{
			Name:     "file-id",
			Usage:    "A list of [File](https://platform.openai.com/docs/api-reference/files) IDs that the vector store should use. Useful for tools like `file_search` that can access files.  If `attributes` or `chunking_strategy` are provided, they will be  applied to all files in the batch. The maximum batch size is 2000 files. This endpoint is recommended for multi-file ingestion and helps reduce per-vector-store write request pressure. Mutually exclusive with `files`.",
			BodyPath: "file_ids",
		},
		&requestflag.Flag[[]map[string]any]{
			Name:     "file",
			Usage:    "A list of objects that each include a `file_id` plus optional `attributes` or `chunking_strategy`. Use this when you need to override metadata for specific files. The global `attributes` or `chunking_strategy` will be ignored and must be specified for each file. The maximum batch size is 2000 files. This endpoint is recommended for multi-file ingestion and helps reduce per-vector-store write request pressure. Mutually exclusive with `file_ids`.",
			BodyPath: "files",
		},
	},
	Action:          handleVectorStoresFileBatchesCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"file": {
		&requestflag.InnerFlag[string]{
			Name:       "file.file-id",
			Usage:      "A [File](https://platform.openai.com/docs/api-reference/files) ID that the vector store should use. Useful for tools like `file_search` that can access files. For multi-file ingestion, we recommend [`file_batches`](https://platform.openai.com/docs/api-reference/vector-stores-file-batches/createBatch) to minimize per-vector-store write requests.",
			InnerField: "file_id",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "file.attributes",
			Usage:      "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard. Keys are strings\nwith a maximum length of 64 characters. Values are strings with a maximum\nlength of 512 characters, booleans, or numbers.\n",
			InnerField: "attributes",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "file.chunking-strategy",
			Usage:      "The chunking strategy used to chunk the file(s). If not set, will use the `auto` strategy. Only applicable if `file_ids` is non-empty.",
			InnerField: "chunking_strategy",
		},
	},
})

var vectorStoresFileBatchesRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves a vector store file batch.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "vector-store-id",
			Required:  true,
			PathParam: "vector_store_id",
		},
		&requestflag.Flag[string]{
			Name:      "batch-id",
			Required:  true,
			PathParam: "batch_id",
		},
	},
	Action:          handleVectorStoresFileBatchesRetrieve,
	HideHelpCommand: true,
}

var vectorStoresFileBatchesCancel = cli.Command{
	Name:    "cancel",
	Usage:   "Cancel a vector store file batch. This attempts to cancel the processing of\nfiles in this batch as soon as possible.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "vector-store-id",
			Required:  true,
			PathParam: "vector_store_id",
		},
		&requestflag.Flag[string]{
			Name:      "batch-id",
			Required:  true,
			PathParam: "batch_id",
		},
	},
	Action:          handleVectorStoresFileBatchesCancel,
	HideHelpCommand: true,
}

var vectorStoresFileBatchesListFiles = cli.Command{
	Name:    "list-files",
	Usage:   "Returns a list of vector store files in a batch.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "vector-store-id",
			Required:  true,
			PathParam: "vector_store_id",
		},
		&requestflag.Flag[string]{
			Name:      "batch-id",
			Required:  true,
			PathParam: "batch_id",
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
		&requestflag.Flag[string]{
			Name:      "filter",
			Usage:     "Filter by file status. One of `in_progress`, `completed`, `failed`, `cancelled`.",
			QueryPath: "filter",
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
	Action:          handleVectorStoresFileBatchesListFiles,
	HideHelpCommand: true,
}

func handleVectorStoresFileBatchesCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("vector-store-id") && len(unusedArgs) > 0 {
		cmd.Set("vector-store-id", unusedArgs[0])
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

	params := openai.VectorStoreFileBatchNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.VectorStores.FileBatches.New(
		ctx,
		cmd.Value("vector-store-id").(string),
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
		Title:          "vector-stores:file-batches create",
		Transform:      transform,
	})
}

func handleVectorStoresFileBatchesRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("vector-store-id") && len(unusedArgs) > 0 {
		cmd.Set("vector-store-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
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
	_, err = client.VectorStores.FileBatches.Get(
		ctx,
		cmd.Value("vector-store-id").(string),
		cmd.Value("batch-id").(string),
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
		Title:          "vector-stores:file-batches retrieve",
		Transform:      transform,
	})
}

func handleVectorStoresFileBatchesCancel(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("vector-store-id") && len(unusedArgs) > 0 {
		cmd.Set("vector-store-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
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
	_, err = client.VectorStores.FileBatches.Cancel(
		ctx,
		cmd.Value("vector-store-id").(string),
		cmd.Value("batch-id").(string),
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
		Title:          "vector-stores:file-batches cancel",
		Transform:      transform,
	})
}

func handleVectorStoresFileBatchesListFiles(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("vector-store-id") && len(unusedArgs) > 0 {
		cmd.Set("vector-store-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
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

	params := openai.VectorStoreFileBatchListFilesParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.VectorStores.FileBatches.ListFiles(
			ctx,
			cmd.Value("vector-store-id").(string),
			cmd.Value("batch-id").(string),
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
			Title:          "vector-stores:file-batches list-files",
			Transform:      transform,
		})
	} else {
		iter := client.VectorStores.FileBatches.ListFilesAutoPaging(
			ctx,
			cmd.Value("vector-store-id").(string),
			cmd.Value("batch-id").(string),
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
			Title:          "vector-stores:file-batches list-files",
			Transform:      transform,
		})
	}
}
