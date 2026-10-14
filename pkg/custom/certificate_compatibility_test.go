package custom

import (
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestConfigureCertificateCompatibility(t *testing.T) {
	certificate := &requestflag.Flag[string]{Name: "certificate", BodyPath: "certificate", Required: true}
	name := &requestflag.Flag[string]{Name: "name", BodyPath: "name"}
	create := &cli.Command{Name: "create", Flags: []cli.Flag{certificate, name}}
	root := &cli.Command{Commands: []*cli.Command{{Name: "admin:organization:certificates", Commands: []*cli.Command{create}}}}
	ConfigureCommand(root)
	ConfigureCommand(root)
	configureCertificateCompatibility(root)
	require.Len(t, create.Flags, 3)
	require.Same(t, certificate, create.Flags[0])
	require.Same(t, name, create.Flags[2])
	content, ok := create.Flags[1].(*requestflag.Flag[string])
	require.True(t, ok)
	require.Equal(t, "content", content.Name)
	require.Equal(t, "content", content.BodyPath)
	require.True(t, content.Hidden)
	require.False(t, content.Required)
	require.Empty(t, content.Aliases)
	require.True(t, certificate.Required)
}
