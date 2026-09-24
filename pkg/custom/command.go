package custom

import (
	"context"

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
	root.Flags = append(root.Flags, mtlsClientFlags()...)
	previousBefore := root.Before
	root.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
		if previousBefore != nil {
			var err error
			ctx, err = previousBefore(ctx, command)
			if err != nil {
				return ctx, err
			}
		}
		return configureMTLS(ctx, command.Root())
	}
	cli.SuggestCommand = suggestCommand
}
