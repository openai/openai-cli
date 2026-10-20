package images

import (
	"context"
	"os"
	"path/filepath"
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

func TestImageInlinePreferenceLocationUsesSymbolicPaths(t *testing.T) {
	for _, name := range []string{"HOME", "APPDATA", "USERPROFILE", "XDG_CONFIG_HOME"} {
		t.Setenv(name, "synthetic-private-config")
	}
	for _, tc := range []struct{ goos, want string }{
		{"darwin", "$HOME/Library/Application Support/openai/image-preferences.json"},
		{"windows", `%AppData%\openai\image-preferences.json`},
		{"linux", "$XDG_CONFIG_HOME/openai/image-preferences.json, or $HOME/.config/openai/image-preferences.json when XDG_CONFIG_HOME is unset"},
		{"freebsd", "$XDG_CONFIG_HOME/openai/image-preferences.json, or $HOME/.config/openai/image-preferences.json when XDG_CONFIG_HOME is unset"},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			require.Equal(t, tc.want, imageInlinePreferenceLocation(tc.goos))
			require.NotContains(t, imageInlinePreferenceLocation(tc.goos), "synthetic-private")
		})
	}
}
