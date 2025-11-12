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

var adminOrganizationProjectsRateLimitsListRateLimits = cli.Command{
	Name:    "list-rate-limits",
	Usage:   "Returns the rate limits per model for a project.",
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
		&requestflag.Flag[string]{
			Name:      "before",
			Usage:     "A cursor for use in pagination. `before` is an object ID that defines your place in the list. For instance, if you make a list request and receive 100 objects, beginning with obj_foo, your subsequent call can include before=obj_foo in order to fetch the previous page of the list.\n",
			QueryPath: "before",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of objects to be returned. The default is 100.\n",
			Default:   100,
			QueryPath: "limit",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleAdminOrganizationProjectsRateLimitsListRateLimits,
	HideHelpCommand: true,
}

var adminOrganizationProjectsRateLimitsUpdateRateLimit = cli.Command{
	Name:    "update-rate-limit",
	Usage:   "Updates a project rate limit.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "project-id",
			Required:  true,
			PathParam: "project_id",
		},
		&requestflag.Flag[string]{
			Name:      "rate-limit-id",
			Required:  true,
			PathParam: "rate_limit_id",
		},
		&requestflag.Flag[int64]{
			Name:     "batch-1-day-max-input-tokens",
			Usage:    "The maximum batch input tokens per day. Only relevant for certain models.",
			BodyPath: "batch_1_day_max_input_tokens",
		},
		&requestflag.Flag[int64]{
			Name:     "max-audio-megabytes-per-1-minute",
			Usage:    "The maximum audio megabytes per minute. Only relevant for certain models.",
			BodyPath: "max_audio_megabytes_per_1_minute",
		},
		&requestflag.Flag[int64]{
			Name:     "max-images-per-1-minute",
			Usage:    "The maximum images per minute. Only relevant for certain models.",
			BodyPath: "max_images_per_1_minute",
		},
		&requestflag.Flag[int64]{
			Name:     "max-requests-per-1-day",
			Usage:    "The maximum requests per day. Only relevant for certain models.",
			BodyPath: "max_requests_per_1_day",
		},
		&requestflag.Flag[int64]{
			Name:     "max-requests-per-1-minute",
			Usage:    "The maximum requests per minute.",
			BodyPath: "max_requests_per_1_minute",
		},
		&requestflag.Flag[int64]{
			Name:     "max-tokens-per-1-minute",
			Usage:    "The maximum tokens per minute.",
			BodyPath: "max_tokens_per_1_minute",
		},
	},
	Action:          handleAdminOrganizationProjectsRateLimitsUpdateRateLimit,
	HideHelpCommand: true,
}

func handleAdminOrganizationProjectsRateLimitsListRateLimits(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationProjectRateLimitListRateLimitsParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Admin.Organization.Projects.RateLimits.ListRateLimits(
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
			Title:          "admin:organization:projects:rate-limits list-rate-limits",
			Transform:      transform,
		})
	} else {
		iter := client.Admin.Organization.Projects.RateLimits.ListRateLimitsAutoPaging(
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
			Title:          "admin:organization:projects:rate-limits list-rate-limits",
			Transform:      transform,
		})
	}
}

func handleAdminOrganizationProjectsRateLimitsUpdateRateLimit(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("project-id") && len(unusedArgs) > 0 {
		cmd.Set("project-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("rate-limit-id") && len(unusedArgs) > 0 {
		cmd.Set("rate-limit-id", unusedArgs[0])
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

	params := openai.AdminOrganizationProjectRateLimitUpdateRateLimitParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Projects.RateLimits.UpdateRateLimit(
		ctx,
		cmd.Value("project-id").(string),
		cmd.Value("rate-limit-id").(string),
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
		Title:          "admin:organization:projects:rate-limits update-rate-limit",
		Transform:      transform,
	})
}
