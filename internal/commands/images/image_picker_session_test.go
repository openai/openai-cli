package images

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
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

func TestImagePickerRegistersOnlyExplicitSuccessfulPrint(t *testing.T) {
	model, err := newImagePicker(imagePickerOptions{Prompt: "reviewed 'prompt'"})
	require.NoError(t, err)
	for _, workspace := range []*imagePickerWorkspace{nil, {}} {
		for _, printOnly := range []bool{false, true} {
			for _, writeFails := range []bool{false, true} {
				var output bytes.Buffer
				var writer io.Writer = &output
				if writeFails {
					writer = failedImagePickerWriter{}
				}
				var commands []string
				result := imagePickerResult{Args: model.settings.args(), settings: model.settings, PrintOnly: printOnly, shell: "bash", workspace: workspace}
				err := finishImagePickerSelection(t.Context(), writer, io.Discard, "", &result, func(text string) error {
					if _, err := io.WriteString(writer, text+"\n"); err != nil {
						return err
					}
					commands = append(commands, text)
					return nil
				})
				if writeFails && (printOnly || workspace == nil) {
					require.ErrorIs(t, err, io.ErrClosedPipe)
				} else {
					require.NoError(t, err)
				}
				if printOnly && !writeFails {
					require.Equal(t, []string{cliterminal.FormatCommand(result.Args, "bash")}, commands)
				} else {
					require.Empty(t, commands)
				}
				if workspace != nil && !printOnly {
					require.Empty(t, output.String(), "retained generation must not print or register the command")
				}
			}
		}
	}
}

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
	result := imagePickerResult{Args: model.settings.args(), settings: model.settings, PrintOnly: true, shell: "bash", workspace: &imagePickerWorkspace{}}
	var diagnostic bytes.Buffer
	err = finishImagePickerSelection(ctx, failedImagePickerWriter{}, &diagnostic, path, &result)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Empty(t, diagnostic.String())
	var output bytes.Buffer
	require.NoError(t, finishImagePickerSelection(ctx, &output, &diagnostic, path, &result))
	require.Equal(t, cliterminal.FormatCommand(result.Args, "bash")+"\n", output.String())
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

func TestImagePickerPrintOnlyDraftWarningAfterWorkspaceClose(t *testing.T) {
	for _, warningFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "visible warning", true: "warning write failure"}[warningFails], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "image-picker.json")
			invalid := []byte(`{"version":99,"prompt":"private old draft"}`)
			require.NoError(t, os.WriteFile(path, invalid, 0600))
			model := pickerForTest(t)
			var queued []tea.Msg
			workspace := &imagePickerWorkspace{send: func(msg tea.Msg) { queued = append(queued, msg) }}
			result := imagePickerResult{Args: model.settings.args(), settings: model.settings, shell: "bash", PrintOnly: true, workspace: workspace}
			var output, diagnostics bytes.Buffer
			var warning io.Writer = &diagnostics
			if warningFails {
				warning = failedImagePickerWriter{}
			}
			err := finishImagePickerSelection(t.Context(), &output, warning, path, &result)
			if warningFails {
				require.ErrorIs(t, err, io.ErrClosedPipe)
			} else {
				require.NoError(t, err)
				require.Equal(t, "Could not save the draft.\n", diagnostics.String())
			}
			require.Equal(t, cliterminal.FormatCommand(result.Args, "bash")+"\n", output.String())
			require.False(t, result.draftSaved)
			require.Empty(t, queued, "the closed workspace cannot display the warning")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, invalid, after)
		})
	}
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

func TestImagePickerCopiedConnection(t *testing.T) {
	const key = "synthetic-print-key"
	t.Setenv("OPENAI_API_KEY", key)
	for _, tc := range []struct {
		name, project string
		hidden        bool
	}{{"overrides", "proj Staging", false}, {"key in project", "proj_" + key, true}} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cli.Command{Name: "openai", Flags: []cli.Flag{&cli.StringFlag{Name: "project"}, &cli.StringFlag{Name: "organization"}}}
			root.Action = func(ctx context.Context, c *cli.Command) error {
				m, err := newImagePicker(imagePickerOptions{Prompt: "synthetic scene", Shell: "bash", connection: pickerConnection(c)})
				require.NoError(t, err)
				m.finishSubmission(m.settings, true)
				result := m.snapshotResult(m.settings)
				var out bytes.Buffer
				path := filepath.Join(t.TempDir(), "draft.json")
				err = finishImagePickerSelection(ctx, &out, io.Discard, path, &result)
				preview := strings.Join(m.commandLines(250), "")
				if tc.hidden {
					require.ErrorContains(t, err, "Command hidden")
					require.Empty(t, out.String())
					require.NotContains(t, preview, key)
					return nil
				}
				require.NoError(t, err)
				want := cliterminal.FormatCommand(append([]string{"--project=" + tc.project, "--organization=org_test"}, m.settings.args()...), "bash")
				require.Equal(t, want+"\n", out.String())
				require.Equal(t, want, preview)
				require.Equal(t, m.settings.args(), result.Args)
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.NotContains(t, string(data), tc.project)
				return nil
			}
			require.NoError(t, root.Run(t.Context(), []string{"openai", "--project=" + tc.project, "--organization=org_test"}))
		})
	}
}

func TestImagePickerCopiedCommandKeepsProfileOutOfDraft(t *testing.T) {
	for _, profile := range []string{"", "work 'demo'"} {
		model, err := newImagePicker(imagePickerOptions{Prompt: "synthetic scene", Shell: "bash", connection: imagePickerConnection{args: []string{"--profile=" + profile}}})
		require.NoError(t, err)
		model.finishSubmission(model.settings, true)
		result := model.snapshotResult(model.settings)
		var output bytes.Buffer
		path := filepath.Join(t.TempDir(), "draft.json")
		require.NoError(t, finishImagePickerSelection(t.Context(), &output, io.Discard, path, &result))
		require.Equal(t, cliterminal.FormatCommand(append([]string{"--profile=" + profile}, model.settings.args()...), "bash")+"\n", output.String())
		require.Equal(t, model.settings.args(), result.Args)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotContains(t, string(data), "profile")
	}
}

func TestImagePickerSensitiveProfileSuppressesCopies(t *testing.T) {
	const credential = "synthetic-key-in-preview-profile"
	t.Setenv("OPENAI_API_KEY", credential)
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{&cli.StringFlag{Name: "profile"}}}
	root.Action = func(ctx context.Context, command *cli.Command) error {
		connection := pickerConnection(command)
		model, err := newImagePicker(imagePickerOptions{Prompt: "synthetic scene", Shell: "bash", connection: connection})
		require.NoError(t, err)
		preview := strings.Join(model.commandLines(120), "")
		require.NotContains(t, preview, credential)
		require.Contains(t, preview, "Command hidden")
		model.finishSubmission(model.settings, true)
		result := model.snapshotResult(model.settings)
		var output bytes.Buffer
		err = finishImagePickerSelection(ctx, &output, io.Discard, "", &result)
		require.ErrorContains(t, err, "Command hidden")
		require.Empty(t, output.String())
		return nil
	}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--profile=" + credential}))
}
