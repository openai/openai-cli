package images_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
	appconfig "github.com/openai/openai-cli/internal/app"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImageInlinePreferenceFailureKeepsPrivatePathsAndOriginal(t *testing.T) {
	home := filepath.Join(t.TempDir(), "synthetic-private-home")
	for _, name := range []string{"HOME", "APPDATA", "USERPROFILE", "XDG_CONFIG_HOME"} {
		t.Setenv(name, home)
	}
	path, err := images.TestImageInlinePreferencePath()
	require.NoError(t, err)
	blocked := filepath.Dir(path)
	require.NoError(t, os.MkdirAll(filepath.Dir(blocked), 0700))
	original := []byte("synthetic-private-original")
	require.NoError(t, os.WriteFile(blocked, original, 0600))
	for _, mode := range []string{"on", "off"} {
		root := &cli.Command{Name: "openai", Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{},
			Flags:    []cli.Flag{&cli.StringFlag{Name: "format", Value: "auto"}},
			Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{{Name: "inline", Commands: images.TestImageInlinePreferenceCommands(appconfig.ImagePreviewOptions().Receipt)}}}},
		}
		failure := root.Run(t.Context(), []string{"openai", "images", "inline", mode})
		var cause *os.PathError
		require.ErrorAs(t, failure, &cause)
		require.ErrorIs(t, failure, cause)
		for _, format := range []string{"text", "json"} {
			var diagnostic bytes.Buffer
			require.NoError(t, appconfig.ShowCommandError(readableErrorTestCommand(t, "--format-error", format), failure, &diagnostic))
			require.Contains(t, diagnostic.String(), "Existing settings were kept.")
			require.Contains(t, diagnostic.String(), "image-preferences.json")
			location := images.TestImageInlinePreferenceLocation(runtime.GOOS)
			if format == "json" {
				location = strings.ReplaceAll(location, `\`, `\\`)
			}
			require.Contains(t, diagnostic.String(), location)
			require.NotContains(t, diagnostic.String(), "synthetic-private")
			require.NotContains(t, diagnostic.String(), home)
		}
		data, err := os.ReadFile(blocked)
		require.NoError(t, err)
		require.Equal(t, original, data)
	}
}

func TestImageInlinePreferenceWarningTerminal(t *testing.T) {
	if !images.TestIsTerminal(os.Stdout) {
		t.Skip("requires actual terminal stdout")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	path, err := images.TestImageInlinePreferencePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("invalid-private-settings"), 0600))
	failure := errors.New("synthetic presentation failure")
	for _, scenario := range []string{"success", "action failure", "action cancellation", "canceled after success", "warning output failure"} {
		t.Run(scenario, func(t *testing.T) {
			diagnostic, err := os.CreateTemp(t.TempDir(), "diagnostic")
			require.NoError(t, err)
			defer func() { _ = diagnostic.Close() }()
			original := os.Stderr
			os.Stderr = diagnostic
			defer func() { os.Stderr = original }()
			if scenario == "warning output failure" {
				require.NoError(t, diagnostic.Close())
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			called := false
			command := &cli.Command{Name: "generate", Reader: strings.NewReader(""), Writer: os.Stdout,
				Action: images.TestImageSavingWorkflow(func(ctx context.Context, command *cli.Command) error {
					called = true
					before, err := os.ReadFile(diagnostic.Name())
					require.NoError(t, err)
					require.Empty(t, before, "preference warning must wait for successful presentation")
					require.Equal(t, "off", images.InlineForTest(ctx))
					switch scenario {
					case "action failure":
						return failure
					case "action cancellation":
						cancel()
						return ctx.Err()
					case "canceled after success":
						cancel()
					}
					return nil
				}, appconfig.ImageSavingOptions()),
			}
			images.TestRegisterImageSavingFlags(command)
			err = command.Run(ctx, []string{"generate", "--output-dir", t.TempDir()})
			require.True(t, called, "warning output must not prevent the action")
			switch scenario {
			case "success":
				require.NoError(t, err)
			case "action failure":
				require.ErrorIs(t, err, failure)
			case "action cancellation", "canceled after success":
				require.ErrorIs(t, err, context.Canceled)
			case "warning output failure":
				require.ErrorIs(t, err, os.ErrClosed)
			}
			after, err := os.ReadFile(diagnostic.Name())
			require.NoError(t, err)
			if scenario == "success" {
				require.Contains(t, string(after), "Could not read the inline preference")
				require.NotContains(t, string(after), "invalid-private-settings")
			} else {
				require.Empty(t, after)
			}
		})
	}
}

func TestImagePreviewCommandsRegisterOnceAndRejectFormats(t *testing.T) {
	for _, argv := range [][]string{{"images", "preview"}, {"images", "preview", "one", "two"}, {"--format", "json", "images", "preview", "one"}, {"--transform", "id", "images", "inline", "on"}, {"--raw-output", "images", "inline", "off"}, {"images", "inline", "on", "extra"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			root := &cli.Command{Name: "openai", Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}, Flags: []cli.Flag{&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"}}, Commands: []*cli.Command{{Name: "images"}}}
			appconfig.ConfigureCommand(root)
			appconfig.ConfigureCommand(root)
			require.NotNil(t, root.Command("images").Command("preview"))
			require.NotNil(t, root.Command("images").Command("inline").Command("off"))
			require.Error(t, root.Run(t.Context(), append([]string{"openai"}, argv...)))
		})
	}
}

func TestImagePreviewRegistrationPreservesInlineCommands(t *testing.T) {
	for _, order := range []string{"saved first", "other commands first"} {
		t.Run(order, func(t *testing.T) {
			images := &cli.Command{Name: "images", Commands: []*cli.Command{{Name: "generate"}}}
			root := &cli.Command{Name: "openai", Commands: []*cli.Command{images}}
			var shared *cli.Command
			addOtherCommands := func() {
				shared = images.Command("inline")
				if shared == nil {
					shared = &cli.Command{Name: "inline", Usage: "Manage image previews"}
					images.Commands = append(images.Commands, shared)
				}
				shared.Commands = append(shared.Commands, &cli.Command{Name: "existing"})
			}
			if order == "saved first" {
				appconfig.ConfigureCommand(root)
				addOtherCommands()
			} else {
				addOtherCommands()
				appconfig.ConfigureCommand(root)
			}
			appconfig.ConfigureCommand(root)
			require.Same(t, shared, images.Command("inline"))
			counts := map[string]int{}
			for _, command := range images.Commands {
				counts[command.Name]++
			}
			require.Equal(t, 1, counts["inline"])
			require.Equal(t, 1, counts["preview"])
			require.Equal(t, 1, counts["generate"])
			children := map[string]int{}
			for _, command := range shared.Commands {
				children[command.Name]++
			}
			for _, name := range []string{"on", "off", "existing"} {
				require.Equal(t, 1, children[name], name)
			}
		})
	}
}
