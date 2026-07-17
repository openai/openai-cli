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

var adminOrganizationProjectsServiceAccountsAPIKeysCreate = cli.Command{
	Name:    "create",
	Usage:   "Creates an API key for a service account in the project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Usage:     "The ID of the project.",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "service-account-id",
			Usage:     "The ID of the service account.",
			Required:  true,
			PathParam: "service_account_id",
		},
		&requestflag.Flag[string]{
			Name:     "name",
			Usage:    "API key name.",
			BodyPath: "name",
		},
		&requestflag.Flag[[]string]{
			Name:     "scope",
			Usage:    "API key scopes.",
			BodyPath: "scopes",
		},
	},
	Action:          handleAdminOrganizationProjectsServiceAccountsAPIKeysCreate,
	HideHelpCommand: true,
}

func handleAdminOrganizationProjectsServiceAccountsAPIKeysCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("service-account-id") && len(unusedArgs) > 0 {
		cmd.Set("service-account-id", unusedArgs[0])
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

	params := openai.AdminOrganizationProjectServiceAccountAPIKeyNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Projects.ServiceAccounts.APIKeys.New(
		ctx,
		cmd.Value("project-id").(string),
		cmd.Value("service-account-id").(string),
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
		Title:          "admin:organization:projects:service-accounts:api-keys create",
		Transform:      transform,
	})
}
