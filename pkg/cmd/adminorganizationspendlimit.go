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

var adminOrganizationSpendLimitRetrieve = cli.Command{
	Name:            "retrieve",
	Usage:           "Get the organization's hard spend limit.",
	Suggest:         true,
	Flags:           []cli.Flag{},
	Action:          handleAdminOrganizationSpendLimitRetrieve,
	HideHelpCommand: true,
}

var adminOrganizationSpendLimitUpdate = cli.Command{
	Name:    "update",
	Usage:   "Create or replace the organization's hard spend limit.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "currency",
			Usage:    "The currency for the threshold amount. Currently, only `USD` is supported.",
			Required: true,
			BodyPath: "currency",
		},
		&requestflag.Flag[string]{
			Name:     "interval",
			Usage:    "The time interval for evaluating spend against the threshold. Currently, only `month` is supported.",
			Required: true,
			BodyPath: "interval",
		},
		&requestflag.Flag[int64]{
			Name:     "threshold-amount",
			Usage:    "The hard spend limit amount, in cents.",
			Required: true,
			BodyPath: "threshold_amount",
		},
	},
	Action:          handleAdminOrganizationSpendLimitUpdate,
	HideHelpCommand: true,
}

var adminOrganizationSpendLimitDelete = cli.Command{
	Name:            "delete",
	Usage:           "Delete the organization's hard spend limit.",
	Suggest:         true,
	Flags:           []cli.Flag{},
	Action:          handleAdminOrganizationSpendLimitDelete,
	HideHelpCommand: true,
}

func handleAdminOrganizationSpendLimitRetrieve(ctx context.Context, cmd *cli.Command) error {
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

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.SpendLimit.Get(ctx, options...)
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
		Title:          "admin:organization:spend-limit retrieve",
		Transform:      transform,
	})
}

func handleAdminOrganizationSpendLimitUpdate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationSpendLimitUpdateParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.SpendLimit.Update(ctx, params, options...)
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
		Title:          "admin:organization:spend-limit update",
		Transform:      transform,
	})
}

func handleAdminOrganizationSpendLimitDelete(ctx context.Context, cmd *cli.Command) error {
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

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.SpendLimit.Delete(ctx, options...)
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
		Title:          "admin:organization:spend-limit delete",
		Transform:      transform,
	})
}
