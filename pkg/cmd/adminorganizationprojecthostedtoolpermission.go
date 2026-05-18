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

var adminOrganizationProjectsHostedToolPermissionsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Returns hosted tool permissions for a project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
	},
	Action:          handleAdminOrganizationProjectsHostedToolPermissionsRetrieve,
	HideHelpCommand: true,
}

var adminOrganizationProjectsHostedToolPermissionsUpdate = requestflag.WithInnerFlags(cli.Command{
	Name:    "update",
	Usage:   "Updates hosted tool permissions for a project.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "code-interpreter",
			Usage:    "The code interpreter permission update.",
			BodyPath: "code_interpreter",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "file-search",
			Usage:    "The file search permission update.",
			BodyPath: "file_search",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "image-generation",
			Usage:    "The image generation permission update.",
			BodyPath: "image_generation",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "mcp",
			Usage:    "The MCP permission update.",
			BodyPath: "mcp",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "web-search",
			Usage:    "The web search permission update.",
			BodyPath: "web_search",
		},
	},
	Action:          handleAdminOrganizationProjectsHostedToolPermissionsUpdate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"code-interpreter": {
		&requestflag.InnerFlag[bool]{
			Name:       "code-interpreter.enabled",
			Usage:      "Whether to enable the hosted tool for the project.",
			InnerField: "enabled",
		},
	},
	"file-search": {
		&requestflag.InnerFlag[bool]{
			Name:       "file-search.enabled",
			Usage:      "Whether to enable the hosted tool for the project.",
			InnerField: "enabled",
		},
	},
	"image-generation": {
		&requestflag.InnerFlag[bool]{
			Name:       "image-generation.enabled",
			Usage:      "Whether to enable the hosted tool for the project.",
			InnerField: "enabled",
		},
	},
	"mcp": {
		&requestflag.InnerFlag[bool]{
			Name:       "mcp.enabled",
			Usage:      "Whether to enable the hosted tool for the project.",
			InnerField: "enabled",
		},
	},
	"web-search": {
		&requestflag.InnerFlag[bool]{
			Name:       "web-search.enabled",
			Usage:      "Whether to enable the hosted tool for the project.",
			InnerField: "enabled",
		},
	},
})

func handleAdminOrganizationProjectsHostedToolPermissionsRetrieve(ctx context.Context, cmd *cli.Command) error {
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
	_, err = client.Admin.Organization.Projects.HostedToolPermissions.Get(ctx, cmd.Value("project-id").(string), options...)
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
		Title:          "admin:organization:projects:hosted-tool-permissions retrieve",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsHostedToolPermissionsUpdate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationProjectHostedToolPermissionUpdateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Projects.HostedToolPermissions.Update(
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
		Title:          "admin:organization:projects:hosted-tool-permissions update",
		Transform:      transform,
	})
}
