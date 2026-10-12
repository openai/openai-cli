package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImagePickerFolderFlagRestoresParsedState(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unset", true: "explicit"}[explicit], func(t *testing.T) {
			destination := ""
			flag := &cli.StringFlag{Name: "output-dir", Value: "default", Destination: &destination}
			app := &cli.Command{Flags: []cli.Flag{flag}}
			app.Action = func(ctx context.Context, c *cli.Command) error {
				before := c.String("output-dir")
				beforeDestination := destination
				for range 2 {
					restore, err := setImagePickerFlag(c, "output-dir", "/tmp/絵 with spaces")
					require.NoError(t, err)
					require.Equal(t, "/tmp/絵 with spaces", c.String("output-dir"))
					require.True(t, c.IsSet("output-dir"))
					require.Equal(t, beforeDestination, destination)
					restore()
					require.Equal(t, before, c.String("output-dir"))
					require.Equal(t, explicit, c.IsSet("output-dir"))
					require.Equal(t, beforeDestination, destination)
				}
				return nil
			}
			args := []string{"openai"}
			if explicit {
				args = append(args, "--output-dir", "original")
			}
			require.NoError(t, app.Run(context.Background(), args))
		})
	}
}

type failedImagePickerWriter struct{}

func (failedImagePickerWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestImagePickerSelectionHistoryFollowsSuccessfulEcho(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "image-picker.json")
	model, err := newImagePicker(imagePickerOptions{Prompt: "previous"})
	require.NoError(t, err)
	require.NoError(t, saveImagePickerState(ctx, path, model.settings))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	model.settings.prompt = "new prompt\n'絵'"
	model.settings.quality = "high"
	result := imagePickerResult{Args: model.settings.args(), settings: model.settings, PrintOnly: true, shell: "bash"}
	var diagnostic bytes.Buffer
	err = finishImagePickerSelection(ctx, failedImagePickerWriter{}, &diagnostic, path, &result)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Empty(t, diagnostic.String())
	var output bytes.Buffer
	require.NoError(t, finishImagePickerSelection(ctx, &output, &diagnostic, path, &result))
	require.Equal(t, formatImagePickerCommand(result.Args, "bash")+"\n", output.String())
	got, found, err := loadImagePickerState(ctx, path)
	require.NoError(t, err)
	require.True(t, found)
	want := model.settings
	require.Equal(t, want, got)
}

func TestImagePickerOptionalHistoryFailureDoesNotBlockSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image-picker.json")
	invalid := []byte(`{"version":99,"prompt":"private text"}`)
	require.NoError(t, os.WriteFile(path, invalid, 0600))
	model, err := newImagePicker(imagePickerOptions{Prompt: "new"})
	require.NoError(t, err)
	result := imagePickerResult{Args: model.settings.args(), settings: model.settings, shell: "bash"}
	var output, diagnostics bytes.Buffer
	require.NoError(t, finishImagePickerSelection(context.Background(), &output, &diagnostics, path, &result))
	require.Equal(t, "Could not save the draft.\n", diagnostics.String())
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, invalid, after)
	err = finishImagePickerSelection(context.Background(), &output, failedImagePickerWriter{}, path, &result)
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestImagePickerCanceledSelectionHasNoPersistenceOrOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "image-picker.json")
	var output bytes.Buffer
	err := finishImagePickerSelection(ctx, &output, &output, path, &imagePickerResult{})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, output.String())
	_, err = os.Stat(path)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func TestImagePickerUnsupportedShellSelection(t *testing.T) {
	for _, shell := range []string{"", "sh", "powershell.exe", "unknown"} {
		t.Run(shell, func(t *testing.T) {
			m := pickerForTest(t)
			path := filepath.Join(t.TempDir(), "image-picker.json")
			result := imagePickerResult{Args: m.settings.args(), settings: m.settings, shell: shell}
			var output, diagnostics bytes.Buffer
			require.NoError(t, finishImagePickerSelection(context.Background(), &output, &diagnostics, path, &result))
			require.Empty(t, output.String(), "generation must not echo a command for the wrong shell")
			require.Empty(t, diagnostics.String())
			got, found, err := loadImagePickerState(t.Context(), path)
			require.NoError(t, err)
			require.True(t, found, "generation still remembers settings without a printable command")
			want := m.settings
			require.Equal(t, want, got)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			result.settings.quality = "high"
			result.Args = result.settings.args()
			result.PrintOnly = true
			require.ErrorContains(t, finishImagePickerSelection(context.Background(), &output, &diagnostics, path, &result), imagePickerUnsupportedShell)
			require.Empty(t, output.String())
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after, "unsupported command printing must not save a selection")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, finishImagePickerSelection(ctx, &output, &diagnostics, path, &result), context.Canceled)
		})
	}
}
