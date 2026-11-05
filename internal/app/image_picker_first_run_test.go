package app

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/auth"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAuthStatusSkipsFirstRunSetupByDeclaredCommand(t *testing.T) {
	root := audioFirstRunTestRoot()
	root.Flags = append(root.Flags, &cli.StringFlag{Name: "api-key"}, &cli.StringFlag{Name: "base-url"}, &cli.BoolFlag{Name: "debug"})
	auth.Register(root)
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"openai", "auth", "status"}, true},
		{[]string{"openai", "--api-key", "synthetic", "auth", "status"}, true},
		{[]string{"openai", "--api-key=synthetic", "auth", "--format=json", "status"}, true},
		{[]string{"openai", "help", "auth", "status"}, true},
		{[]string{"openai", "--", "auth", "status"}, true},
		{[]string{"openai", "auth", "--help"}, true},
		{[]string{"openai", "auth", "status", "--unknown"}, true},
		{[]string{"openai", "--debug=false", "auth", "status"}, true},
		{[]string{"openai", "--debug=bad", "auth", "status"}, true},
		{[]string{"openai", "--api-key", "auth", "models", "list"}, false},
		{[]string{"openai", "--header", "X-Test: auth", "models", "list"}, false},
		{[]string{"openai", "--base-url=auth", "models", "list"}, false},
		{[]string{"openai", "models", "list", "auth"}, false},
		{[]string{"openai", "--unknown", "auth", "status"}, false},
		{[]string{"openai", "--api-key", "auth"}, false},
		{[]string{"openai"}, false},
	} {
		require.Equal(t, tc.want, skipFirstRunSetup(root, tc.args), "%q", tc.args)
	}
	require.False(t, skipFirstRunSetup(nil, []string{"openai", "auth", "status"}))
	require.False(t, skipFirstRunSetup(&cli.Command{}, []string{"openai", "auth", "status"}))
}

func TestAuthFirstRunSetupUsesSelectedHelpCommand(t *testing.T) {
	root := audioFirstRunTestRoot()
	auth.Register(root)
	for _, tc := range []struct {
		args []string
		skip bool
	}{
		{[]string{"openai", "--all", "--help", "auth", "status"}, true},
		{[]string{"openai", "--all", "--help", "auth"}, true},
		{[]string{"openai", "--all", "--help", "--", "auth", "status"}, false},
		{[]string{"openai", "--help", "--", "auth", "status"}, false},
	} {
		_, help, err := ConfigureHelp(root, tc.args)
		require.NoError(t, err)
		require.True(t, help)
		require.Equal(t, tc.skip, skipFirstRunSetup(root, tc.args), "%q", tc.args)
	}
}
