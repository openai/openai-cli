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

var adminOrganizationUsageAudioSpeeches = cli.Command{
	Name:    "audio-speeches",
	Usage:   "Get audio speeches usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "api-key-id",
			Usage:     "Return only usage for these API keys.",
			QueryPath: "api_key_ids",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`, `user_id`, `api_key_id`, `model` or any combination of them.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[[]string]{
			Name:      "model",
			Usage:     "Return only usage for these models.",
			QueryPath: "models",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "user-id",
			Usage:     "Return only usage for these users.",
			QueryPath: "user_ids",
		},
	},
	Action:          handleAdminOrganizationUsageAudioSpeeches,
	HideHelpCommand: true,
}

var adminOrganizationUsageAudioTranscriptions = cli.Command{
	Name:    "audio-transcriptions",
	Usage:   "Get audio transcriptions usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "api-key-id",
			Usage:     "Return only usage for these API keys.",
			QueryPath: "api_key_ids",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`, `user_id`, `api_key_id`, `model` or any combination of them.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[[]string]{
			Name:      "model",
			Usage:     "Return only usage for these models.",
			QueryPath: "models",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "user-id",
			Usage:     "Return only usage for these users.",
			QueryPath: "user_ids",
		},
	},
	Action:          handleAdminOrganizationUsageAudioTranscriptions,
	HideHelpCommand: true,
}

var adminOrganizationUsageCodeInterpreterSessions = cli.Command{
	Name:    "code-interpreter-sessions",
	Usage:   "Get code interpreter sessions usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
	},
	Action:          handleAdminOrganizationUsageCodeInterpreterSessions,
	HideHelpCommand: true,
}

var adminOrganizationUsageCompletions = cli.Command{
	Name:    "completions",
	Usage:   "Get completions usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "api-key-id",
			Usage:     "Return only usage for these API keys.",
			QueryPath: "api_key_ids",
		},
		&requestflag.Flag[bool]{
			Name:      "batch",
			Usage:     "If `true`, return batch jobs only. If `false`, return non-batch jobs only. By default, return both.\n",
			QueryPath: "batch",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`, `user_id`, `api_key_id`, `model`, `batch`, `service_tier` or any combination of them.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[[]string]{
			Name:      "model",
			Usage:     "Return only usage for these models.",
			QueryPath: "models",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "user-id",
			Usage:     "Return only usage for these users.",
			QueryPath: "user_ids",
		},
	},
	Action:          handleAdminOrganizationUsageCompletions,
	HideHelpCommand: true,
}

var adminOrganizationUsageCosts = cli.Command{
	Name:    "costs",
	Usage:   "Get costs details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "api-key-id",
			Usage:     "Return only costs for these API keys.",
			QueryPath: "api_key_ids",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently only `1d` is supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the costs by the specified fields. Support fields include `project_id`, `line_item`, `api_key_id` and any combination of them.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "A limit on the number of buckets to be returned. Limit can range between 1 and 180, and the default is 7.\n",
			Default:   7,
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only costs for these projects.",
			QueryPath: "project_ids",
		},
	},
	Action:          handleAdminOrganizationUsageCosts,
	HideHelpCommand: true,
}

var adminOrganizationUsageEmbeddings = cli.Command{
	Name:    "embeddings",
	Usage:   "Get embeddings usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "api-key-id",
			Usage:     "Return only usage for these API keys.",
			QueryPath: "api_key_ids",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`, `user_id`, `api_key_id`, `model` or any combination of them.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[[]string]{
			Name:      "model",
			Usage:     "Return only usage for these models.",
			QueryPath: "models",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "user-id",
			Usage:     "Return only usage for these users.",
			QueryPath: "user_ids",
		},
	},
	Action:          handleAdminOrganizationUsageEmbeddings,
	HideHelpCommand: true,
}

var adminOrganizationUsageFileSearchCalls = cli.Command{
	Name:    "file-search-calls",
	Usage:   "Get file search calls usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "api-key-id",
			Usage:     "Return only usage for these API keys.",
			QueryPath: "api_key_ids",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`, `user_id`, `api_key_id`, `vector_store_id` or any combination of them.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "user-id",
			Usage:     "Return only usage for these users.",
			QueryPath: "user_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "vector-store-id",
			Usage:     "Return only usage for these vector stores.",
			QueryPath: "vector_store_ids",
		},
	},
	Action:          handleAdminOrganizationUsageFileSearchCalls,
	HideHelpCommand: true,
}

var adminOrganizationUsageImages = cli.Command{
	Name:    "images",
	Usage:   "Get images usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "api-key-id",
			Usage:     "Return only usage for these API keys.",
			QueryPath: "api_key_ids",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`, `user_id`, `api_key_id`, `model`, `size`, `source` or any combination of them.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[[]string]{
			Name:      "model",
			Usage:     "Return only usage for these models.",
			QueryPath: "models",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "size",
			Usage:     "Return only usages for these image sizes. Possible values are `256x256`, `512x512`, `1024x1024`, `1792x1792`, `1024x1792` or any combination of them.",
			QueryPath: "sizes",
		},
		&requestflag.Flag[[]string]{
			Name:      "source",
			Usage:     "Return only usages for these sources. Possible values are `image.generation`, `image.edit`, `image.variation` or any combination of them.",
			QueryPath: "sources",
		},
		&requestflag.Flag[[]string]{
			Name:      "user-id",
			Usage:     "Return only usage for these users.",
			QueryPath: "user_ids",
		},
	},
	Action:          handleAdminOrganizationUsageImages,
	HideHelpCommand: true,
}

var adminOrganizationUsageModerations = cli.Command{
	Name:    "moderations",
	Usage:   "Get moderations usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "api-key-id",
			Usage:     "Return only usage for these API keys.",
			QueryPath: "api_key_ids",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`, `user_id`, `api_key_id`, `model` or any combination of them.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[[]string]{
			Name:      "model",
			Usage:     "Return only usage for these models.",
			QueryPath: "models",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "user-id",
			Usage:     "Return only usage for these users.",
			QueryPath: "user_ids",
		},
	},
	Action:          handleAdminOrganizationUsageModerations,
	HideHelpCommand: true,
}

var adminOrganizationUsageVectorStores = cli.Command{
	Name:    "vector-stores",
	Usage:   "Get vector stores usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
	},
	Action:          handleAdminOrganizationUsageVectorStores,
	HideHelpCommand: true,
}

var adminOrganizationUsageWebSearchCalls = cli.Command{
	Name:    "web-search-calls",
	Usage:   "Get web search calls usage details for the organization.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[int64]{
			Name:      "start-time",
			Usage:     "Start time (Unix seconds) of the query time range, inclusive.",
			Required:  true,
			QueryPath: "start_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "api-key-id",
			Usage:     "Return only usage for these API keys.",
			QueryPath: "api_key_ids",
		},
		&requestflag.Flag[string]{
			Name:      "bucket-width",
			Usage:     "Width of each time bucket in response. Currently `1m`, `1h` and `1d` are supported, default to `1d`.",
			Default:   "1d",
			QueryPath: "bucket_width",
		},
		&requestflag.Flag[[]string]{
			Name:      "context-level",
			Usage:     "Return only web search usage for these context levels.",
			QueryPath: "context_levels",
		},
		&requestflag.Flag[int64]{
			Name:      "end-time",
			Usage:     "End time (Unix seconds) of the query time range, exclusive.",
			QueryPath: "end_time",
		},
		&requestflag.Flag[[]string]{
			Name:      "group-by",
			Usage:     "Group the usage data by the specified fields. Support fields include `project_id`, `user_id`, `api_key_id`, `model`, `context_level` or any combination of them.",
			QueryPath: "group_by",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Specifies the number of buckets to return.\n- `bucket_width=1d`: default: 7, max: 31\n- `bucket_width=1h`: default: 24, max: 168\n- `bucket_width=1m`: default: 60, max: 1440\n",
			QueryPath: "limit",
		},
		&requestflag.Flag[[]string]{
			Name:      "model",
			Usage:     "Return only usage for these models.",
			QueryPath: "models",
		},
		&requestflag.Flag[string]{
			Name:      "page",
			Usage:     "A cursor for use in pagination. Corresponding to the `next_page` field from the previous response.",
			QueryPath: "page",
		},
		&requestflag.Flag[[]string]{
			Name:      "project-id",
			Usage:     "Return only usage for these projects.",
			QueryPath: "project_ids",
		},
		&requestflag.Flag[[]string]{
			Name:      "user-id",
			Usage:     "Return only usage for these users.",
			QueryPath: "user_ids",
		},
	},
	Action:          handleAdminOrganizationUsageWebSearchCalls,
	HideHelpCommand: true,
}

func handleAdminOrganizationUsageAudioSpeeches(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageAudioSpeechesParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.AudioSpeeches(ctx, params, options...)
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
		Title:          "admin:organization:usage audio-speeches",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageAudioTranscriptions(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageAudioTranscriptionsParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.AudioTranscriptions(ctx, params, options...)
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
		Title:          "admin:organization:usage audio-transcriptions",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageCodeInterpreterSessions(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageCodeInterpreterSessionsParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.CodeInterpreterSessions(ctx, params, options...)
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
		Title:          "admin:organization:usage code-interpreter-sessions",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageCompletions(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageCompletionsParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.Completions(ctx, params, options...)
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
		Title:          "admin:organization:usage completions",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageCosts(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageCostsParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.Costs(ctx, params, options...)
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
		Title:          "admin:organization:usage costs",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageEmbeddings(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageEmbeddingsParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.Embeddings(ctx, params, options...)
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
		Title:          "admin:organization:usage embeddings",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageFileSearchCalls(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageFileSearchCallsParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.FileSearchCalls(ctx, params, options...)
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
		Title:          "admin:organization:usage file-search-calls",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageImages(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageImagesParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.Images(ctx, params, options...)
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
		Title:          "admin:organization:usage images",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageModerations(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageModerationsParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.Moderations(ctx, params, options...)
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
		Title:          "admin:organization:usage moderations",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageVectorStores(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageVectorStoresParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.VectorStores(ctx, params, options...)
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
		Title:          "admin:organization:usage vector-stores",
		Transform:      transform,
	})
}

func handleAdminOrganizationUsageWebSearchCalls(ctx context.Context, cmd *cli.Command) error {
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

	params := openai.AdminOrganizationUsageWebSearchCallsParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.Admin.Organization.Usage.WebSearchCalls(ctx, params, options...)
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
		Title:          "admin:organization:usage web-search-calls",
		Transform:      transform,
	})
}
