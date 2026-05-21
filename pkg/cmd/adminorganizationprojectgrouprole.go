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

var adminOrganizationProjectsGroupsRolesCreate = cli.Command{
	Name:    "create",
	Usage:   "Assigns a project role to a group within a project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "group-id",
			Required:  true,
			PathParam: "group_id",
		},
		&requestflag.Flag[string]{
			Name:     "role-id",
			Usage:    "Identifier of the role to assign.",
			Required: true,
			BodyPath: "role_id",
		},
	},
	Action:          handleAdminOrganizationProjectsGroupsRolesCreate,
	HideHelpCommand: true,
}

var adminOrganizationProjectsGroupsRolesRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves a project role assigned to a group.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "group-id",
			Required:  true,
			PathParam: "group_id",
		},
		&requestflag.Flag[string]{
			Name:      "role-id",
			Required:  true,
			PathParam: "role_id",
		},
	},
	Action:          handleAdminOrganizationProjectsGroupsRolesRetrieve,
	HideHelpCommand: true,
}

var adminOrganizationProjectsGroupsRolesList = cli.Command{
	Name:    "list",
	Usage:   "Lists the project roles assigned to a group within a project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "group-id",
			Required:  true,
			PathParam: "group_id",
		},
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "Cursor for pagination. Provide the value from the previous response's `next` field to continue listing project roles.",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of project role assignments to return.",
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     "Sort order for the returned project roles.",
			QueryPath: "order",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleAdminOrganizationProjectsGroupsRolesList,
	HideHelpCommand: true,
}

var adminOrganizationProjectsGroupsRolesDelete = cli.Command{
	Name:    "delete",
	Usage:   "Unassigns a project role from a group within a project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "group-id",
			Required:  true,
			PathParam: "group_id",
		},
		&requestflag.Flag[string]{
			Name:      "role-id",
			Required:  true,
			PathParam: "role_id",
		},
	},
	Action:          handleAdminOrganizationProjectsGroupsRolesDelete,
	HideHelpCommand: true,
}

func handleAdminOrganizationProjectsGroupsRolesCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
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

	params := openai.AdminOrganizationProjectGroupRoleNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Projects.Groups.Roles.New(
		ctx,
		cmd.Value("project-id").(string),
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
		Title:          "admin:organization:projects:groups:roles create",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsGroupsRolesRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("group-id") && len(unusedArgs) > 0 {
		cmd.Set("group-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("role-id") && len(unusedArgs) > 0 {
		cmd.Set("role-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.Projects.Groups.Roles.Get(
		ctx,
		cmd.Value("project-id").(string),
		cmd.Value("group-id").(string),
		cmd.Value("role-id").(string),
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
		Title:          "admin:organization:projects:groups:roles retrieve",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsGroupsRolesList(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
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

	params := openai.AdminOrganizationProjectGroupRoleListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Admin.Organization.Projects.Groups.Roles.List(
			ctx,
			cmd.Value("project-id").(string),
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
			Title:          "admin:organization:projects:groups:roles list",
			Transform:      transform,
		})
	} else {
		iter := client.Admin.Organization.Projects.Groups.Roles.ListAutoPaging(
			ctx,
			cmd.Value("project-id").(string),
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
			Title:          "admin:organization:projects:groups:roles list",
			Transform:      transform,
		})
	}
}

func handleAdminOrganizationProjectsGroupsRolesDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("group-id") && len(unusedArgs) > 0 {
		cmd.Set("group-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("role-id") && len(unusedArgs) > 0 {
		cmd.Set("role-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.Projects.Groups.Roles.Delete(
		ctx,
		cmd.Value("project-id").(string),
		cmd.Value("group-id").(string),
		cmd.Value("role-id").(string),
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
		Title:          "admin:organization:projects:groups:roles delete",
		Transform:      transform,
	})
}
