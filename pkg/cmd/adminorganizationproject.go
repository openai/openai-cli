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

var adminOrganizationProjectsCreate = cli.Command{
	Name:    "create",
	Usage:   "Create a new project in the organization. Projects can be created and archived,\nbut cannot be deleted.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "name",
			Usage:    "The friendly name of the project, this name appears in reports.",
			Required: true,
			BodyPath: "name",
		},
		&requestflag.Flag[*string]{
			Name:     "external-key-id",
			Usage:    "External key ID to associate with the project.",
			BodyPath: "external_key_id",
		},
		&requestflag.Flag[*string]{
			Name:     "geography",
			Usage:    "Create the project with the specified data residency region. Your organization must have access to Data residency functionality in order to use. See [data residency controls](https://platform.openai.com/docs/guides/your-data#data-residency-controls) to review the functionality and limitations of setting this field.",
			BodyPath: "geography",
		},
	},
	Action:          handleAdminOrganizationProjectsCreate,
	HideHelpCommand: true,
}

var adminOrganizationProjectsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves a project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
	},
	Action:          handleAdminOrganizationProjectsRetrieve,
	HideHelpCommand: true,
}

var adminOrganizationProjectsUpdate = cli.Command{
	Name:    "update",
	Usage:   "Modifies a project in the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[*string]{
			Name:     "external-key-id",
			Usage:    "External key ID to associate with the project.",
			BodyPath: "external_key_id",
		},
		&requestflag.Flag[*string]{
			Name:     "geography",
			Usage:    "Geography for the project.",
			BodyPath: "geography",
		},
		&requestflag.Flag[*string]{
			Name:     "name",
			Usage:    "The updated name of the project, this name appears in reports.",
			BodyPath: "name",
		},
	},
	Action:          handleAdminOrganizationProjectsUpdate,
	HideHelpCommand: true,
}

var adminOrganizationProjectsList = cli.Command{
	Name:    "list",
	Usage:   "Returns a list of projects.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "A cursor for use in pagination. `after` is an object ID that defines your place in the list. For instance, if you make a list request and receive 100 objects, ending with obj_foo, your subsequent call can include after=obj_foo in order to fetch the next page of the list.\n",
			QueryPath: "after",
		},
		&requestflag.Flag[bool]{
			Name:      "include-archived",
			Usage:     "If `true` returns all projects including those that have been `archived`. Archived projects are not included by default.",
			Default:   false,
			QueryPath: "include_archived",
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
	Action:          handleAdminOrganizationProjectsList,
	HideHelpCommand: true,
}

var adminOrganizationProjectsArchive = cli.Command{
	Name:    "archive",
	Usage:   "Archives a project in the organization. Archived projects cannot be used or\nupdated.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
	},
	Action:          handleAdminOrganizationProjectsArchive,
	HideHelpCommand: true,
}

func handleAdminOrganizationProjectsCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationProjectNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Projects.New(ctx, params, options...)
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
		Title:          "admin:organization:projects create",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.Projects.Get(ctx, cmd.Value("project-id").(string), options...)
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
		Title:          "admin:organization:projects retrieve",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsUpdate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
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

	params := openai.AdminOrganizationProjectUpdateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Projects.Update(
		ctx,
		cmd.Value("project-id").(string),
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
		Title:          "admin:organization:projects update",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsList(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationProjectListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Admin.Organization.Projects.List(ctx, params, options...)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "admin:organization:projects list",
			Transform:      transform,
		})
	} else {
		iter := client.Admin.Organization.Projects.ListAutoPaging(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "admin:organization:projects list",
			Transform:      transform,
		})
	}
}

func handleAdminOrganizationProjectsArchive(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.Projects.Archive(ctx, cmd.Value("project-id").(string), options...)
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
		Title:          "admin:organization:projects archive",
		Transform:      transform,
	})
}
