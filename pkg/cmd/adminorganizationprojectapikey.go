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

var adminOrganizationProjectsAPIKeysRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieves an API key in the project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "api-key-id",
			Required:  true,
			PathParam: "api_key_id",
		},
	},
	Action:          handleAdminOrganizationProjectsAPIKeysRetrieve,
	HideHelpCommand: true,
}

var adminOrganizationProjectsAPIKeysList = cli.Command{
	Name:    "list",
	Usage:   "Returns a list of API keys in the project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
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
			Name:      "owner-project-access",
			Usage:     "Filter API keys by whether the owner currently has effective access to the project. Use `active` for owners with access, `inactive` for owners without access, or `any` for all enabled project API keys. If omitted, the endpoint applies its existing membership-based visibility rules, which may exclude some enabled keys.\n",
			QueryPath: "owner_project_access",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleAdminOrganizationProjectsAPIKeysList,
	HideHelpCommand: true,
}

var adminOrganizationProjectsAPIKeysDelete = cli.Command{
	Name:    "delete",
	Usage:   "Deletes an API key from the project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "api-key-id",
			Required:  true,
			PathParam: "api_key_id",
		},
	},
	Action:          handleAdminOrganizationProjectsAPIKeysDelete,
	HideHelpCommand: true,
}

func handleAdminOrganizationProjectsAPIKeysRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("api-key-id") && len(unusedArgs) > 0 {
		cmd.Set("api-key-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.Projects.APIKeys.Get(
		ctx,
		cmd.Value("project-id").(string),
		cmd.Value("api-key-id").(string),
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
		Title:          "admin:organization:projects:api-keys retrieve",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsAPIKeysList(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationProjectAPIKeyListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Admin.Organization.Projects.APIKeys.List(
			ctx,
			cmd.Value("project-id").(string),
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
			Title:          "admin:organization:projects:api-keys list",
			Transform:      transform,
		})
	} else {
		iter := client.Admin.Organization.Projects.APIKeys.ListAutoPaging(
			ctx,
			cmd.Value("project-id").(string),
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
			Title:          "admin:organization:projects:api-keys list",
			Transform:      transform,
		})
	}
}

func handleAdminOrganizationProjectsAPIKeysDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("api-key-id") && len(unusedArgs) > 0 {
		cmd.Set("api-key-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.Projects.APIKeys.Delete(
		ctx,
		cmd.Value("project-id").(string),
		cmd.Value("api-key-id").(string),
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
		Title:          "admin:organization:projects:api-keys delete",
		Transform:      transform,
	})
}
