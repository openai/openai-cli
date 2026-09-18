package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImagePreviewGuidance(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"missing path", nil, `./openai images preview "path/to/image.png"`},
		{"extra paths", []string{"one.png", "two.png"}, "paths with spaces in quotes"},
		{"missing file", []string{filepath.Join(t.TempDir(), "missing.png")}, "check the filename and folder"},
		{"folder", []string{t.TempDir()}, "is a folder; choose a PNG, JPEG, or WebP file inside it"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			app := &cli.Command{
				Writer: &output, Metadata: map[string]any{"help-invocation": "./openai"},
				Flags: []cli.Flag{&cli.BoolFlag{Name: "open"}},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return handleImagesPreviewWithOpener(ctx, cmd, func(context.Context, string) error {
						t.Fatal("invalid arguments or paths must not open a viewer")
						return nil
					})
				},
			}
			err := app.Run(t.Context(), append([]string{"preview", "--open"}, test.args...))
			require.ErrorContains(t, err, test.want)
			require.Empty(t, output.String(), "no success message may precede invalid-path guidance")
			if test.name == "missing file" {
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestImagePreviewHelpUsesActualInvocation(t *testing.T) {
	var output bytes.Buffer
	app := &cli.Command{
		Name: "openai", Writer: &output,
		Metadata: map[string]any{"help-invocation": "'/Applications/CLI tools/openai'"},
		Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{{
			Name: "preview", CustomHelpTemplate: imagePreviewHelp,
			Action: func(context.Context, *cli.Command) error {
				t.Fatal("help must not attempt a preview")
				return nil
			},
		}}}},
	}
	require.NoError(t, app.Run(t.Context(), []string{"openai", "images", "preview", "--help"}))
	help := output.String()
	require.Contains(t, help, `'/Applications/CLI tools/openai' images preview "path/to/image.png"`)
	require.Contains(t, help, `'/Applications/CLI tools/openai' images preview --open "path/to/image.png"`)
	require.Contains(t, help, "makes no API request and uses no credits")
	require.Contains(t, help, "help --all images preview")
}

func TestImagePreviewFormatGuidancePreservesErrors(t *testing.T) {
	path := "synthetic\n\x1b[2J.png"
	formatErr := fmt.Errorf("inspect saved image for preview: %w", image.ErrFormat)
	err := explainImagePreviewFormat(formatErr, path)
	require.ErrorIs(t, err, image.ErrFormat)
	require.Contains(t, err.Error(), "PNG, JPEG, or WebP")
	require.Contains(t, err.Error(), "choose an image saved in one of those formats")
	require.False(t, strings.ContainsAny(err.Error(), "\n\x1b"), "filenames must not inject terminal controls")

	for _, failure := range []error{nil, context.Canceled, errors.New("synthetic output failure")} {
		require.Equal(t, failure, explainImagePreviewFormat(failure, path), "unrelated failures must retain their identity and behavior")
	}
}
