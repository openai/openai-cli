package app

import (
	"context"
	"os"

	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/commands/admin"
	"github.com/openai/openai-cli/internal/commands/audio"
	"github.com/openai/openai-cli/internal/commands/codex"
	"github.com/openai/openai-cli/internal/commands/explore"
	filecommands "github.com/openai/openai-cli/internal/commands/files"
	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/openai/openai-cli/internal/commands/manpages"
	"github.com/openai/openai-cli/internal/commands/tokenize"
	"github.com/openai/openai-cli/internal/commands/webhooks"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

const configuredCommandMetadata = "openai-custom-configured"

// ConfigureCommand installs SDK-owned behavior after the generated command tree is complete.
func ConfigureCommand(root *cli.Command) {
	if root.Metadata == nil {
		root.Metadata = map[string]any{}
	}
	if configured, _ := root.Metadata[configuredCommandMetadata].(bool); configured {
		return
	}
	root.Metadata[configuredCommandMetadata] = true
	root.Flags = append(root.Flags, debugCaptureFlags()...)
	configureCertificateCompatibility(root)
	configureAudioVoices(root)
	requestconfig.ConfigureRootFlags(root)
	// Batch workflows preserve positional path inputs and need the same help annotations.
	configureBatchCommands(root)
	clihelp.DescribeRequestInputs(root)
	images.RegisterModels(root, ImageModelOptions())
	configureReadableOutput(root)
	configureModelsList(root)
	images.ConfigureSaving(root, ImageSavingOptions())
	images.ConfigureCompletion(root)
	images.ConfigureShellSetup(root, writeOutputReceipt)
	configureShellSuggestions(root)
	explore.ConfigureCompletion(root)
	explore.Register(root)
	images.RegisterPreview(root, ImagePreviewOptions())
	audio.ConfigureText(root)
	configureReadableSpeech(root)
	registerAdminSetup(root)
	admin.ConfigureCredentials(root)
	admin.ConfigureProjectLifecycle(root)
	webhooks.Configure(root, webhookCommandOptions())
	describeAgentsCommands(root)
	configureAgentsSessionCreation(root)
	localcommands.ConfigureSubgroups(root)
	localcommands.ConfigureTasks(root)
	registerAudioCommands(root)
	filecommands.Configure(root)
	tokenize.Register(root)
	codex.Register(root)
	localcommands.RemoveFineTuningCommands(root)
	localcommands.ConfigurePresentation(root)
	manpages.Configure(root, func(command *cli.Command, message string) error {
		return ReportSaveReceipt(command, os.Stderr, message, nil)
	})
	configureOutputPolicy(root)
	ConfigureCommandErrors(root)
	root.Flags = append(root.Flags, requestconfig.MTLSClientFlags()...)
	previousBefore := root.Before
	root.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
		if _, err := debugCaptureOptions(command); err != nil {
			return ctx, err
		}
		if previousBefore != nil {
			var err error
			ctx, err = previousBefore(ctx, command)
			if err != nil {
				return ctx, err
			}
		}
		if IsLocalUtilityCommand(command) {
			return ctx, nil
		}
		baseURL, source := command.Root().String("base-url"), "--base-url"
		if baseURL == "" {
			baseURL, source = os.Getenv("OPENAI_BASE_URL"), "OPENAI_BASE_URL"
		}
		if err := requestconfig.ValidateBaseURL(baseURL, source); err != nil {
			return ctx, err
		}
		return requestconfig.ConfigureMTLS(ctx, command.Root())
	}
	cli.SuggestCommand = clihelp.SuggestCommand
}
