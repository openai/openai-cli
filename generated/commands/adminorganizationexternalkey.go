// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package commands

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

var adminOrganizationExternalKeysCreate = cli.Command{
	Name:    "create",
	Usage:   "Add an external key config for an organization",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "external-id",
			Usage:    "The ExternalID that OpenAI will pass in the AssumeRole call for the customer's AWS account. This must be the OpenAI organization ID or an API project ID that belongs to the organization.",
			BodyPath: "external_id",
		},
		&requestflag.Flag[string]{
			Name:     "kms-arn",
			Usage:    "The Key Management System ARN for the master key you manage",
			BodyPath: "kms_arn",
		},
		&requestflag.Flag[string]{
			Name:     "name",
			Usage:    "A friendly name to identify this config",
			Required: true,
			BodyPath: "name",
		},
		&requestflag.Flag[string]{
			Name:     "role-arn",
			Usage:    "The Role ARN that OpenAI will assume to call your KMS",
			BodyPath: "role_arn",
		},
		&requestflag.Flag[string]{
			Name:     "type",
			Usage:    `Allowed values: "aws", "gcp", "azure".`,
			Required: true,
			BodyPath: "type",
		},
		&requestflag.Flag[string]{
			Name:     "purpose",
			Usage:    `Allowed values: "ekm", "psp_decryption".`,
			BodyPath: "purpose",
		},
		&requestflag.Flag[string]{
			Name:     "audience",
			Usage:    "The aud field in the identity token OpenAI will pass in the call to the customer's GCP STS. This must be the OpenAI organization ID or an API project ID that belongs to the organization.",
			BodyPath: "audience",
		},
		&requestflag.Flag[string]{
			Name:     "kms-key-location",
			Usage:    "The region where your Key Management System master key is located",
			BodyPath: "kms_key_location",
		},
		&requestflag.Flag[string]{
			Name:     "kms-key-name",
			Usage:    "The name of the Key Management System master key you manage",
			BodyPath: "kms_key_name",
		},
		&requestflag.Flag[string]{
			Name:     "kms-key-ring-name",
			Usage:    "The Key Management System key ring for the master key you manage",
			BodyPath: "kms_key_ring_name",
		},
		&requestflag.Flag[string]{
			Name:     "kms-project-id",
			Usage:    "The string GCP project name where your KMS resides",
			BodyPath: "kms_project_id",
		},
		&requestflag.Flag[string]{
			Name:     "workload-identity-pool-id",
			Usage:    "The pool containing the Workload Identity provider that you registered for OpenAI",
			BodyPath: "workload_identity_pool_id",
		},
		&requestflag.Flag[string]{
			Name:     "workload-identity-project-number",
			Usage:    "The numeric GCP project number where you registered OpenAI's workload identity",
			BodyPath: "workload_identity_project_number",
		},
		&requestflag.Flag[string]{
			Name:     "workload-identity-provider-id",
			Usage:    "The Workload Identity provider that you registered for OpenAI",
			BodyPath: "workload_identity_provider_id",
		},
		&requestflag.Flag[string]{
			Name:     "kms-key-version",
			Usage:    "An exact CryptoKeyVersion number. Required for PSP decryption; not accepted for EKM.",
			BodyPath: "kms_key_version",
		},
		&requestflag.Flag[string]{
			Name:     "key-name",
			Usage:    "The name of the Azure Key Vault master key that you manage",
			BodyPath: "key_name",
		},
		&requestflag.Flag[string]{
			Name:     "tenant-id",
			Usage:    "Your Azure tenant UUID",
			BodyPath: "tenant_id",
		},
		&requestflag.Flag[string]{
			Name:     "vault-uri",
			Usage:    "The URI of the Azure vault containing the master key you manage",
			BodyPath: "vault_uri",
		},
		&requestflag.Flag[string]{
			Name:     "key-version",
			Usage:    "An exact Azure Key Vault key version. Required for PSP decryption; not accepted for EKM.",
			BodyPath: "key_version",
		},
	},
	Action:          handleAdminOrganizationExternalKeysCreate,
	HideHelpCommand: true,
}

var adminOrganizationExternalKeysRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Retrieve a stored external key registration for the user's organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "external-key-id",
			Required:  true,
			PathParam: "external_key_id",
		},
	},
	Action:          handleAdminOrganizationExternalKeysRetrieve,
	HideHelpCommand: true,
}

var adminOrganizationExternalKeysList = cli.Command{
	Name:    "list",
	Usage:   "List the external key configs registered on the user's organization",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[*string]{
			Name:      "after",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "order",
			Usage:     `Allowed values: "asc", "desc".`,
			QueryPath: "order",
		},
		&requestflag.Flag[string]{
			Name:      "purpose",
			Usage:     "Filter by key purpose. When omitted, returns keys of all purposes.",
			QueryPath: "purpose",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleAdminOrganizationExternalKeysList,
	HideHelpCommand: true,
}

var adminOrganizationExternalKeysDelete = cli.Command{
	Name:    "delete",
	Usage:   "Delete an external key config for the user's organization",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "external-key-id",
			Required:  true,
			PathParam: "external_key_id",
		},
	},
	Action:          handleAdminOrganizationExternalKeysDelete,
	HideHelpCommand: true,
}

var adminOrganizationExternalKeysValidate = cli.Command{
	Name:    "validate",
	Usage:   "Validate an external key config for the user's organization",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "external-key-id",
			Required:  true,
			PathParam: "external_key_id",
		},
	},
	Action:          handleAdminOrganizationExternalKeysValidate,
	HideHelpCommand: true,
}

func handleAdminOrganizationExternalKeysCreate(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationExternalKeyNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.ExternalKeys.New(ctx, params, options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) admin.organization.external_keys > (method) create",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "admin:organization:external-keys create",
		Transform:      transform,
	})
}

func handleAdminOrganizationExternalKeysRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("external-key-id") && len(unusedArgs) > 0 {
		cmd.Set("external-key-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.ExternalKeys.Get(ctx, cmd.Value("external-key-id").(string), options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) admin.organization.external_keys > (method) retrieve",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "admin:organization:external-keys retrieve",
		Transform:      transform,
	})
}

func handleAdminOrganizationExternalKeysList(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationExternalKeyListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.Admin.Organization.ExternalKeys.List(ctx, params, options...)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			Context:        ctx,
			Operation:      "(resource) admin.organization.external_keys > (method) list",
			OutputKind:     outputResponse,
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "admin:organization:external-keys list",
			Transform:      transform,
		})
	} else {
		iter := client.Admin.Organization.ExternalKeys.ListAutoPaging(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			Context:        ctx,
			Operation:      "(resource) admin.organization.external_keys > (method) list",
			OutputKind:     outputPageItem,
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "admin:organization:external-keys list",
			Transform:      transform,
		})
	}
}

func handleAdminOrganizationExternalKeysDelete(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("external-key-id") && len(unusedArgs) > 0 {
		cmd.Set("external-key-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.ExternalKeys.Delete(ctx, cmd.Value("external-key-id").(string), options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) admin.organization.external_keys > (method) delete",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "admin:organization:external-keys delete",
		Transform:      transform,
	})
}

func handleAdminOrganizationExternalKeysValidate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("external-key-id") && len(unusedArgs) > 0 {
		cmd.Set("external-key-id", unusedArgs[0])
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
	_, err = client.Admin.Organization.ExternalKeys.Validate(ctx, cmd.Value("external-key-id").(string), options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		Context:        ctx,
		Operation:      "(resource) admin.organization.external_keys > (method) validate",
		OutputKind:     outputResponse,
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "admin:organization:external-keys validate",
		Transform:      transform,
	})
}
