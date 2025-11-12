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

var vectorStoresCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Create a vector store.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[map[string]any]{
			Name:     "chunking-strategy",
			Usage:    "The chunking strategy used to chunk the file(s). If not set, will use the `auto` strategy. Only applicable if `file_ids` is non-empty.",
			BodyPath: "chunking_strategy",
		},
		&requestflag.Flag[string]{
			Name:     "description",
			Usage:    "A description for the vector store. Can be used to describe the vector store's purpose.",
			BodyPath: "description",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "expires-after",
			Usage:    "The expiration policy for a vector store.",
			BodyPath: "expires_after",
		},
		&requestflag.Flag[[]string]{
			Name:     "file-id",
			Usage:    "A list of [File](https://platform.openai.com/docs/api-reference/files) IDs that the vector store should use. Useful for tools like `file_search` that can access files.",
			BodyPath: "file_ids",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "metadata",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			BodyPath: "metadata",
		},
		&requestflag.Flag[string]{
			Name:     "name",
			Usage:    "The name of the vector store.",
			BodyPath: "name",
		},
	},
	Action:          handleVectorStoresCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"expires-after": {
		&requestflag.InnerFlag[string]{
			Name:       "expires-after.anchor",
			Usage:      "Anchor timestamp after which the expiration policy applies. Supported anchors: `last_active_at`.",
			InnerField: "anchor",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "expires-after.days",
			Usage:      "The number of days after the anchor time that the vector store will expire.",
			InnerField: "days",
		},
	},
})

var vectorStoresRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves a vector store.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "vector-store-id",
			Required:  true,
			PathParam: "vector_store_id",
		},
	},
	Action:          handleVectorStoresRetrieve,
	HideHelpCommand: true,
}

var vectorStoresUpdate = requestflag.WithInnerFlags(cli.Command{
	Name:    "update",
	Usage:   "Modifies a vector store.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "vector-store-id",
			Required:  true,
			PathParam: "vector_store_id",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "expires-after",
			Usage:    "The expiration policy for a vector store.",
			BodyPath: "expires_after",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "metadata",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			BodyPath: "metadata",
		},
		&requestflag.Flag[*string]{
			Name:     "name",
			Usage:    "The name of the vector store.",
			BodyPath: "name",
		},
	},
	Action:          handleVectorStoresUpdate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"expires-after": {
		&requestflag.InnerFlag[string]{
			Name:       "expires-after.anchor",
			Usage:      "Anchor timestamp after which the expiration policy applies. Supported anchors: `last_active_at`.",
			InnerField: "anchor",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "expires-after.days",
			Usage:      "The number of days after the anchor time that the vector store will expire.",
			InnerField: "days",
		},
	},
})

var vectorStoresList = cli.Command{
	Name:    "list",
	Usage:   "Returns a list of vector stores.",
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
	Action:          handleVectorStoresList,
	HideHelpCommand: true,
}

var vectorStoresDelete = cli.Command{
	Name:    "delete",
	Usage:   "Delete a vector store.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "vector-store-id",
			Required:  true,
			PathParam: "vector_store_id",
		},
	},
	Action:          handleVectorStoresDelete,
	HideHelpCommand: true,
}

var vectorStoresSearch = requestflag.WithInnerFlags(cli.Command{
	Name:    "search",
	Usage:   "Search a vector store for relevant chunks based on a query and file attributes\nfilter.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "vector-store-id",
			Required:  true,
			PathParam: "vector_store_id",
		},
		&requestflag.Flag[any]{
			Name:     "query",
			Usage:    "A query string for a search",
			Required: true,
			BodyPath: "query",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "filters",
			Usage:    "A filter to apply based on file attributes.",
			BodyPath: "filters",
		},
		&requestflag.Flag[int64]{
			Name:     "max-num-results",
			Usage:    "The maximum number of results to return. This number should be between 1 and 50 inclusive.",
			Default:  10,
			BodyPath: "max_num_results",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "ranking-options",
			Usage:    "Ranking options for search.",
			BodyPath: "ranking_options",
		},
		&requestflag.Flag[bool]{
			Name:     "rewrite-query",
			Usage:    "Whether to rewrite the natural language query for vector search.",
			Default:  false,
			BodyPath: "rewrite_query",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleVectorStoresSearch,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"ranking-options": {
		&requestflag.InnerFlag[string]{
			Name:       "ranking-options.ranker",
			Usage:      "Enable re-ranking; set to `none` to disable, which can help reduce latency.",
			InnerField: "ranker",
		},
		&requestflag.InnerFlag[float64]{
			Name:       "ranking-options.score-threshold",
			InnerField: "score_threshold",
		},
	},
})

func handleVectorStoresCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.VectorStoreNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.VectorStores.New(ctx, params, options...)
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
		Title:          "vector-stores create",
		Transform:      transform,
	})
}

func handleVectorStoresRetrieve(ctx context.Context, cmd *cli.Command) error {
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
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.VectorStores.Get(ctx, cmd.Value("vector-store-id").(string), options...)
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
		Title:          "vector-stores retrieve",
		Transform:      transform,
	})
}

func handleVectorStoresUpdate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.VectorStoreUpdateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.VectorStores.Update(
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
		Title:          "vector-stores update",
		Transform:      transform,
	})
}

func handleVectorStoresList(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.VectorStoreListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.VectorStores.List(ctx, params, options...)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "vector-stores list",
			Transform:      transform,
		})
	} else {
		iter := client.VectorStores.ListAutoPaging(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "vector-stores list",
			Transform:      transform,
		})
	}
}

func handleVectorStoresDelete(ctx context.Context, cmd *cli.Command) error {
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
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.VectorStores.Delete(ctx, cmd.Value("vector-store-id").(string), options...)
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
		Title:          "vector-stores delete",
		Transform:      transform,
	})
}

func handleVectorStoresSearch(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.VectorStoreSearchParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.VectorStores.Search(
			ctx,
			cmd.Value("vector-store-id").(string),
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
			Title:          "vector-stores search",
			Transform:      transform,
		})
	} else {
		iter := client.VectorStores.SearchAutoPaging(
			ctx,
			cmd.Value("vector-store-id").(string),
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
			Title:          "vector-stores search",
			Transform:      transform,
		})
	}
}
