// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/openai/openai-cli/internal/requestflag"
	docs "github.com/urfave/cli-docs/v3"
	"github.com/urfave/cli/v3"
)

var (
	Command            *cli.Command
	CommandErrorBuffer bytes.Buffer
)

func init() {
	Command = &cli.Command{
		Name:      "openai",
		Usage:     "CLI for the openai API",
		Suggest:   true,
		Version:   Version,
		ErrWriter: &CommandErrorBuffer,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "debug",
				Usage: "Enable debug logging",
			},
			&cli.StringFlag{
				Name:        "base-url",
				DefaultText: "url",
				Usage:       "Override the base URL for API requests",
				Validator: func(baseURL string) error {
					return ValidateBaseURL(baseURL, "--base-url")
				},
			},
			&requestflag.Flag[string]{
				Name:    "ads-api-key",
				Usage:   "Ads API bearer token.",
				Sources: cli.EnvVars("OPENAI_ADS_API_KEY"),
			},
			&requestflag.Flag[string]{
				Name:        "ads-base-url",
				Default:     defaultAdsBaseURL,
				DefaultText: defaultAdsBaseURL,
				Usage:       "Override the base URL for Ads API requests",
				Sources:     cli.EnvVars("OPENAI_ADS_BASE_URL"),
				Validator: func(baseURL string) error {
					return ValidateBaseURL(baseURL, "--ads-base-url")
				},
			},
			&cli.StringFlag{
				Name:  "format",
				Usage: "The format for displaying response data (one of: " + strings.Join(OutputFormats, ", ") + ")",
				Value: "auto",
				Validator: func(format string) error {
					if !slices.Contains(OutputFormats, strings.ToLower(format)) {
						return fmt.Errorf("format must be one of: %s", strings.Join(OutputFormats, ", "))
					}
					return nil
				},
			},
			&cli.StringFlag{
				Name:  "format-error",
				Usage: "The format for displaying error data (one of: " + strings.Join(OutputFormats, ", ") + ")",
				Value: "auto",
				Validator: func(format string) error {
					if !slices.Contains(OutputFormats, strings.ToLower(format)) {
						return fmt.Errorf("format must be one of: %s", strings.Join(OutputFormats, ", "))
					}
					return nil
				},
			},
			&cli.StringFlag{
				Name:  "transform",
				Usage: "The GJSON transformation for data output.",
			},
			&cli.StringFlag{
				Name:  "transform-error",
				Usage: "The GJSON transformation for errors.",
			},
			&cli.BoolFlag{
				Name:    "raw-output",
				Aliases: []string{"r"},
				Usage:   "If the result is a string, print it without JSON quotes. This can be useful for making output transforms talk to non-JSON-based systems.",
			},
			&requestflag.Flag[string]{
				Name:    "api-key",
				Sources: cli.EnvVars("OPENAI_API_KEY"),
			},
			&requestflag.Flag[string]{
				Name:    "admin-api-key",
				Sources: cli.EnvVars("OPENAI_ADMIN_KEY"),
			},
			&requestflag.Flag[string]{
				Name:    "organization",
				Sources: cli.EnvVars("OPENAI_ORG_ID"),
			},
			&requestflag.Flag[string]{
				Name:    "project",
				Sources: cli.EnvVars("OPENAI_PROJECT_ID"),
			},
			&requestflag.Flag[string]{
				Name:    "webhook-secret",
				Sources: cli.EnvVars("OPENAI_WEBHOOK_SECRET"),
			},
		},
		Commands: []*cli.Command{
			&adsCommand,
			{
				Name:     "completions",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&completionsCreate,
				},
			},
			{
				Name:     "chat:completions",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&chatCompletionsCreate,
					&chatCompletionsRetrieve,
					&chatCompletionsUpdate,
					&chatCompletionsList,
					&chatCompletionsDelete,
				},
			},
			{
				Name:     "chat:completions:messages",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&chatCompletionsMessagesList,
				},
			},
			{
				Name:     "embeddings",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&embeddingsCreate,
				},
			},
			{
				Name:     "files",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&filesCreate,
					&filesRetrieve,
					&filesList,
					&filesDelete,
					&filesContent,
				},
			},
			{
				Name:     "images",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&imagesCreateVariation,
					&imagesEdit,
					&imagesGenerate,
				},
			},
			{
				Name:     "audio:transcriptions",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&audioTranscriptionsCreate,
				},
			},
			{
				Name:     "audio:translations",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&audioTranslationsCreate,
				},
			},
			{
				Name:     "audio:speech",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&audioSpeechCreate,
				},
			},
			{
				Name:     "moderations",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&moderationsCreate,
				},
			},
			{
				Name:     "models",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&modelsRetrieve,
					&modelsList,
					&modelsDelete,
				},
			},
			{
				Name:     "fine-tuning:jobs",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&fineTuningJobsCreate,
					&fineTuningJobsRetrieve,
					&fineTuningJobsList,
					&fineTuningJobsCancel,
					&fineTuningJobsListEvents,
					&fineTuningJobsPause,
					&fineTuningJobsResume,
				},
			},
			{
				Name:     "fine-tuning:jobs:checkpoints",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&fineTuningJobsCheckpointsList,
				},
			},
			{
				Name:     "fine-tuning:checkpoints:permissions",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&fineTuningCheckpointsPermissionsCreate,
					&fineTuningCheckpointsPermissionsRetrieve,
					&fineTuningCheckpointsPermissionsList,
					&fineTuningCheckpointsPermissionsDelete,
				},
			},
			{
				Name:     "fine-tuning:alpha:graders",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&fineTuningAlphaGradersRun,
					&fineTuningAlphaGradersValidate,
				},
			},
			{
				Name:     "vector-stores",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&vectorStoresCreate,
					&vectorStoresRetrieve,
					&vectorStoresUpdate,
					&vectorStoresList,
					&vectorStoresDelete,
					&vectorStoresSearch,
				},
			},
			{
				Name:     "vector-stores:files",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&vectorStoresFilesCreate,
					&vectorStoresFilesRetrieve,
					&vectorStoresFilesUpdate,
					&vectorStoresFilesList,
					&vectorStoresFilesDelete,
					&vectorStoresFilesContent,
				},
			},
			{
				Name:     "vector-stores:file-batches",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&vectorStoresFileBatchesCreate,
					&vectorStoresFileBatchesRetrieve,
					&vectorStoresFileBatchesCancel,
					&vectorStoresFileBatchesListFiles,
				},
			},
			{
				Name:     "beta:chatkit:sessions",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&betaChatKitSessionsCreate,
					&betaChatKitSessionsCancel,
				},
			},
			{
				Name:     "beta:chatkit:threads",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&betaChatKitThreadsRetrieve,
					&betaChatKitThreadsList,
					&betaChatKitThreadsDelete,
					&betaChatKitThreadsListItems,
				},
			},
			{
				Name:     "beta:assistants",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&betaAssistantsCreate,
					&betaAssistantsRetrieve,
					&betaAssistantsUpdate,
					&betaAssistantsList,
					&betaAssistantsDelete,
				},
			},
			{
				Name:     "beta:threads",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&betaThreadsCreate,
					&betaThreadsRetrieve,
					&betaThreadsUpdate,
					&betaThreadsDelete,
					&betaThreadsCreateAndRun,
				},
			},
			{
				Name:     "beta:threads:runs",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&betaThreadsRunsCreate,
					&betaThreadsRunsRetrieve,
					&betaThreadsRunsUpdate,
					&betaThreadsRunsList,
					&betaThreadsRunsCancel,
					&betaThreadsRunsSubmitToolOutputs,
				},
			},
			{
				Name:     "beta:threads:runs:steps",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&betaThreadsRunsStepsRetrieve,
					&betaThreadsRunsStepsList,
				},
			},
			{
				Name:     "beta:threads:messages",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&betaThreadsMessagesCreate,
					&betaThreadsMessagesRetrieve,
					&betaThreadsMessagesUpdate,
					&betaThreadsMessagesList,
					&betaThreadsMessagesDelete,
				},
			},
			{
				Name:     "batches",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&batchesCreate,
					&batchesRetrieve,
					&batchesList,
					&batchesCancel,
				},
			},
			{
				Name:     "uploads",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&uploadsCreate,
					&uploadsCancel,
					&uploadsComplete,
				},
			},
			{
				Name:     "uploads:parts",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&uploadsPartsCreate,
				},
			},
			{
				Name:     "admin:organization:audit-logs",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationAuditLogsList,
				},
			},
			{
				Name:     "admin:organization:admin-api-keys",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationAdminAPIKeysCreate,
					&adminOrganizationAdminAPIKeysRetrieve,
					&adminOrganizationAdminAPIKeysList,
					&adminOrganizationAdminAPIKeysDelete,
				},
			},
			{
				Name:     "admin:organization:usage",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationUsageAudioSpeeches,
					&adminOrganizationUsageAudioTranscriptions,
					&adminOrganizationUsageCodeInterpreterSessions,
					&adminOrganizationUsageCompletions,
					&adminOrganizationUsageCosts,
					&adminOrganizationUsageEmbeddings,
					&adminOrganizationUsageImages,
					&adminOrganizationUsageModerations,
					&adminOrganizationUsageVectorStores,
				},
			},
			{
				Name:     "admin:organization:invites",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationInvitesCreate,
					&adminOrganizationInvitesRetrieve,
					&adminOrganizationInvitesList,
					&adminOrganizationInvitesDelete,
				},
			},
			{
				Name:     "admin:organization:users",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationUsersRetrieve,
					&adminOrganizationUsersUpdate,
					&adminOrganizationUsersList,
					&adminOrganizationUsersDelete,
				},
			},
			{
				Name:     "admin:organization:users:roles",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationUsersRolesCreate,
					&adminOrganizationUsersRolesList,
					&adminOrganizationUsersRolesDelete,
				},
			},
			{
				Name:     "admin:organization:groups",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationGroupsCreate,
					&adminOrganizationGroupsUpdate,
					&adminOrganizationGroupsList,
					&adminOrganizationGroupsDelete,
				},
			},
			{
				Name:     "admin:organization:groups:users",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationGroupsUsersCreate,
					&adminOrganizationGroupsUsersList,
					&adminOrganizationGroupsUsersDelete,
				},
			},
			{
				Name:     "admin:organization:groups:roles",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationGroupsRolesCreate,
					&adminOrganizationGroupsRolesList,
					&adminOrganizationGroupsRolesDelete,
				},
			},
			{
				Name:     "admin:organization:roles",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationRolesCreate,
					&adminOrganizationRolesUpdate,
					&adminOrganizationRolesList,
					&adminOrganizationRolesDelete,
				},
			},
			{
				Name:     "admin:organization:certificates",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationCertificatesCreate,
					&adminOrganizationCertificatesRetrieve,
					&adminOrganizationCertificatesUpdate,
					&adminOrganizationCertificatesList,
					&adminOrganizationCertificatesDelete,
					&adminOrganizationCertificatesActivate,
					&adminOrganizationCertificatesDeactivate,
				},
			},
			{
				Name:     "admin:organization:projects",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsCreate,
					&adminOrganizationProjectsRetrieve,
					&adminOrganizationProjectsUpdate,
					&adminOrganizationProjectsList,
					&adminOrganizationProjectsArchive,
				},
			},
			{
				Name:     "admin:organization:projects:users",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsUsersCreate,
					&adminOrganizationProjectsUsersRetrieve,
					&adminOrganizationProjectsUsersUpdate,
					&adminOrganizationProjectsUsersList,
					&adminOrganizationProjectsUsersDelete,
				},
			},
			{
				Name:     "admin:organization:projects:users:roles",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsUsersRolesCreate,
					&adminOrganizationProjectsUsersRolesList,
					&adminOrganizationProjectsUsersRolesDelete,
				},
			},
			{
				Name:     "admin:organization:projects:service-accounts",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsServiceAccountsCreate,
					&adminOrganizationProjectsServiceAccountsRetrieve,
					&adminOrganizationProjectsServiceAccountsList,
					&adminOrganizationProjectsServiceAccountsDelete,
				},
			},
			{
				Name:     "admin:organization:projects:api-keys",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsAPIKeysRetrieve,
					&adminOrganizationProjectsAPIKeysList,
					&adminOrganizationProjectsAPIKeysDelete,
				},
			},
			{
				Name:     "admin:organization:projects:rate-limits",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsRateLimitsListRateLimits,
					&adminOrganizationProjectsRateLimitsUpdateRateLimit,
				},
			},
			{
				Name:     "admin:organization:projects:groups",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsGroupsCreate,
					&adminOrganizationProjectsGroupsList,
					&adminOrganizationProjectsGroupsDelete,
				},
			},
			{
				Name:     "admin:organization:projects:groups:roles",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsGroupsRolesCreate,
					&adminOrganizationProjectsGroupsRolesList,
					&adminOrganizationProjectsGroupsRolesDelete,
				},
			},
			{
				Name:     "admin:organization:projects:roles",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsRolesCreate,
					&adminOrganizationProjectsRolesUpdate,
					&adminOrganizationProjectsRolesList,
					&adminOrganizationProjectsRolesDelete,
				},
			},
			{
				Name:     "admin:organization:projects:certificates",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&adminOrganizationProjectsCertificatesList,
					&adminOrganizationProjectsCertificatesActivate,
					&adminOrganizationProjectsCertificatesDeactivate,
				},
			},
			{
				Name:     "responses",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&responsesCreate,
					&responsesRetrieve,
					&responsesDelete,
					&responsesCancel,
					&responsesCompact,
				},
			},
			{
				Name:     "responses:input-items",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&responsesInputItemsList,
				},
			},
			{
				Name:     "responses:input-tokens",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&responsesInputTokensCount,
				},
			},
			{
				Name:     "realtime:client-secrets",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&realtimeClientSecretsCreate,
				},
			},
			{
				Name:     "realtime:calls",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&realtimeCallsAccept,
					&realtimeCallsHangup,
					&realtimeCallsRefer,
					&realtimeCallsReject,
				},
			},
			{
				Name:     "conversations",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&conversationsCreate,
					&conversationsRetrieve,
					&conversationsUpdate,
					&conversationsDelete,
				},
			},
			{
				Name:     "conversations:items",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&conversationsItemsCreate,
					&conversationsItemsRetrieve,
					&conversationsItemsList,
					&conversationsItemsDelete,
				},
			},
			{
				Name:     "containers",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&containersCreate,
					&containersRetrieve,
					&containersList,
					&containersDelete,
				},
			},
			{
				Name:     "containers:files",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&containersFilesCreate,
					&containersFilesRetrieve,
					&containersFilesList,
					&containersFilesDelete,
				},
			},
			{
				Name:     "containers:files:content",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&containersFilesContentRetrieve,
				},
			},
			{
				Name:     "skills",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&skillsCreate,
					&skillsRetrieve,
					&skillsUpdate,
					&skillsList,
					&skillsDelete,
				},
			},
			{
				Name:     "skills:content",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&skillsContentRetrieve,
				},
			},
			{
				Name:     "skills:versions",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&skillsVersionsCreate,
					&skillsVersionsRetrieve,
					&skillsVersionsList,
					&skillsVersionsDelete,
				},
			},
			{
				Name:     "skills:versions:content",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&skillsVersionsContentRetrieve,
				},
			},
			{
				Name:     "videos",
				Category: "API RESOURCE",
				Suggest:  true,
				Commands: []*cli.Command{
					&videosCreate,
					&videosRetrieve,
					&videosList,
					&videosDelete,
					&videosCreateCharacter,
					&videosDownloadContent,
					&videosEdit,
					&videosExtend,
					&videosGetCharacter,
					&videosRemix,
				},
			},
			{
				Name:            "@manpages",
				Usage:           "Generate documentation for 'man'",
				UsageText:       "openai @manpages [-o openai.1] [--gzip]",
				Hidden:          true,
				Action:          generateManpages,
				HideHelpCommand: true,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "output",
						Aliases: []string{"o"},
						Usage:   "write manpages to the given folder",
						Value:   "man",
					},
					&cli.BoolFlag{
						Name:    "gzip",
						Aliases: []string{"z"},
						Usage:   "output gzipped manpage files to .gz",
						Value:   true,
					},
					&cli.BoolFlag{
						Name:    "text",
						Aliases: []string{"z"},
						Usage:   "output uncompressed text files",
						Value:   false,
					},
				},
			},
			{
				Name:            "__complete",
				Hidden:          true,
				HideHelpCommand: true,
				Action:          autocomplete.ExecuteShellCompletion,
			},
			{
				Name:            "@completion",
				Hidden:          true,
				HideHelpCommand: true,
				Action:          autocomplete.OutputCompletionScript,
			},
		},
		HideHelpCommand: true,
	}
}

func generateManpages(ctx context.Context, c *cli.Command) error {
	manpage, err := docs.ToManWithSection(Command, 1)
	if err != nil {
		return err
	}
	dir := c.String("output")
	err = os.MkdirAll(filepath.Join(dir, "man1"), 0755)
	if err != nil {
		// handle error
	}
	if c.Bool("text") {
		file, err := os.Create(filepath.Join(dir, "man1", "openai.1"))
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err := file.WriteString(manpage); err != nil {
			return err
		}
	}
	if c.Bool("gzip") {
		file, err := os.Create(filepath.Join(dir, "man1", "openai.1.gz"))
		if err != nil {
			return err
		}
		defer file.Close()
		gzWriter := gzip.NewWriter(file)
		defer gzWriter.Close()
		_, err = gzWriter.Write([]byte(manpage))
		if err != nil {
			return err
		}
	}
	fmt.Printf("Wrote manpages to %s\n", dir)
	return nil
}
