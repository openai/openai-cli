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

var adminOrganizationGroupsUsersCreate = cli.Command{
	Name:    "create",
	Usage:   "Adds a user to a group.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "group-id",
			Required:  true,
			PathParam: "group_id",
		},
		&requestflag.Flag[string]{
			Name:     "user-id",
			Usage:    "Identifier of the user to add to the group.",
			Required: true,
			BodyPath: "user_id",
		},
	},
	Action:          handleAdminOrganizationGroupsUsersCreate,
	HideHelpCommand: true,
}

var adminOrganizationGroupsUsersList = cli.Command{
	Name:    "list",
	Usage:   "Lists the users assigned to a group.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "group-id",
			Required:  true,
			PathParam: "group_id",
		},
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "A cursor for use in pagination. Provide the ID of the last user from the previous list response to retrieve the next page.\n",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of users to be returned. Limit can range between 0 and 1000, and the default is 100.\n",
			Default:   100,
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     "Specifies the sort order of users in the list.",
			Default:   "desc",
			QueryPath: "order",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleAdminOrganizationGroupsUsersList,
	HideHelpCommand: true,
}

var adminOrganizationGroupsUsersDelete = cli.Command{
	Name:    "delete",
	Usage:   "Removes a user from a group.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "group-id",
			Required:  true,
			PathParam: "group_id",
		},
		&requestflag.Flag[string]{
			Name:      "user-id",
			Required:  true,
			PathParam: "user_id",
		},
	},
	Action:          handleAdminOrganizationGroupsUsersDelete,
	HideHelpCommand: true,
}

func handleAdminOrganizationGroupsUsersCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("group-id") && len(unusedArgs) > 0 {
		cmd.Set("group-id", unusedArgs[0])
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

	params := openai.AdminOrganizationGroupUserNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Groups.Users.New(
		ctx,
		cmd.Value("group-id").(string),
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
		Title:          "admin:organization:groups:users create",
		Transform:      transform,
	})
}

func handleAdminOrganizationGroupsUsersList(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("group-id") && len(unusedArgs) > 0 {
		cmd.Set("group-id", unusedArgs[0])
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

	params := openai.AdminOrganizationGroupUserListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Admin.Organization.Groups.Users.List(
			ctx,
			cmd.Value("group-id").(string),
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
			Title:          "admin:organization:groups:users list",
			Transform:      transform,
		})
	} else {
		iter := client.Admin.Organization.Groups.Users.ListAutoPaging(
			ctx,
			cmd.Value("group-id").(string),
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
			Title:          "admin:organization:groups:users list",
			Transform:      transform,
		})
	}
}

func handleAdminOrganizationGroupsUsersDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("group-id") && len(unusedArgs) > 0 {
		cmd.Set("group-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("user-id") && len(unusedArgs) > 0 {
		cmd.Set("user-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.Groups.Users.Delete(
		ctx,
		cmd.Value("group-id").(string),
		cmd.Value("user-id").(string),
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
		Title:          "admin:organization:groups:users delete",
		Transform:      transform,
	})
}
