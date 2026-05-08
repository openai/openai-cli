package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const defaultAdsBaseURL = "https://api.ads.openai.com/v1"

var adsCommand = cli.Command{
	Name:     "ads",
	Usage:    "Manage OpenAI Ads API resources.",
	Category: "API RESOURCE",
	Suggest:  true,
	Commands: []*cli.Command{
		{
			Name:    "account",
			Usage:   "Manage the current ad account.",
			Suggest: true,
			Commands: []*cli.Command{
				&adsAccountRetrieve,
			},
		},
		{
			Name:    "campaigns",
			Usage:   "Create, list, update, and manage campaigns.",
			Suggest: true,
			Commands: []*cli.Command{
				&adsCampaignsCreate,
				&adsCampaignsRetrieve,
				&adsCampaignsList,
				&adsCampaignsUpdate,
				&adsCampaignsActivate,
				&adsCampaignsPause,
				&adsCampaignsArchive,
			},
		},
		{
			Name:    "ad-groups",
			Usage:   "Create, list, update, and manage ad groups.",
			Suggest: true,
			Commands: []*cli.Command{
				&adsAdGroupsCreate,
				&adsAdGroupsRetrieve,
				&adsAdGroupsList,
				&adsAdGroupsUpdate,
				&adsAdGroupsActivate,
				&adsAdGroupsPause,
				&adsAdGroupsArchive,
			},
		},
		{
			Name:    "ads",
			Aliases: []string{"ad"},
			Usage:   "Create, list, update, and manage ads.",
			Suggest: true,
			Commands: []*cli.Command{
				&adsAdsCreate,
				&adsAdsRetrieve,
				&adsAdsList,
				&adsAdsUpdate,
				&adsAdsActivate,
				&adsAdsPause,
				&adsAdsArchive,
			},
		},
		{
			Name:    "files",
			Aliases: []string{"file"},
			Usage:   "Upload image assets for ad creatives.",
			Suggest: true,
			Commands: []*cli.Command{
				&adsFilesUpload,
			},
		},
		{
			Name:    "insights",
			Usage:   "Fetch Ads reporting insights.",
			Suggest: true,
			Commands: []*cli.Command{
				&adsInsightsAccount,
				&adsInsightsCampaign,
				&adsInsightsAdGroup,
				&adsInsightsAd,
			},
		},
	},
}

var adsAccountRetrieve = cli.Command{
	Name:            "retrieve",
	Usage:           "Fetch metadata for the current ad account.",
	Suggest:         true,
	Action:          handleAdsAccountRetrieve,
	HideHelpCommand: true,
}

var adsCampaignsCreate = cli.Command{
	Name:    "create",
	Usage:   "Create a campaign in the current ad account.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "name", Usage: "Campaign name.", Required: true},
		&requestflag.Flag[string]{Name: "description", Usage: "Campaign description."},
		&requestflag.Flag[string]{Name: "status", Usage: "Campaign status: active or paused.", Required: true},
		&requestflag.Flag[int64]{Name: "start-time", Usage: "Unix timestamp when delivery starts."},
		&requestflag.Flag[int64]{Name: "end-time", Usage: "Unix timestamp when delivery ends."},
		&requestflag.Flag[int64]{Name: "lifetime-spend-limit-micros", Usage: "Lifetime campaign budget in micros. Minimum 1000000.", Required: true},
		&requestflag.Flag[[]string]{Name: "country", Usage: "Included country code. May be repeated."},
		&requestflag.Flag[[]string]{Name: "exclude-country", Usage: "Excluded country code. May be repeated."},
	},
	Action:          handleAdsCampaignsCreate,
	HideHelpCommand: true,
}

var adsCampaignsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Fetch one campaign by ID.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "campaign-id"},
	},
	Action:          handleAdsCampaignsRetrieve,
	HideHelpCommand: true,
}

var adsCampaignsList = cli.Command{
	Name:            "list",
	Usage:           "List campaigns in the current ad account.",
	Suggest:         true,
	Flags:           adsPaginationFlags(),
	Action:          handleAdsCampaignsList,
	HideHelpCommand: true,
}

var adsCampaignsUpdate = cli.Command{
	Name:    "update",
	Usage:   "Update a campaign with POST. Use null for nullable fields like --description null.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "campaign-id"},
		&requestflag.Flag[*string]{Name: "name", Usage: "Campaign name."},
		&requestflag.Flag[*string]{Name: "description", Usage: "Campaign description. Use null to clear."},
		&requestflag.Flag[*string]{Name: "status", Usage: "Campaign status: active, paused, or archived."},
		&requestflag.Flag[*int64]{Name: "start-time", Usage: "Unix timestamp when delivery starts. Use null to clear."},
		&requestflag.Flag[*int64]{Name: "end-time", Usage: "Unix timestamp when delivery ends. Use null to clear."},
		&requestflag.Flag[int64]{Name: "lifetime-spend-limit-micros", Usage: "Lifetime campaign budget in micros."},
		&requestflag.Flag[map[string]any]{Name: "targeting", Usage: "Full targeting object as JSON/YAML. Use null to clear."},
		&requestflag.Flag[[]string]{Name: "country", Usage: "Included country code. May be repeated."},
		&requestflag.Flag[[]string]{Name: "exclude-country", Usage: "Excluded country code. May be repeated."},
	},
	Action:          handleAdsCampaignsUpdate,
	HideHelpCommand: true,
}

var adsCampaignsActivate = adsStateCommand("activate", "campaign-id", "Activate a campaign.", handleAdsCampaignsActivate)
var adsCampaignsPause = adsStateCommand("pause", "campaign-id", "Pause a campaign.", handleAdsCampaignsPause)
var adsCampaignsArchive = adsStateCommand("archive", "campaign-id", "Archive a campaign. This is not reversible.", handleAdsCampaignsArchive)

var adsAdGroupsCreate = cli.Command{
	Name:    "create",
	Usage:   "Create an ad group for a campaign.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "campaign-id", Usage: "Parent campaign ID.", Required: true},
		&requestflag.Flag[string]{Name: "name", Usage: "Ad group name.", Required: true},
		&requestflag.Flag[string]{Name: "description", Usage: "Ad group description."},
		&requestflag.Flag[[]string]{Name: "context-hint", Usage: "Free-form audience or placement hint. May be repeated."},
		&requestflag.Flag[string]{Name: "status", Usage: "Ad group status: active or paused.", Required: true},
		&requestflag.Flag[string]{Name: "billing-event-type", Usage: "Billing event type. Currently impression.", Default: "impression"},
		&requestflag.Flag[int64]{Name: "max-bid-micros", Usage: "Maximum bid in micros. Between 1 and 100000000.", Required: true},
	},
	Action:          handleAdsAdGroupsCreate,
	HideHelpCommand: true,
}

var adsAdGroupsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Fetch one ad group by ID.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "ad-group-id"},
	},
	Action:          handleAdsAdGroupsRetrieve,
	HideHelpCommand: true,
}

var adsAdGroupsList = cli.Command{
	Name:    "list",
	Usage:   "List ad groups for a campaign.",
	Suggest: true,
	Flags: append([]cli.Flag{
		&requestflag.Flag[string]{Name: "campaign-id", Usage: "Parent campaign ID.", Required: true},
	}, adsPaginationFlags()...),
	Action:          handleAdsAdGroupsList,
	HideHelpCommand: true,
}

var adsAdGroupsUpdate = cli.Command{
	Name:    "update",
	Usage:   "Update an ad group with POST. Use null for nullable fields like --description null.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "ad-group-id"},
		&requestflag.Flag[*string]{Name: "name", Usage: "Ad group name."},
		&requestflag.Flag[*string]{Name: "description", Usage: "Ad group description. Use null to clear."},
		&requestflag.Flag[[]string]{Name: "context-hint", Usage: "Free-form audience or placement hint. May be repeated."},
		&requestflag.Flag[*string]{Name: "status", Usage: "Ad group status: active, paused, or archived."},
		&requestflag.Flag[map[string]any]{Name: "bidding-config", Usage: "Full bidding_config object as JSON/YAML."},
		&requestflag.Flag[string]{Name: "billing-event-type", Usage: "Billing event type. Currently impression."},
		&requestflag.Flag[int64]{Name: "max-bid-micros", Usage: "Maximum bid in micros."},
	},
	Action:          handleAdsAdGroupsUpdate,
	HideHelpCommand: true,
}

var adsAdGroupsActivate = adsStateCommand("activate", "ad-group-id", "Activate an ad group.", handleAdsAdGroupsActivate)
var adsAdGroupsPause = adsStateCommand("pause", "ad-group-id", "Pause an ad group.", handleAdsAdGroupsPause)
var adsAdGroupsArchive = adsStateCommand("archive", "ad-group-id", "Archive an ad group. This is not reversible.", handleAdsAdGroupsArchive)

var adsAdsCreate = cli.Command{
	Name:    "create",
	Usage:   "Create an ad for an ad group.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "ad-group-id", Usage: "Parent ad group ID.", Required: true},
		&requestflag.Flag[string]{Name: "name", Usage: "Ad name for organization; not shown to end users.", Required: true},
		&requestflag.Flag[string]{Name: "status", Usage: "Ad status: active or paused.", Required: true},
		&requestflag.Flag[string]{Name: "creative-type", Usage: "Creative type. Currently chat_card.", Default: "chat_card"},
		&requestflag.Flag[string]{Name: "title", Usage: "Creative title."},
		&requestflag.Flag[string]{Name: "body", Usage: "Creative body."},
		&requestflag.Flag[string]{Name: "target-url", Usage: "Creative destination URL."},
		&requestflag.Flag[string]{Name: "file-id", Usage: "File ID returned by ads files upload."},
	},
	Action:          handleAdsAdsCreate,
	HideHelpCommand: true,
}

var adsAdsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Fetch one ad by ID.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "ad-id"},
	},
	Action:          handleAdsAdsRetrieve,
	HideHelpCommand: true,
}

var adsAdsList = cli.Command{
	Name:    "list",
	Usage:   "List ads for an ad group.",
	Suggest: true,
	Flags: append([]cli.Flag{
		&requestflag.Flag[string]{Name: "ad-group-id", Usage: "Parent ad group ID.", Required: true},
	}, adsPaginationFlags()...),
	Action:          handleAdsAdsList,
	HideHelpCommand: true,
}

var adsAdsUpdate = cli.Command{
	Name:    "update",
	Usage:   "Update an ad with POST. If updating creative fields, provide the full creative.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "ad-id"},
		&requestflag.Flag[*string]{Name: "name", Usage: "Ad name for organization; not shown to end users."},
		&requestflag.Flag[*string]{Name: "status", Usage: "Ad status: active, paused, or archived."},
		&requestflag.Flag[map[string]any]{Name: "creative", Usage: "Full creative object as JSON/YAML."},
		&requestflag.Flag[string]{Name: "creative-type", Usage: "Creative type. Currently chat_card.", Default: "chat_card"},
		&requestflag.Flag[string]{Name: "title", Usage: "Creative title."},
		&requestflag.Flag[string]{Name: "body", Usage: "Creative body."},
		&requestflag.Flag[string]{Name: "target-url", Usage: "Creative destination URL."},
		&requestflag.Flag[string]{Name: "file-id", Usage: "File ID returned by ads files upload."},
	},
	Action:          handleAdsAdsUpdate,
	HideHelpCommand: true,
}

var adsAdsActivate = adsStateCommand("activate", "ad-id", "Activate an ad.", handleAdsAdsActivate)
var adsAdsPause = adsStateCommand("pause", "ad-id", "Pause an ad.", handleAdsAdsPause)
var adsAdsArchive = adsStateCommand("archive", "ad-id", "Archive an ad. This is not reversible.", handleAdsAdsArchive)

var adsFilesUpload = cli.Command{
	Name:    "upload",
	Usage:   "Upload an image asset from --file or remote --image-url and return a file_id.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "file", Usage: "Local image file path. Use '-' to read from stdin."},
		&requestflag.Flag[string]{Name: "image-url", Usage: "Remote image URL to import."},
	},
	Action:          handleAdsFilesUpload,
	HideHelpCommand: true,
}

var adsInsightsAccount = cli.Command{
	Name:            "account",
	Usage:           "Fetch insights for the current ad account.",
	Suggest:         true,
	Flags:           adsInsightsFlags(),
	Action:          handleAdsInsightsAccount,
	HideHelpCommand: true,
}

var adsInsightsCampaign = cli.Command{
	Name:    "campaign",
	Usage:   "Fetch insights for one campaign.",
	Suggest: true,
	Flags: append([]cli.Flag{
		&requestflag.Flag[string]{Name: "campaign-id"},
	}, adsInsightsFlags()...),
	Action:          handleAdsInsightsCampaign,
	HideHelpCommand: true,
}

var adsInsightsAdGroup = cli.Command{
	Name:    "ad-group",
	Usage:   "Fetch insights for one ad group.",
	Suggest: true,
	Flags: append([]cli.Flag{
		&requestflag.Flag[string]{Name: "ad-group-id"},
	}, adsInsightsFlags()...),
	Action:          handleAdsInsightsAdGroup,
	HideHelpCommand: true,
}

var adsInsightsAd = cli.Command{
	Name:    "ad",
	Usage:   "Fetch insights for one ad.",
	Suggest: true,
	Flags: append([]cli.Flag{
		&requestflag.Flag[string]{Name: "ad-id"},
	}, adsInsightsFlags()...),
	Action:          handleAdsInsightsAd,
	HideHelpCommand: true,
}

func adsPaginationFlags() []cli.Flag {
	return []cli.Flag{
		&requestflag.Flag[int64]{Name: "limit", Usage: "Number of objects to return. Between 1 and 500."},
		&requestflag.Flag[string]{Name: "after", Usage: "Cursor for the next page."},
		&requestflag.Flag[string]{Name: "before", Usage: "Cursor for the previous page."},
		&requestflag.Flag[string]{Name: "order", Usage: "Sort order: asc or desc."},
	}
}

func adsInsightsFlags() []cli.Flag {
	return []cli.Flag{
		&requestflag.Flag[string]{Name: "time-granularity", Usage: "Aggregation bucket size: daily or none."},
		&requestflag.Flag[string]{Name: "aggregation-level", Usage: "Aggregation scope: ad_account, campaign, ad_group, or ad."},
		&requestflag.Flag[int64]{Name: "limit", Usage: "Number of rows to return. Between 1 and 10000."},
		&requestflag.Flag[string]{Name: "after", Usage: "Cursor for the next page."},
		&requestflag.Flag[string]{Name: "before", Usage: "Cursor for the previous page."},
		&requestflag.Flag[[]string]{Name: "field", Usage: "Field to project in each row. May be repeated."},
		&requestflag.Flag[[]string]{Name: "time-range", Usage: "Time range JSON expression. May be repeated."},
		&requestflag.Flag[[]string]{Name: "filter", Usage: "Filter expression JSON. May be repeated."},
		&requestflag.Flag[[]string]{Name: "sort", Usage: "Sort expression JSON. May be repeated."},
	}
}

func adsStateCommand(name, idFlag, usage string, action cli.ActionFunc) cli.Command {
	return cli.Command{
		Name:    name,
		Usage:   usage,
		Suggest: true,
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: idFlag},
		},
		Action:          action,
		HideHelpCommand: true,
	}
}

func handleAdsAccountRetrieve(ctx context.Context, cmd *cli.Command) error {
	return runAdsRequest(ctx, cmd, http.MethodGet, "/ad_account", nil, nil, "ads account retrieve")
}

func handleAdsCampaignsCreate(ctx context.Context, cmd *cli.Command) error {
	if err := rejectExtraArgs(cmd); err != nil {
		return err
	}
	body := map[string]any{
		"name":   cmd.String("name"),
		"status": cmd.String("status"),
		"budget": map[string]any{
			"lifetime_spend_limit_micros": int64Flag(cmd, "lifetime-spend-limit-micros"),
		},
	}
	setStringIfSet(body, cmd, "description", "description")
	setInt64IfSet(body, cmd, "start-time", "start_time")
	setInt64IfSet(body, cmd, "end-time", "end_time")
	if targeting := targetingFromCountryFlags(cmd); len(targeting) > 0 {
		body["targeting"] = targeting
	}
	return runAdsRequest(ctx, cmd, http.MethodPost, "/campaigns", nil, body, "ads campaigns create")
}

func handleAdsCampaignsRetrieve(ctx context.Context, cmd *cli.Command) error {
	id, err := idFromFlagOrArg(cmd, "campaign-id")
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/campaigns/%s", url.PathEscape(id))
	return runAdsRequest(ctx, cmd, http.MethodGet, path, nil, nil, "ads campaigns retrieve")
}

func handleAdsCampaignsList(ctx context.Context, cmd *cli.Command) error {
	if err := rejectExtraArgs(cmd); err != nil {
		return err
	}
	return runAdsRequest(ctx, cmd, http.MethodGet, "/campaigns", paginationQuery(cmd), nil, "ads campaigns list")
}

func handleAdsCampaignsUpdate(ctx context.Context, cmd *cli.Command) error {
	id, err := idFromFlagOrArg(cmd, "campaign-id")
	if err != nil {
		return err
	}
	body := map[string]any{}
	setPtrStringIfSet(body, cmd, "name", "name")
	setPtrStringIfSet(body, cmd, "description", "description")
	setPtrStringIfSet(body, cmd, "status", "status")
	setPtrInt64IfSet(body, cmd, "start-time", "start_time")
	setPtrInt64IfSet(body, cmd, "end-time", "end_time")
	if cmd.IsSet("lifetime-spend-limit-micros") {
		body["budget"] = map[string]any{"lifetime_spend_limit_micros": int64Flag(cmd, "lifetime-spend-limit-micros")}
	}
	if cmd.IsSet("targeting") {
		body["targeting"] = mapFlag(cmd, "targeting")
	} else if targeting := targetingFromCountryFlags(cmd); len(targeting) > 0 {
		body["targeting"] = targeting
	}
	if len(body) == 0 {
		return fmt.Errorf("No campaign fields set; pass at least one update flag")
	}
	path := fmt.Sprintf("/campaigns/%s", url.PathEscape(id))
	return runAdsRequest(ctx, cmd, http.MethodPost, path, nil, body, "ads campaigns update")
}

func handleAdsCampaignsActivate(ctx context.Context, cmd *cli.Command) error {
	return handleAdsState(ctx, cmd, "campaign-id", "/campaigns", "activate", "ads campaigns activate")
}

func handleAdsCampaignsPause(ctx context.Context, cmd *cli.Command) error {
	return handleAdsState(ctx, cmd, "campaign-id", "/campaigns", "pause", "ads campaigns pause")
}

func handleAdsCampaignsArchive(ctx context.Context, cmd *cli.Command) error {
	return handleAdsState(ctx, cmd, "campaign-id", "/campaigns", "archive", "ads campaigns archive")
}

func handleAdsAdGroupsCreate(ctx context.Context, cmd *cli.Command) error {
	if err := rejectExtraArgs(cmd); err != nil {
		return err
	}
	body := map[string]any{
		"campaign_id": cmd.String("campaign-id"),
		"name":        cmd.String("name"),
		"status":      cmd.String("status"),
		"bidding_config": map[string]any{
			"billing_event_type": cmd.String("billing-event-type"),
			"max_bid_micros":     int64Flag(cmd, "max-bid-micros"),
		},
	}
	setStringIfSet(body, cmd, "description", "description")
	setStringSliceIfSet(body, cmd, "context-hint", "context_hints")
	return runAdsRequest(ctx, cmd, http.MethodPost, "/ad_groups", nil, body, "ads ad-groups create")
}

func handleAdsAdGroupsRetrieve(ctx context.Context, cmd *cli.Command) error {
	id, err := idFromFlagOrArg(cmd, "ad-group-id")
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/ad_groups/%s", url.PathEscape(id))
	return runAdsRequest(ctx, cmd, http.MethodGet, path, nil, nil, "ads ad-groups retrieve")
}

func handleAdsAdGroupsList(ctx context.Context, cmd *cli.Command) error {
	if err := rejectExtraArgs(cmd); err != nil {
		return err
	}
	query := paginationQuery(cmd)
	query.Set("campaign_id", cmd.String("campaign-id"))
	return runAdsRequest(ctx, cmd, http.MethodGet, "/ad_groups", query, nil, "ads ad-groups list")
}

func handleAdsAdGroupsUpdate(ctx context.Context, cmd *cli.Command) error {
	id, err := idFromFlagOrArg(cmd, "ad-group-id")
	if err != nil {
		return err
	}
	body := map[string]any{}
	setPtrStringIfSet(body, cmd, "name", "name")
	setPtrStringIfSet(body, cmd, "description", "description")
	setStringSliceIfSet(body, cmd, "context-hint", "context_hints")
	setPtrStringIfSet(body, cmd, "status", "status")
	if cmd.IsSet("bidding-config") {
		body["bidding_config"] = mapFlag(cmd, "bidding-config")
	} else if cmd.IsSet("billing-event-type") || cmd.IsSet("max-bid-micros") {
		if !cmd.IsSet("billing-event-type") || !cmd.IsSet("max-bid-micros") {
			return fmt.Errorf("Updating bidding_config requires both --billing-event-type and --max-bid-micros, or pass --bidding-config")
		}
		body["bidding_config"] = map[string]any{
			"billing_event_type": cmd.String("billing-event-type"),
			"max_bid_micros":     int64Flag(cmd, "max-bid-micros"),
		}
	}
	if len(body) == 0 {
		return fmt.Errorf("No ad group fields set; pass at least one update flag")
	}
	path := fmt.Sprintf("/ad_groups/%s", url.PathEscape(id))
	return runAdsRequest(ctx, cmd, http.MethodPost, path, nil, body, "ads ad-groups update")
}

func handleAdsAdGroupsActivate(ctx context.Context, cmd *cli.Command) error {
	return handleAdsState(ctx, cmd, "ad-group-id", "/ad_groups", "activate", "ads ad-groups activate")
}

func handleAdsAdGroupsPause(ctx context.Context, cmd *cli.Command) error {
	return handleAdsState(ctx, cmd, "ad-group-id", "/ad_groups", "pause", "ads ad-groups pause")
}

func handleAdsAdGroupsArchive(ctx context.Context, cmd *cli.Command) error {
	return handleAdsState(ctx, cmd, "ad-group-id", "/ad_groups", "archive", "ads ad-groups archive")
}

func handleAdsAdsCreate(ctx context.Context, cmd *cli.Command) error {
	if err := rejectExtraArgs(cmd); err != nil {
		return err
	}
	creative, err := creativeFromFlags(cmd, true)
	if err != nil {
		return err
	}
	body := map[string]any{
		"ad_group_id": cmd.String("ad-group-id"),
		"name":        cmd.String("name"),
		"status":      cmd.String("status"),
		"creative":    creative,
	}
	return runAdsRequest(ctx, cmd, http.MethodPost, "/ads", nil, body, "ads ads create")
}

func handleAdsAdsRetrieve(ctx context.Context, cmd *cli.Command) error {
	id, err := idFromFlagOrArg(cmd, "ad-id")
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/ads/%s", url.PathEscape(id))
	return runAdsRequest(ctx, cmd, http.MethodGet, path, nil, nil, "ads ads retrieve")
}

func handleAdsAdsList(ctx context.Context, cmd *cli.Command) error {
	if err := rejectExtraArgs(cmd); err != nil {
		return err
	}
	query := paginationQuery(cmd)
	query.Set("ad_group_id", cmd.String("ad-group-id"))
	return runAdsRequest(ctx, cmd, http.MethodGet, "/ads", query, nil, "ads ads list")
}

func handleAdsAdsUpdate(ctx context.Context, cmd *cli.Command) error {
	id, err := idFromFlagOrArg(cmd, "ad-id")
	if err != nil {
		return err
	}
	body := map[string]any{}
	setPtrStringIfSet(body, cmd, "name", "name")
	setPtrStringIfSet(body, cmd, "status", "status")
	if cmd.IsSet("creative") {
		body["creative"] = mapFlag(cmd, "creative")
	} else if anyCreativeFlagSet(cmd) {
		creative, err := creativeFromFlags(cmd, true)
		if err != nil {
			return err
		}
		body["creative"] = creative
	}
	if len(body) == 0 {
		return fmt.Errorf("No ad fields set; pass at least one update flag")
	}
	path := fmt.Sprintf("/ads/%s", url.PathEscape(id))
	return runAdsRequest(ctx, cmd, http.MethodPost, path, nil, body, "ads ads update")
}

func handleAdsAdsActivate(ctx context.Context, cmd *cli.Command) error {
	return handleAdsState(ctx, cmd, "ad-id", "/ads", "activate", "ads ads activate")
}

func handleAdsAdsPause(ctx context.Context, cmd *cli.Command) error {
	return handleAdsState(ctx, cmd, "ad-id", "/ads", "pause", "ads ads pause")
}

func handleAdsAdsArchive(ctx context.Context, cmd *cli.Command) error {
	return handleAdsState(ctx, cmd, "ad-id", "/ads", "archive", "ads ads archive")
}

func handleAdsFilesUpload(ctx context.Context, cmd *cli.Command) error {
	if err := rejectExtraArgs(cmd); err != nil {
		return err
	}
	filePath, hasFile := stringFlagIfSet(cmd, "file")
	imageURL, hasImageURL := stringFlagIfSet(cmd, "image-url")
	if hasFile == hasImageURL {
		return fmt.Errorf("Pass exactly one of --file or --image-url")
	}
	if hasImageURL {
		return runAdsRequest(ctx, cmd, http.MethodPost, "/upload", nil, map[string]any{"image_url": imageURL}, "ads files upload")
	}
	return runAdsMultipartUpload(ctx, cmd, "/upload", filePath, "ads files upload")
}

func handleAdsInsightsAccount(ctx context.Context, cmd *cli.Command) error {
	if err := rejectExtraArgs(cmd); err != nil {
		return err
	}
	return runAdsRequest(ctx, cmd, http.MethodGet, "/ad_account/insights", insightsQuery(cmd), nil, "ads insights account")
}

func handleAdsInsightsCampaign(ctx context.Context, cmd *cli.Command) error {
	id, err := idFromFlagOrArg(cmd, "campaign-id")
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/campaigns/%s/insights", url.PathEscape(id))
	return runAdsRequest(ctx, cmd, http.MethodGet, path, insightsQuery(cmd), nil, "ads insights campaign")
}

func handleAdsInsightsAdGroup(ctx context.Context, cmd *cli.Command) error {
	id, err := idFromFlagOrArg(cmd, "ad-group-id")
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/ad_groups/%s/insights", url.PathEscape(id))
	return runAdsRequest(ctx, cmd, http.MethodGet, path, insightsQuery(cmd), nil, "ads insights ad-group")
}

func handleAdsInsightsAd(ctx context.Context, cmd *cli.Command) error {
	id, err := idFromFlagOrArg(cmd, "ad-id")
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/ads/%s/insights", url.PathEscape(id))
	return runAdsRequest(ctx, cmd, http.MethodGet, path, insightsQuery(cmd), nil, "ads insights ad")
}

func handleAdsState(ctx context.Context, cmd *cli.Command, idFlag, resourcePath, transition, title string) error {
	id, err := idFromFlagOrArg(cmd, idFlag)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("%s/%s/%s", resourcePath, url.PathEscape(id), transition)
	return runAdsRequest(ctx, cmd, http.MethodPost, path, nil, nil, title)
}

func runAdsRequest(ctx context.Context, cmd *cli.Command, method, path string, query url.Values, jsonBody any, title string) error {
	var body io.Reader
	if jsonBody != nil {
		buf, err := json.Marshal(jsonBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}

	req, err := newAdsRequest(ctx, cmd, method, path, query, body)
	if err != nil {
		return err
	}
	if jsonBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return doAdsRequest(cmd, req, title)
}

func runAdsMultipartUpload(ctx context.Context, cmd *cli.Command, path, filePath, title string) error {
	buf := new(bytes.Buffer)
	writer := multipart.NewWriter(buf)

	var reader io.Reader
	filename := filepath.Base(filePath)
	if filePath == "-" {
		reader = os.Stdin
		filename = "stdin"
	} else {
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer file.Close()
		reader = file
	}

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, reader); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := newAdsRequest(ctx, cmd, http.MethodPost, path, nil, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return doAdsRequest(cmd, req, title)
}

func newAdsRequest(ctx context.Context, cmd *cli.Command, method, path string, query url.Values, body io.Reader) (*http.Request, error) {
	adsAPIKey := strings.TrimSpace(cmd.Root().String("ads-api-key"))
	if adsAPIKey == "" {
		return nil, fmt.Errorf("Ads API key required; set OPENAI_ADS_API_KEY or pass --ads-api-key")
	}

	baseURL := defaultAdsBaseURL
	if cmd.Root().IsSet("base-url") {
		baseURL = strings.TrimRight(cmd.Root().String("base-url"), "/")
	}
	endpoint, err := url.Parse(baseURL + path)
	if err != nil {
		return nil, err
	}
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+adsAPIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("OpenAI/CLI %s", Version))
	req.Header.Set("X-Stainless-Lang", "cli")
	req.Header.Set("X-Stainless-Package-Version", Version)
	req.Header.Set("X-Stainless-Runtime", "cli")
	req.Header.Set("X-Stainless-CLI-Command", cmd.FullName())
	return req, nil
}

func doAdsRequest(cmd *cli.Command, req *http.Request, title string) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	res, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(res))
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf("Ads API request failed: %s: %s", resp.Status, message)
	}
	if len(bytes.TrimSpace(res)) == 0 {
		res = []byte("null")
	}
	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          title,
		Transform:      transform,
	})
}

func idFromFlagOrArg(cmd *cli.Command, flagName string) (string, error) {
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet(flagName) && len(unusedArgs) > 0 {
		if err := cmd.Set(flagName, unusedArgs[0]); err != nil {
			return "", err
		}
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return "", fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}
	value := cmd.String(flagName)
	if value == "" {
		return "", fmt.Errorf("Required flag %q not set\nRun '%s --help' for usage information", flagName, cmd.FullName())
	}
	return value, nil
}

func rejectExtraArgs(cmd *cli.Command) error {
	if unusedArgs := cmd.Args().Slice(); len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}
	return nil
}

func paginationQuery(cmd *cli.Command) url.Values {
	query := url.Values{}
	addInt64QueryIfSet(query, cmd, "limit", "limit")
	addStringQueryIfSet(query, cmd, "after", "after")
	addStringQueryIfSet(query, cmd, "before", "before")
	addStringQueryIfSet(query, cmd, "order", "order")
	return query
}

func insightsQuery(cmd *cli.Command) url.Values {
	query := url.Values{}
	addStringQueryIfSet(query, cmd, "time-granularity", "time_granularity")
	addStringQueryIfSet(query, cmd, "aggregation-level", "aggregation_level")
	addInt64QueryIfSet(query, cmd, "limit", "limit")
	addStringQueryIfSet(query, cmd, "after", "after")
	addStringQueryIfSet(query, cmd, "before", "before")
	addStringSliceQueryIfSet(query, cmd, "field", "fields[]")
	addStringSliceQueryIfSet(query, cmd, "time-range", "time_ranges[]")
	addStringSliceQueryIfSet(query, cmd, "filter", "filters[]")
	addStringSliceQueryIfSet(query, cmd, "sort", "sort[]")
	return query
}

func targetingFromCountryFlags(cmd *cli.Command) map[string]any {
	targeting := map[string]any{}
	if countries := stringSliceFlag(cmd, "country"); len(countries) > 0 {
		targeting["locations"] = map[string]any{"countries": countries}
	}
	if countries := stringSliceFlag(cmd, "exclude-country"); len(countries) > 0 {
		targeting["excluded_locations"] = map[string]any{"countries": countries}
	}
	return targeting
}

func creativeFromFlags(cmd *cli.Command, requireAll bool) (map[string]any, error) {
	missing := []string{}
	for _, name := range []string{"title", "body", "target-url", "file-id"} {
		if !cmd.IsSet(name) {
			missing = append(missing, "--"+name)
		}
	}
	if requireAll && len(missing) > 0 {
		return nil, fmt.Errorf("Creative requires %s", strings.Join(missing, ", "))
	}
	creative := map[string]any{"type": cmd.String("creative-type")}
	setStringIfSet(creative, cmd, "title", "title")
	setStringIfSet(creative, cmd, "body", "body")
	setStringIfSet(creative, cmd, "target-url", "target_url")
	setStringIfSet(creative, cmd, "file-id", "file_id")
	return creative, nil
}

func anyCreativeFlagSet(cmd *cli.Command) bool {
	for _, name := range []string{"creative-type", "title", "body", "target-url", "file-id"} {
		if cmd.IsSet(name) {
			return true
		}
	}
	return false
}

func addStringQueryIfSet(query url.Values, cmd *cli.Command, flagName, queryName string) {
	if cmd.IsSet(flagName) {
		query.Set(queryName, cmd.String(flagName))
	}
}

func addInt64QueryIfSet(query url.Values, cmd *cli.Command, flagName, queryName string) {
	if cmd.IsSet(flagName) {
		query.Set(queryName, fmt.Sprintf("%d", int64Flag(cmd, flagName)))
	}
}

func addStringSliceQueryIfSet(query url.Values, cmd *cli.Command, flagName, queryName string) {
	for _, value := range stringSliceFlag(cmd, flagName) {
		query.Add(queryName, value)
	}
}

func setStringIfSet(body map[string]any, cmd *cli.Command, flagName, bodyName string) {
	if cmd.IsSet(flagName) {
		body[bodyName] = cmd.String(flagName)
	}
}

func setPtrStringIfSet(body map[string]any, cmd *cli.Command, flagName, bodyName string) {
	if !cmd.IsSet(flagName) {
		return
	}
	if value := cmd.Value(flagName); value != nil {
		if ptr, ok := value.(*string); ok {
			if ptr == nil {
				body[bodyName] = nil
			} else {
				body[bodyName] = *ptr
			}
			return
		}
	}
	body[bodyName] = nil
}

func setInt64IfSet(body map[string]any, cmd *cli.Command, flagName, bodyName string) {
	if cmd.IsSet(flagName) {
		body[bodyName] = int64Flag(cmd, flagName)
	}
}

func setPtrInt64IfSet(body map[string]any, cmd *cli.Command, flagName, bodyName string) {
	if !cmd.IsSet(flagName) {
		return
	}
	if value := cmd.Value(flagName); value != nil {
		if ptr, ok := value.(*int64); ok {
			if ptr == nil {
				body[bodyName] = nil
			} else {
				body[bodyName] = *ptr
			}
			return
		}
	}
	body[bodyName] = nil
}

func setStringSliceIfSet(body map[string]any, cmd *cli.Command, flagName, bodyName string) {
	if values := stringSliceFlag(cmd, flagName); len(values) > 0 {
		body[bodyName] = values
	}
}

func stringFlagIfSet(cmd *cli.Command, flagName string) (string, bool) {
	if !cmd.IsSet(flagName) {
		return "", false
	}
	return cmd.String(flagName), true
}

func stringSliceFlag(cmd *cli.Command, flagName string) []string {
	value := cmd.Value(flagName)
	if value == nil {
		return nil
	}
	values, _ := value.([]string)
	return values
}

func int64Flag(cmd *cli.Command, flagName string) int64 {
	value := cmd.Value(flagName)
	if value == nil {
		return 0
	}
	result, _ := value.(int64)
	return result
}

func mapFlag(cmd *cli.Command, flagName string) any {
	value := cmd.Value(flagName)
	if value == nil {
		return nil
	}
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return nil
}
