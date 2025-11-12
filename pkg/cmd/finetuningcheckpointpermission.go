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

var fineTuningCheckpointsPermissionsCreate = cli.Command{
	Name:    "create",
	Usage:   "**NOTE:** Calling this endpoint requires an [admin API key](../admin-api-keys).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "fine-tuned-model-checkpoint",
			Required:  true,
			PathParam: "fine_tuned_model_checkpoint",
		},
		&requestflag.Flag[[]string]{
			Name:     "project-id",
			Usage:    "The project identifiers to grant access to.",
			Required: true,
			BodyPath: "project_ids",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleFineTuningCheckpointsPermissionsCreate,
	HideHelpCommand: true,
}

var fineTuningCheckpointsPermissionsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "**NOTE:** This endpoint requires an [admin API key](../admin-api-keys).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "fine-tuned-model-checkpoint",
			Required:  true,
			PathParam: "fine_tuned_model_checkpoint",
		},
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "Identifier for the last permission ID from the previous pagination request.",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Number of permissions to retrieve.",
			Default:   10,
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     "The order in which to retrieve permissions.",
			Default:   "descending",
			QueryPath: "order",
		},
		&requestflag.Flag[string]{
			Name:      "project-id",
			Usage:     "The ID of the project to get permissions for.",
			QueryPath: "project_id",
		},
	},
	Action:          handleFineTuningCheckpointsPermissionsRetrieve,
	HideHelpCommand: true,
}

var fineTuningCheckpointsPermissionsList = cli.Command{
	Name:    "list",
	Usage:   "**NOTE:** This endpoint requires an [admin API key](../admin-api-keys).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "fine-tuned-model-checkpoint",
			Required:  true,
			PathParam: "fine_tuned_model_checkpoint",
		},
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "Identifier for the last permission ID from the previous pagination request.",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Number of permissions to retrieve.",
			Default:   10,
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     "The order in which to retrieve permissions.",
			Default:   "descending",
			QueryPath: "order",
		},
		&requestflag.Flag[string]{
			Name:      "project-id",
			Usage:     "The ID of the project to get permissions for.",
			QueryPath: "project_id",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleFineTuningCheckpointsPermissionsList,
	HideHelpCommand: true,
}

var fineTuningCheckpointsPermissionsDelete = cli.Command{
	Name:    "delete",
	Usage:   "**NOTE:** This endpoint requires an [admin API key](../admin-api-keys).",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "fine-tuned-model-checkpoint",
			Required:  true,
			PathParam: "fine_tuned_model_checkpoint",
		},
		&requestflag.Flag[string]{
			Name:      "permission-id",
			Required:  true,
			PathParam: "permission_id",
		},
	},
	Action:          handleFineTuningCheckpointsPermissionsDelete,
	HideHelpCommand: true,
}

func handleFineTuningCheckpointsPermissionsCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("fine-tuned-model-checkpoint") && len(unusedArgs) > 0 {
		cmd.Set("fine-tuned-model-checkpoint", unusedArgs[0])
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

	params := openai.FineTuningCheckpointPermissionNewParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.FineTuning.Checkpoints.Permissions.New(
			ctx,
			cmd.Value("fine-tuned-model-checkpoint").(string),
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
			Title:          "fine-tuning:checkpoints:permissions create",
			Transform:      transform,
		})
	} else {
		iter := client.FineTuning.Checkpoints.Permissions.NewAutoPaging(
			ctx,
			cmd.Value("fine-tuned-model-checkpoint").(string),
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
			Title:          "fine-tuning:checkpoints:permissions create",
			Transform:      transform,
		})
	}
}

func handleFineTuningCheckpointsPermissionsRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("fine-tuned-model-checkpoint") && len(unusedArgs) > 0 {
		cmd.Set("fine-tuned-model-checkpoint", unusedArgs[0])
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

	params := openai.FineTuningCheckpointPermissionGetParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.FineTuning.Checkpoints.Permissions.Get(
		ctx,
		cmd.Value("fine-tuned-model-checkpoint").(string),
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
		Title:          "fine-tuning:checkpoints:permissions retrieve",
		Transform:      transform,
	})
}

func handleFineTuningCheckpointsPermissionsList(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("fine-tuned-model-checkpoint") && len(unusedArgs) > 0 {
		cmd.Set("fine-tuned-model-checkpoint", unusedArgs[0])
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

	params := openai.FineTuningCheckpointPermissionListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.FineTuning.Checkpoints.Permissions.List(
			ctx,
			cmd.Value("fine-tuned-model-checkpoint").(string),
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
			Title:          "fine-tuning:checkpoints:permissions list",
			Transform:      transform,
		})
	} else {
		iter := client.FineTuning.Checkpoints.Permissions.ListAutoPaging(
			ctx,
			cmd.Value("fine-tuned-model-checkpoint").(string),
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
			Title:          "fine-tuning:checkpoints:permissions list",
			Transform:      transform,
		})
	}
}

func handleFineTuningCheckpointsPermissionsDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("fine-tuned-model-checkpoint") && len(unusedArgs) > 0 {
		cmd.Set("fine-tuned-model-checkpoint", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("permission-id") && len(unusedArgs) > 0 {
		cmd.Set("permission-id", unusedArgs[0])
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
	_, err = client.FineTuning.Checkpoints.Permissions.Delete(
		ctx,
		cmd.Value("fine-tuned-model-checkpoint").(string),
		cmd.Value("permission-id").(string),
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
		Title:          "fine-tuning:checkpoints:permissions delete",
		Transform:      transform,
	})
}
