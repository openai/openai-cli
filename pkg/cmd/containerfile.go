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

var containersFilesCreate = cli.Command{
	Name:    "create",
	Usage:   "Create a Container File",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "container-id",
			Required:  true,
			PathParam: "container_id",
		},
		&requestflag.Flag[string]{
			Name:      "file",
			Usage:     "The File object (not file name) to be uploaded.\n",
			BodyPath:  "file",
			FileInput: true,
		},
		&requestflag.Flag[string]{
			Name:     "file-id",
			Usage:    "Name of the file to create.",
			BodyPath: "file_id",
		},
	},
	Action:          handleContainersFilesCreate,
	HideHelpCommand: true,
}

var containersFilesRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieve Container File",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "container-id",
			Required:  true,
			PathParam: "container_id",
		},
		&requestflag.Flag[string]{
			Name:      "file-id",
			Required:  true,
			PathParam: "file_id",
		},
	},
	Action:          handleContainersFilesRetrieve,
	HideHelpCommand: true,
}

var containersFilesList = cli.Command{
	Name:    "list",
	Usage:   "List Container files",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "container-id",
			Required:  true,
			PathParam: "container_id",
		},
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
	Action:          handleContainersFilesList,
	HideHelpCommand: true,
}

var containersFilesDelete = cli.Command{
	Name:    "delete",
	Usage:   "Delete Container File",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "container-id",
			Required:  true,
			PathParam: "container_id",
		},
		&requestflag.Flag[string]{
			Name:      "file-id",
			Required:  true,
			PathParam: "file_id",
		},
	},
	Action:          handleContainersFilesDelete,
	HideHelpCommand: true,
}

func handleContainersFilesCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("container-id") && len(unusedArgs) > 0 {
		cmd.Set("container-id", unusedArgs[0])
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

	params := openai.ContainerFileNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Containers.Files.New(
		ctx,
		cmd.Value("container-id").(string),
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
		Title:          "containers:files create",
		Transform:      transform,
	})
}

func handleContainersFilesRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("container-id") && len(unusedArgs) > 0 {
		cmd.Set("container-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("file-id") && len(unusedArgs) > 0 {
		cmd.Set("file-id", unusedArgs[0])
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
	_, err = client.Containers.Files.Get(
		ctx,
		cmd.Value("container-id").(string),
		cmd.Value("file-id").(string),
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
		Title:          "containers:files retrieve",
		Transform:      transform,
	})
}

func handleContainersFilesList(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("container-id") && len(unusedArgs) > 0 {
		cmd.Set("container-id", unusedArgs[0])
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

	params := openai.ContainerFileListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Containers.Files.List(
			ctx,
			cmd.Value("container-id").(string),
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
			Title:          "containers:files list",
			Transform:      transform,
		})
	} else {
		iter := client.Containers.Files.ListAutoPaging(
			ctx,
			cmd.Value("container-id").(string),
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
			Title:          "containers:files list",
			Transform:      transform,
		})
	}
}

func handleContainersFilesDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("container-id") && len(unusedArgs) > 0 {
		cmd.Set("container-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("file-id") && len(unusedArgs) > 0 {
		cmd.Set("file-id", unusedArgs[0])
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

	return client.Containers.Files.Delete(
		ctx,
		cmd.Value("container-id").(string),
		cmd.Value("file-id").(string),
		options...,
	)
}
