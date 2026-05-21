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

var adminOrganizationProjectsSpendAlertsCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Creates a project spend alert.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:     "currency",
			Usage:    "The currency for the threshold amount.",
			Required: true,
			BodyPath: "currency",
		},
		&requestflag.Flag[string]{
			Name:     "interval",
			Usage:    "The time interval for evaluating spend against the threshold.",
			Required: true,
			BodyPath: "interval",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "notification-channel",
			Usage:    "Email notification settings for a spend alert.",
			Required: true,
			BodyPath: "notification_channel",
		},
		&requestflag.Flag[int64]{
			Name:     "threshold-amount",
			Usage:    "The alert threshold amount, in cents.",
			Required: true,
			BodyPath: "threshold_amount",
		},
	},
	Action:          handleAdminOrganizationProjectsSpendAlertsCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"notification-channel": {
		&requestflag.InnerFlag[[]string]{
			Name:       "notification-channel.recipients",
			Usage:      "Email addresses that receive the spend alert notification.",
			InnerField: "recipients",
		},
		&requestflag.InnerFlag[string]{
			Name:       "notification-channel.type",
			Usage:      "The notification channel type. Currently only `email` is supported.",
			InnerField: "type",
		},
		&requestflag.InnerFlag[*string]{
			Name:       "notification-channel.subject-prefix",
			Usage:      "Optional subject prefix for alert emails.",
			InnerField: "subject_prefix",
		},
	},
})

var adminOrganizationProjectsSpendAlertsUpdate = requestflag.WithInnerFlags(cli.Command{
	Name:    "update",
	Usage:   "Updates a project spend alert.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "alert-id",
			Required:  true,
			PathParam: "alert_id",
		},
		&requestflag.Flag[string]{
			Name:     "currency",
			Usage:    "The currency for the threshold amount.",
			Required: true,
			BodyPath: "currency",
		},
		&requestflag.Flag[string]{
			Name:     "interval",
			Usage:    "The time interval for evaluating spend against the threshold.",
			Required: true,
			BodyPath: "interval",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "notification-channel",
			Usage:    "Email notification settings for a spend alert.",
			Required: true,
			BodyPath: "notification_channel",
		},
		&requestflag.Flag[int64]{
			Name:     "threshold-amount",
			Usage:    "The alert threshold amount, in cents.",
			Required: true,
			BodyPath: "threshold_amount",
		},
	},
	Action:          handleAdminOrganizationProjectsSpendAlertsUpdate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"notification-channel": {
		&requestflag.InnerFlag[[]string]{
			Name:       "notification-channel.recipients",
			Usage:      "Email addresses that receive the spend alert notification.",
			InnerField: "recipients",
		},
		&requestflag.InnerFlag[string]{
			Name:       "notification-channel.type",
			Usage:      "The notification channel type. Currently only `email` is supported.",
			InnerField: "type",
		},
		&requestflag.InnerFlag[*string]{
			Name:       "notification-channel.subject-prefix",
			Usage:      "Optional subject prefix for alert emails.",
			InnerField: "subject_prefix",
		},
	},
})

var adminOrganizationProjectsSpendAlertsList = cli.Command{
	Name:    "list",
	Usage:   "Lists project spend alerts.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "Cursor for pagination. Provide the ID of the last spend alert from the previous response to fetch the next page.",
			QueryPath: "after",
		},
		&requestflag.Flag[string]{
			Name:      "before",
			Usage:     "Cursor for pagination. Provide the ID of the first spend alert from the previous response to fetch the previous page.",
			QueryPath: "before",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of spend alerts to return. Defaults to 20.",
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     "Sort order for the returned spend alerts.",
			Default:   "asc",
			QueryPath: "order",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleAdminOrganizationProjectsSpendAlertsList,
	HideHelpCommand: true,
}

var adminOrganizationProjectsSpendAlertsDelete = cli.Command{
	Name:    "delete",
	Usage:   "Deletes a project spend alert.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "alert-id",
			Required:  true,
			PathParam: "alert_id",
		},
	},
	Action:          handleAdminOrganizationProjectsSpendAlertsDelete,
	HideHelpCommand: true,
}

func handleAdminOrganizationProjectsSpendAlertsCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationProjectSpendAlertNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Projects.SpendAlerts.New(
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
		Title:          "admin:organization:projects:spend-alerts create",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsSpendAlertsUpdate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("alert-id") && len(unusedArgs) > 0 {
		cmd.Set("alert-id", unusedArgs[0])
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

	params := openai.AdminOrganizationProjectSpendAlertUpdateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Projects.SpendAlerts.Update(
		ctx,
		cmd.Value("project-id").(string),
		cmd.Value("alert-id").(string),
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
		Title:          "admin:organization:projects:spend-alerts update",
		Transform:      transform,
	})
}

func handleAdminOrganizationProjectsSpendAlertsList(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationProjectSpendAlertListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Admin.Organization.Projects.SpendAlerts.List(
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
			Title:          "admin:organization:projects:spend-alerts list",
			Transform:      transform,
		})
	} else {
		iter := client.Admin.Organization.Projects.SpendAlerts.ListAutoPaging(
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
			Title:          "admin:organization:projects:spend-alerts list",
			Transform:      transform,
		})
	}
}

func handleAdminOrganizationProjectsSpendAlertsDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("alert-id") && len(unusedArgs) > 0 {
		cmd.Set("alert-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.Projects.SpendAlerts.Delete(
		ctx,
		cmd.Value("project-id").(string),
		cmd.Value("alert-id").(string),
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
		Title:          "admin:organization:projects:spend-alerts delete",
		Transform:      transform,
	})
}
