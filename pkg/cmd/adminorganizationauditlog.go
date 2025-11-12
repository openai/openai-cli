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

var adminOrganizationAuditLogsList = requestflag.WithInnerFlags(cli.Command{
	Name:    "list",
	Usage:   "List user actions and configuration changes within this organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[[]string]{
			Name:      "actor-email",
			Usage:     "Return only events performed by users with these emails.",
			QueryPath: "actor_emails",
		},
		&requestflag.Flag[[]string]{
			Name:      "actor-id",
			Usage:     "Return only events performed by these actors. Can be a user ID, a service account ID, or an api key tracking ID.",
			QueryPath: "actor_ids",
		},
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "A cursor for use in pagination. `after` is an object ID that defines your place in the list. For instance, if you make a list request and receive 100 objects, ending with obj_foo, your subsequent call can include after=obj_foo in order to fetch the next page of the list.\n",
			QueryPath: "after",
		},
		&requestflag.Flag[string]{
			Name:      "before",
			Usage:     "A cursor for use in pagination. `before` is an object ID that defines your place in the list. For instance, if you make a list request and receive 100 objects, starting with obj_foo, your subsequent call can include before=obj_foo in order to fetch the previous page of the list.\n",
			QueryPath: "before",
		},
		&requestflag.Flag[map[string]any]{
			Name:      "effective-at",
			Usage:     "Return only events whose `effective_at` (Unix seconds) is in this range.",
			QueryPath: "effective_at",
		},
		&requestflag.Flag[[]string]{
			Name:      "event-type",
			Usage:     "Return only events with a `type` in one of these values. For example, `project.created`. For all options, see the documentation for the [audit log object](https://platform.openai.com/docs/api-reference/audit-logs/object).",
			QueryPath: "event_types",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of objects to be returned. Limit can range between 1 and 100, and the default is 20.\n",
			Default:   20,
			QueryPath: "limit",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only events for these projects.",
			QueryPath: "project_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "resource-id",
			Usage:     "Return only events performed on these targets. For example, a project ID updated.",
			QueryPath: "resource_ids",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleAdminOrganizationAuditLogsList,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"effective-at": {
		&requestflag.InnerFlag[int64]{
			Name:       "effective-at.gt",
			Usage:      "Return only events whose `effective_at` (Unix seconds) is greater than this value.",
			InnerField: "gt",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "effective-at.gte",
			Usage:      "Return only events whose `effective_at` (Unix seconds) is greater than or equal to this value.",
			InnerField: "gte",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "effective-at.lt",
			Usage:      "Return only events whose `effective_at` (Unix seconds) is less than this value.",
			InnerField: "lt",
		},
		&requestflag.InnerFlag[int64]{
			Name:       "effective-at.lte",
			Usage:      "Return only events whose `effective_at` (Unix seconds) is less than or equal to this value.",
			InnerField: "lte",
		},
	},
})

func handleAdminOrganizationAuditLogsList(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationAuditLogListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Admin.Organization.AuditLogs.List(ctx, params, options...)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "admin:organization:audit-logs list",
			Transform:      transform,
		})
	} else {
		iter := client.Admin.Organization.AuditLogs.ListAutoPaging(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "admin:organization:audit-logs list",
			Transform:      transform,
		})
	}
}
