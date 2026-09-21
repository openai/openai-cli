package custom

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/imageprefs"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func isolateImagePreferences(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// os.UserConfigDir uses HOME on macOS, XDG_CONFIG_HOME on Linux, and
	// AppData on Windows. All writes remain inside this test's temporary tree.
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "AppData"} {
		t.Setenv(key, dir)
	}
	path, err := imageInlinePreferencePath()
	require.NoError(t, err)
	return path
}

func TestImageInlinePreferenceCommands(t *testing.T) {
	path := isolateImagePreferences(t)
	for _, mode := range []string{"off", "on", "off"} {
		var output bytes.Buffer
		app := &cli.Command{Writer: &output, Commands: imageInlinePreferenceCommands()}
		require.NoError(t, app.Run(t.Context(), []string{"inline", mode}))
		on, err := imageprefs.Load(path)
		require.NoError(t, err)
		require.Equal(t, mode == "on", on)
		require.Contains(t, output.String(), "Inline previews "+mode+" for future image generations")
	}
	for _, args := range [][]string{{"off", "extra"}, {"--format", "json", "off"}, {"--raw-output", "off"}, {"--transform", "foo", "off"}} {
		require.NoError(t, imageprefs.Save(path, true))
		app := &cli.Command{Writer: io.Discard, Commands: imageInlinePreferenceCommands(), Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"}, &cli.BoolFlag{Name: "raw-output"}, &cli.StringFlag{Name: "transform"},
		}}
		require.Error(t, app.Run(t.Context(), append([]string{"inline"}, args...)))
		on, err := imageprefs.Load(path)
		require.NoError(t, err)
		require.True(t, on, "invalid preference command must not change settings")
	}
}

func TestImageInlinePreferencePolicy(t *testing.T) {
	for _, test := range []struct {
		name          string
		args          []string
		body          string
		preference    bool
		broken        bool
		piped, ci     bool
		preview, fail bool
	}{
		{name: "saved off"},
		{name: "saved on", preference: true, preview: true},
		{name: "explicit on overrides off", args: []string{"--inline", "on"}, preview: true},
		{name: "explicit off overrides on", preference: true, args: []string{"--inline", "off"}},
		{name: "old no-preview overrides saved on", preference: true, args: []string{"--no-preview"}},
		{name: "open overrides saved on", preference: true, args: []string{"--open"}},
		{name: "open plus explicit on", args: []string{"--open", "--inline", "on"}, preview: true},
		{name: "invalid preference reports repair", broken: true, fail: true},
		{name: "explicit on ignores broken preference", broken: true, args: []string{"--inline", "on"}, preview: true},
		{name: "explicit off ignores broken preference", broken: true, args: []string{"--inline", "off"}},
		{name: "open ignores broken preference", broken: true, args: []string{"--open"}},
		{name: "no-preview ignores broken preference", broken: true, args: []string{"--no-preview"}},
		{name: "JSON ignores broken preference", broken: true, args: []string{"--format", "json"}},
		{name: "transform ignores broken preference", broken: true, args: []string{"--transform", "data"}},
		{name: "raw output ignores broken preference", broken: true, args: []string{"--raw-output"}},
		{name: "stream ignores broken preference", broken: true, body: `{"stream":true}`},
		{name: "URL ignores broken preference", broken: true, body: `{"response_format":"url"}`},
		{name: "pipe ignores broken preference", broken: true, piped: true},
		{name: "CI ignores broken preference", broken: true, ci: true},
		{name: "invalid flag still validated in pipe", broken: true, piped: true, args: []string{"--inline", "invalid"}, fail: true},
		{name: "old conflicting flags remain invalid", args: []string{"--inline", "on", "--no-preview"}, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := isolateImagePreferences(t)
			require.NoError(t, imageprefs.Save(path, test.preference))
			if test.broken {
				require.NoError(t, os.WriteFile(path, []byte("invalid"), 0600))
			}
			for _, key := range []string{"TMUX", "STY", "ZELLIJ", "CI"} {
				t.Setenv(key, "")
			}
			if test.ci {
				t.Setenv("CI", "true")
			}
			t.Setenv("TERM_PROGRAM", "ghostty")
			t.Setenv("TERM", "xterm-ghostty")
			app := &cli.Command{Writer: io.Discard, Flags: []cli.Flag{
				&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "output-dir"},
				&cli.StringFlag{Name: "inline", Value: "on"}, &cli.BoolFlag{Name: "no-preview"},
				&cli.BoolFlag{Name: "open"}, &cli.StringFlag{Name: "name"},
				&cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"},
			}, Action: func(ctx context.Context, cmd *cli.Command) error {
				body := test.body
				if body == "" {
					body = "{}"
				}
				plan, err := prepareImageOutput(cmd, !test.piped, gjson.Parse(body))
				if err != nil {
					return err
				}
				preview := plan != nil && (plan.preview != "" || plan.textPreview)
				require.Equal(t, test.preview, preview)
				return nil
			}}
			err := app.Run(t.Context(), append([]string{"images"}, test.args...))
			if test.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			files, err := os.ReadDir(filepath.Dir(path))
			require.NoError(t, err)
			require.Len(t, files, 1, "image generation must only read preferences")
		})
	}
}
