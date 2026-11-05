package audio

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAudioLocalRoutingUsesRegisteredAliasesAndParsedLeaves(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "audio", "history"},
		{"openai", "sounds", "history"},
		{"openai", "sounds", "--project", "inspect", "past"},
		{"openai", "--project", "speak", "sounds", "history"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&cli.StringFlag{Name: "project"}},
				Commands: []*cli.Command{{Name: "audio", Aliases: []string{"sounds"}, Commands: []*cli.Command{
					{Name: "history", Aliases: []string{"past"}, Metadata: map[string]any{LocalMetadata: true}},
					{Name: "speak"},
				}}},
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					require.True(t, IsLocalCommand(cmd))
					return ctx, nil
				},
			}
			require.NoError(t, root.Run(t.Context(), args))
		})
	}
}

func TestAudioLocalRoutingDoesNotAcceptARequestValue(t *testing.T) {
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "project"}},
		Commands: []*cli.Command{{Name: "audio", Commands: []*cli.Command{
			{Name: "history", Metadata: map[string]any{LocalMetadata: true}},
			{Name: "speak", Flags: []cli.Flag{&cli.StringFlag{Name: "input"}}},
		}}},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			require.False(t, IsLocalCommand(cmd))
			return ctx, nil
		},
	}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--project", "history", "audio", "speak", "--input", "inspect"}))
}
