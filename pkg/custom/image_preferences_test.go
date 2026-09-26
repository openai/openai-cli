package custom

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imageprefs"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImageInlinePreferencePrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	path, err := imageInlinePreferencePath()
	require.NoError(t, err)
	for _, preset := range []string{"absent", "on", "off", "corrupt"} {
		if preset != "absent" {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		}
		switch preset {
		case "on", "off":
			require.NoError(t, imageprefs.Save(t.Context(), path, preset == "on"))
		case "corrupt":
			require.NoError(t, os.WriteFile(path, []byte(`{"version":999,"inline":true}`), 0600))
		}
		for _, flag := range []string{"", "auto", "on", "off"} {
			for _, terminal := range []bool{false, true} {
				name := preset + "/" + flag
				t.Run(name, func(t *testing.T) {
					app := &cli.Command{Name: "openai", Flags: []cli.Flag{&cli.StringFlag{Name: "inline", Value: "auto"}}, Action: func(ctx context.Context, command *cli.Command) error {
						got, err := imageInlineMode(ctx, command, terminal)
						if flag == "" && terminal && preset == "corrupt" {
							require.Error(t, err)
							return nil
						}
						require.NoError(t, err)
						want := flag
						if want == "" {
							want = "auto"
							if terminal && (preset == "on" || preset == "off") {
								want = preset
							}
						}
						require.Equal(t, want, got)
						return nil
					}}
					args := []string{"openai"}
					if flag != "" {
						args = append(args, "--inline", flag)
					}
					require.NoError(t, app.Run(t.Context(), args))
				})
			}
		}
	}
}

func TestImageInlinePreferenceWarningTerminal(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("requires actual terminal stdout")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	path, err := imageInlinePreferencePath()
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
				Action: imageSavingWorkflow(func(ctx context.Context, command *cli.Command) error {
					called = true
					before, err := os.ReadFile(diagnostic.Name())
					require.NoError(t, err)
					require.Empty(t, before, "preference warning must wait for successful presentation")
					presentation := ctx.Value(imagePresentationKey{}).(imagePresentation)
					require.Equal(t, "off", presentation.plan.inline)
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
				}),
			}
			registerImageSavingFlags(command)
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
			ConfigureCommand(root)
			ConfigureCommand(root)
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
				ConfigureCommand(root)
				addOtherCommands()
			} else {
				addOtherCommands()
				ConfigureCommand(root)
			}
			ConfigureCommand(root)
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
