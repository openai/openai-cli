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

var adminOrganizationProjectsModelPermissionsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Returns model permissions for a project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
	},
	Action:          handleAdminOrganizationProjectsModelPermissionsRetrieve,
	HideHelpCommand: true,
}

var adminOrganizationProjectsModelPermissionsUpdate = cli.Command{
	Name:    "update",
	Usage:   "Updates model permissions for a project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:     "mode",
			Usage:    "The model permissions mode to apply.",
			Required: true,
			BodyPath: "mode",
		},
		&requestflag.Flag[[]string]{
			Name:     "model-id",
			Usage:    "The model IDs included in this permissions policy.",
			Required: true,
			BodyPath: "model_ids",
		},
	},
	Action:          handleAdminOrganizationProjectsModelPermissionsUpdate,
	HideHelpCommand: true,
}

var adminOrganizationProjectsModelPermissionsDelete = cli.Command{
	Name:    "delete",
	Usage:   "Deletes model permissions for a project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
	},
	Action:          handleAdminOrganizationProjectsModelPermissionsDelete,
	HideHelpCommand: true,
}

func handleAdminOrganizationProjectsModelPermissionsRetrieve(ctx context.Context, cmd *cli.Command) error {
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
	_, err = client.Admin.Organization.Projects.ModelPermissions.Get(ctx, cmd.Value("project-id").(string), options...)
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
		Title:          "admin:organization:projects:model-permissions retrieve",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsModelPermissionsUpdate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationProjectModelPermissionUpdateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Projects.ModelPermissions.Update(
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
		Title:          "admin:organization:projects:model-permissions update",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsModelPermissionsDelete(ctx context.Context, cmd *cli.Command) error {
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
	_, err = client.Admin.Organization.Projects.ModelPermissions.Delete(ctx, cmd.Value("project-id").(string), options...)
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
		Title:          "admin:organization:projects:model-permissions delete",
		Transform:      transform,
	})
}
