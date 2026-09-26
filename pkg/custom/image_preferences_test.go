package custom

import (
	"bytes"
	"context"
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
