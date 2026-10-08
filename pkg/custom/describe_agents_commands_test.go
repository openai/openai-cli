package custom

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAgentsDescriptionsDoNotRegisterMissingCommands(t *testing.T) {
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "models", Usage: "Existing models."}}}
	describeAgentsCommands(root)
	require.Len(t, root.Commands, 1)
	require.Nil(t, root.Command("beta:agents"))
	require.Equal(t, "Existing models.", root.Command("models").Usage)
}

func TestAgentsDescriptionsPreserveDeclaredFlagsAndActions(t *testing.T) {
	called := false
	stream := &cli.Command{Name: "stream", Flags: []cli.Flag{&cli.StringFlag{Name: "session-id"}},
		Action: func(context.Context, *cli.Command) error { called = true; return nil },
	}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "beta:agents:sessions:events", Commands: []*cli.Command{stream}}}}
	describeAgentsCommands(root)
	require.Len(t, stream.Flags, 1)
	require.Equal(t, "session-id", stream.Flags[0].Names()[0])
	require.Contains(t, stream.Description, "Ctrl+C stops local observation")
	require.Contains(t, stream.Description, "remote work continues")
	require.NoError(t, stream.Action(context.Background(), stream))
	require.True(t, called)
}
