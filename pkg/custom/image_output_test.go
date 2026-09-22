package custom

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestImageOutputTerminalPolicy(t *testing.T) {
	for _, key := range []string{"CI", "TMUX", "STY", "ZELLIJ"} {
		t.Setenv(key, "")
	}
	t.Setenv("TERM", "xterm-ghostty")
	t.Setenv("TERM_PROGRAM", "ghostty")
	for _, test := range []struct {
		name      string
		args      []string
		piped     bool
		save      bool
		noPreview bool
		open      bool
		partials  int64
		wantError string
	}{
		{name: "default terminal saves", save: true},
		{name: "default nonterminal saves without preview", piped: true, save: true, noPreview: true},
		{name: "inline on", args: []string{"--inline", "on"}, save: true},
		{name: "inline off still saves", args: []string{"--inline", "off"}, save: true, noPreview: true},
		{name: "open selects original viewer", args: []string{"--open"}, save: true, noPreview: true, open: true},
		{name: "open and explicit inline", args: []string{"--open", "--inline", "on"}, save: true, open: true},
		{name: "preview opt out still saves", args: []string{"--no-preview"}, save: true, noPreview: true},
		{name: "explicit auto saves", args: []string{"--format", "auto"}, save: true},
		{name: "explicit text saves", args: []string{"--format", "text"}, save: true},
		{name: "JSON stays JSON", args: []string{"--format", "json"}},
		{name: "explorer stays explorer", args: []string{"--format", "explore"}},
		{name: "transform stays transform", args: []string{"--transform", "data.0"}},
		{name: "raw output stays raw", args: []string{"--raw-output"}},
		{name: "stream saves final image", args: []string{"--stream", "true"}, save: true},
		{name: "nonterminal stream saves without preview", args: []string{"--stream", "true"}, piped: true, save: true, noPreview: true},
		{name: "stream with name saves", args: []string{"--stream", "true", "--name", "robot"}, save: true},
		{name: "stream with open saves", args: []string{"--stream", "true", "--open"}, save: true, noPreview: true, open: true},
		{name: "partials automatically select saved stream", args: []string{"--partial-images", "2"}, save: true, partials: 2},
		{name: "partials with stream save", args: []string{"--partial-images", "3", "--stream", "true"}, save: true, partials: 3},
		{name: "partials with JSON remain API output", args: []string{"--partial-images", "2", "--stream", "true", "--format", "json"}},
		{name: "partials respect inline off", args: []string{"--partial-images", "1", "--inline", "off"}, save: true, partials: 1, noPreview: true},
		{name: "partial stream false rejected", args: []string{"--partial-images", "1", "--stream", "false"}, wantError: "--partial-images needs streaming"},
		{name: "save stream event limit rejected", args: []string{"--stream", "true", "--name", "robot", "--max-items", "1"}, wantError: "--max-items limits API events"},
		{name: "partial event limit rejected", args: []string{"--partial-images", "1", "--max-items", "1"}, wantError: "--max-items limits API events"},
		{name: "API stream event limit preserved", args: []string{"--stream", "true", "--max-items", "1", "--format", "json"}},
		{name: "URL stays URL", args: []string{"--response-format", "url"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			destination := filepath.Join(home, "Downloads", "gpt-images")
			app := &cli.Command{
				Name:   "images",
				Writer: io.Discard,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "format", Value: "auto"},
					&cli.StringFlag{Name: "output-dir"},
					&cli.BoolFlag{Name: "no-preview"},
					&cli.BoolFlag{Name: "open"},
					&cli.StringFlag{Name: "inline", Value: "on"},
					&cli.StringFlag{Name: "name"},
					&cli.StringFlag{Name: "transform"},
					&cli.BoolFlag{Name: "raw-output"},
					&requestflag.Flag[*bool]{Name: "stream", BodyPath: "stream", Default: requestflag.Ptr(false)},
					&requestflag.Flag[*int64]{Name: "partial-images", BodyPath: "partial_images", Default: requestflag.Ptr[int64](0)},
					&requestflag.Flag[int64]{Name: "max-items"},
					&requestflag.Flag[*string]{Name: "response-format", BodyPath: "response_format"},
					&requestflag.Flag[*string]{Name: "model", BodyPath: "model"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					body, err := json.Marshal(requestflag.ExtractRequestContents(cmd).Body)
					require.NoError(t, err)
					plan, err := prepareImageOutput(cmd, !test.piped, gjson.ParseBytes(body))
					if test.wantError != "" {
						require.ErrorContains(t, err, test.wantError)
						require.Nil(t, plan)
						return nil
					}
					require.NoError(t, err)
					if test.save {
						require.NotNil(t, plan)
						require.Equal(t, destination, plan.directory)
						require.Equal(t, test.open, plan.openFiles)
						require.Equal(t, test.partials, plan.partialImages)
						if test.noPreview {
							require.Empty(t, plan.preview)
						} else {
							require.Equal(t, terminalimage.Kitty, plan.preview)
						}
					} else {
						require.Nil(t, plan)
					}
					return nil
				},
			}
			require.NoError(t, app.Run(t.Context(), append([]string{"images"}, test.args...)))
			info, err := os.Stat(destination)
			if test.save {
				require.NoError(t, err)
				require.True(t, info.IsDir())
			} else {
				require.True(t, os.IsNotExist(err), "explicit API output must not create download folders")
			}
		})
	}
}

func TestImageOutputOpenPreservesSavedImage(t *testing.T) {
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	response, err := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngData.Bytes())}}})
	require.NoError(t, err)
	for _, test := range []struct {
		name    string
		open    bool
		failure error
	}{
		{"explicit viewer", true, nil},
		{"viewer fails after generation", true, errors.New("synthetic viewer failure")},
		{"ordinary save never opens", false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := &imageOutputPlan{directory: t.TempDir(), openFiles: test.open}
			var output bytes.Buffer
			calls := 0
			err := plan.saveWithOpener(t.Context(), response, &output, func(ctx context.Context, path string) error {
				calls++
				saved, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, pngData.Bytes(), saved)
				return test.failure
			})
			require.NoError(t, err, "a viewer failure must not invite another paid generation")
			files, err := os.ReadDir(plan.directory)
			require.NoError(t, err)
			require.Len(t, files, 1)
			require.Equal(t, map[bool]int{true: 1, false: 0}[test.open], calls)
			require.NotContains(t, output.String(), "\x1b")
			if test.failure != nil {
				require.Contains(t, output.String(), "The image is saved")
				require.Contains(t, output.String(), "preview --open FILE")
			} else if test.open {
				require.Contains(t, output.String(), "Opening original image")
			}
		})
	}
}

func TestImagePreviewOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original image.png")
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	require.NoError(t, os.WriteFile(path, encoded.Bytes(), 0600))
	for _, test := range []struct {
		name     string
		flags    []string
		failure  error
		wantCall bool
	}{
		{"opens without TTY", nil, nil, true},
		{"reports viewer failure", nil, errors.New("synthetic viewer failure"), true},
		{"rejects JSON", []string{"--format", "json"}, nil, false},
		{"rejects raw", []string{"--raw-output"}, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			called := false
			app := &cli.Command{Writer: &output, Flags: []cli.Flag{
				&cli.BoolFlag{Name: "open"}, &cli.StringFlag{Name: "format", Value: "auto"},
				&cli.BoolFlag{Name: "raw-output"}, &cli.StringFlag{Name: "transform"},
			}, Action: func(ctx context.Context, cmd *cli.Command) error {
				return handleImagesPreviewWithOpener(ctx, cmd, func(ctx context.Context, got string) error {
					called = true
					require.Equal(t, path, got)
					return test.failure
				})
			}}
			args := append([]string{"preview", "--open"}, test.flags...)
			err := app.Run(t.Context(), append(args, path))
			require.Equal(t, test.wantCall, called)
			if test.failure != nil {
				require.ErrorIs(t, err, test.failure)
			} else if test.wantCall {
				require.NoError(t, err)
				require.Contains(t, output.String(), "Opening original image")
			} else {
				require.Error(t, err)
			}
			require.NotContains(t, output.String(), "Inline preview")
			saved, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			require.Equal(t, encoded.Bytes(), saved)
		})
	}
}

func TestImagePreviewMissingPathEscapesControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing\n\x1b[2J.png")
	app := &cli.Command{Writer: io.Discard, Flags: []cli.Flag{
		&cli.BoolFlag{Name: "open"}, &cli.StringFlag{Name: "format", Value: "auto"},
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		return handleImagesPreviewWithOpener(ctx, cmd, func(context.Context, string) error {
			t.Fatal("missing file must not reach a viewer")
			return nil
		})
	}}
	err := app.Run(t.Context(), []string{"preview", "--open", path})
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NotContains(t, err.Error(), "\x1b")
	require.NotContains(t, err.Error(), "\n")
	require.Contains(t, err.Error(), `\x1b`)
}

func TestImagePreviewTextFallback(t *testing.T) {
	for _, test := range []struct {
		name                            string
		env                             map[string]string
		off, piped, wantText, wantColor bool
	}{
		{name: "Apple Terminal", env: map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"}, wantText: true, wantColor: true},
		{name: "unknown basic terminal", wantText: true},
		{name: "dumb terminal", env: map[string]string{"TERM": "dumb", "COLORTERM": "truecolor"}, wantText: true},
		{name: "NO_COLOR", env: map[string]string{"TERM": "xterm-256color", "NO_COLOR": "1"}, wantText: true},
		{name: "CLICOLOR off", env: map[string]string{"TERM": "xterm-256color", "CLICOLOR": "0"}, wantText: true},
		{name: "tmux text", env: map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "screen-256color", "TMUX": "synthetic"}, wantText: true, wantColor: true},
		{name: "off disables all previews", env: map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, off: true},
		{name: "CI", env: map[string]string{"TERM_PROGRAM": "Apple_Terminal", "CI": "true"}},
		{name: "piped output", env: map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, piped: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"TERM_PROGRAM", "TERM", "CI", "TMUX", "STY", "ZELLIJ", "NO_COLOR", "COLORTERM", "CLICOLOR"} {
				t.Setenv(key, test.env[key])
			}
			app := &cli.Command{Writer: io.Discard, Flags: []cli.Flag{
				&cli.StringFlag{Name: "inline", Value: "on"}, &cli.StringFlag{Name: "output-dir"},
			}, Action: func(ctx context.Context, cmd *cli.Command) error {
				plan, err := prepareImageOutput(cmd, !test.piped, gjson.Parse("{}"))
				require.NoError(t, err)
				require.NotNil(t, plan)
				require.Empty(t, plan.preview)
				require.Equal(t, test.wantText, plan.textPreview)
				if plan.textPreview {
					require.Equal(t, test.wantColor, plan.textColor)
				}
				return nil
			}}
			args := []string{"images", "--output-dir", t.TempDir()}
			if test.off {
				args = append(args, "--inline", "off")
			}
			require.NoError(t, app.Run(t.Context(), args))
		})
	}
}

func TestImageOutputPreview(t *testing.T) {
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 16, 8))))
	for _, test := range []struct {
		name     string
		protocol terminalimage.Protocol
		data     []byte
		marker   string
	}{
		{"iTerm2", terminalimage.ITerm2, pngData.Bytes(), "\x1b]1337;File="},
		{"Kitty", terminalimage.Kitty, pngData.Bytes(), "\x1b_Ga=T"},
		{"unsupported terminal", "", pngData.Bytes(), ""},
		{"broken preview keeps saved file", terminalimage.Kitty, []byte("\x89PNG\r\n\x1a\ninvalid"), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(test.data)}}})
			require.NoError(t, err)
			plan := &imageOutputPlan{directory: t.TempDir(), preview: test.protocol}
			var output bytes.Buffer
			require.NoError(t, plan.save(t.Context(), response, &output))
			files, err := os.ReadDir(plan.directory)
			require.NoError(t, err)
			require.Len(t, files, 1)
			path := filepath.Join(plan.directory, files[0].Name())
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, test.data, saved)
			require.Contains(t, output.String(), path)
			if test.marker == "" {
				require.NotContains(t, output.String(), "\x1b")
			} else {
				require.Contains(t, output.String(), test.marker)
			}
			if test.name == "broken preview keeps saved file" {
				require.Contains(t, output.String(), "Preview unavailable")
			}
		})
	}
}
