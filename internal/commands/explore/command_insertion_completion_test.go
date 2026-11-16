package explore

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestConfigureCommandInsertionCompletionIsIdempotent(t *testing.T) {
	root := &cli.Command{Commands: []*cli.Command{{Name: "@completion", Flags: []cli.Flag{&cli.BoolFlag{Name: "picker"}}}}}
	ConfigureCompletion(root)
	ConfigureCompletion(root)
	flags := root.Command("@completion").Flags
	var names []string
	for _, flag := range flags {
		names = append(names, flag.Names()...)
	}
	require.ElementsMatch(t, []string{"picker", "suggestions", "suggestions-double-tab", "suggestions-click",
		"suggestions-adapter", "install-suggestions", "auto-suggestions", "uninstall-suggestions", "suggestions-status", "suggestions-only"}, names)
	ConfigureCompletion(&cli.Command{})
}

func TestCommandInsertionRejectsPersistentPickerSetupBeforeAction(t *testing.T) {
	for _, args := range [][]string{
		{"--suggestions-adapter", "kitty", "--install-picker"},
		{"--suggestions-adapter", "kitty", "--uninstall-picker"},
		{"--suggestions-adapter", "kitty", "--automatic"},
		{"--suggestions-adapter", "kitty", "--profile", "PROFILE"},
		{"zsh", "--suggestions", "--suggestions-click", "--install-picker", "--profile", "PROFILE"},
		{"zsh", "--suggestions", "--install-picker", "--profile", "PROFILE"},
		{"zsh", "--suggestions-double-tab", "--install-picker", "--profile", "PROFILE"},
	} {
		var output bytes.Buffer
		called := false
		root := &cli.Command{Name: "openai", Writer: &output,
			ExitErrHandler: func(context.Context, *cli.Command, error) {},
			Commands: []*cli.Command{{Name: "@completion", Action: func(context.Context, *cli.Command) error {
				called = true
				return nil
			}}},
		}
		images.ConfigureShellSetup(root, func(context.Context, string) error { return nil })
		ConfigureCompletion(root)
		profile := filepath.Join(t.TempDir(), "startup")
		for i := range args {
			if args[i] == "PROFILE" {
				args[i] = profile
			}
		}
		err := root.Run(t.Context(), append([]string{"openai", "@completion"}, args...))
		require.ErrorContains(t, err, "command insertion options cannot be combined with persistent picker setup")
		require.False(t, called)
		require.Empty(t, output.String())
		require.NoFileExists(t, profile)
	}
}
