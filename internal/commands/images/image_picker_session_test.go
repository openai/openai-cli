package images

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	require.Equal(t, cliterminal.FormatCommand(result.Args, "bash")+"\n", output.String())
	got, found, err := loadImagePickerState(ctx, path)
	require.NoError(t, err)
	require.True(t, found)
	want := model.settings
	require.Equal(t, want, got)
}

func TestImagePickerProfileDisplayRemainsEphemeral(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, test := range []struct {
			name, profile string
			explicit      bool
		}{
			{name: "absent"},
			{name: "empty", explicit: true},
			{name: "special", profile: "studio '雪' $HOME $(touch NOT_RUN)", explicit: true},
			{name: "format controls", profile: "studio\u202e\u200b", explicit: true},
		} {
			t.Run(shell+"/"+test.name, func(t *testing.T) {
				options := imagePickerOptions{Prompt: "synthetic scene", Shell: shell}
				if test.explicit {
					options.profile = &test.profile
				}
				model, err := newImagePicker(options)
				require.NoError(t, err)
				requestArgs := model.settings.args()
				wantArgs := requestArgs
				if test.explicit {
					wantArgs = append([]string{"--profile", test.profile}, requestArgs...)
				}
				wantCommand := cliterminal.FormatCommand(wantArgs, shell)
				require.Equal(t, wantCommand, strings.Join(model.commandLines(40), ""))
				require.NotContains(t, wantCommand, "\u202e")
				require.NotContains(t, wantCommand, "\u200b")
				path := filepath.Join(t.TempDir(), "image-picker.json")
				require.NoError(t, saveImagePickerState(t.Context(), path, model.settings))
				before, err := os.ReadFile(path)
				require.NoError(t, err)
				for _, printOnly := range []bool{false, true} {
					model.finishSubmission(model.settings, printOnly)
					result := model.snapshotResult(model.settings)
					require.Equal(t, options.profile, result.profile)
					require.Equal(t, requestArgs, result.Args, "the selector must not enter generated request arguments")
					var output, diagnostics bytes.Buffer
					require.NoError(t, finishImagePickerSelection(t.Context(), &output, &diagnostics, path, &result))
					require.Equal(t, wantCommand+"\n", output.String())
					require.Empty(t, diagnostics.String())
					after, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, before, after, "the selector must not change saved draft bytes")
				}
				saved, found, err := loadImagePickerState(t.Context(), path)
				require.NoError(t, err)
				require.True(t, found)
				restored, err := newImagePicker(restoreImagePickerDraft(imagePickerOptions{Shell: shell}, saved))
				require.NoError(t, err)
				require.Nil(t, restored.profile)
				require.Equal(t, cliterminal.FormatCommand(requestArgs, shell), strings.Join(restored.commandLines(40), ""))
				restored, err = newImagePicker(restoreImagePickerDraft(options, saved))
				require.NoError(t, err)
				require.Equal(t, wantCommand, strings.Join(restored.commandLines(40), ""))
			})
		}
	}
}

func TestImagePickerProfileCommandNativeShellRoundTrip(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				if strings.Contains(","+os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS")+",", ","+shell+",") {
					t.Fatalf("required shell %s unavailable", shell)
				}
				t.Skipf("%s is not installed", shell)
			}
			prefix := "openai() { printf '%s\\0' \"$@\"; }\n"
			var shellArgs []string
			switch shell {
			case "bash":
				shellArgs = []string{"--noprofile", "--norc", "-c"}
			case "zsh":
				shellArgs = []string{"-f", "-c"}
			case "fish":
				shellArgs = []string{"--no-config", "-c"}
				prefix = "function openai; printf '%s\\0' $argv; end\n"
			case "pwsh":
				shellArgs = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}
				prefix = "function openai { foreach ($v in $args) { [Console]::Out.Write($v); [Console]::Out.Write([char]0) } }\n"
			}
			profiles := []string{"", "-studio", "studio '雪' $HOME $(touch NOT_RUN); & | > [x] {x}\u202e"}
			var commands, expected bytes.Buffer
			var requestArgs []string
			for _, profile := range profiles {
				model, err := newImagePicker(imagePickerOptions{Prompt: "synthetic scene", Shell: shell, profile: &profile})
				require.NoError(t, err)
				model.finishSubmission(model.settings, true)
				result := model.snapshotResult(model.settings)
				require.NoError(t, finishImagePickerSelection(t.Context(), &commands, io.Discard, "", &result))
				requestArgs = model.settings.args()
				expected.WriteString(strings.Join(append([]string{"--profile", profile}, requestArgs...), "\x00") + "\x00")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, binary, append(shellArgs, prefix+commands.String())...)
			dir := t.TempDir()
			process.Dir = dir
			process.Env = append(os.Environ(), "HOME="+dir, "XDG_CONFIG_HOME="+dir, "OPENAI_API_KEY=synthetic-not-used", "OPENAI_BASE_URL=http://127.0.0.1:9")
			process.WaitDelay = time.Second
			var stdout, stderr bytes.Buffer
			process.Stdout, process.Stderr = &stdout, &stderr
			require.NoError(t, process.Run(), stderr.String())
			require.Empty(t, stderr.String())
			require.Equal(t, expected.String(), stdout.String())
			parsed := strings.Split(strings.TrimSuffix(stdout.String(), "\x00"), "\x00")
			for i, profile := range profiles {
				calls := 0
				generate := &cli.Command{Name: "generate", SkipFlagParsing: true}
				root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{generate}}}}
				root.Flags = append(root.Flags, &cli.StringFlag{Name: "profile"})
				generate.Action = func(context.Context, *cli.Command) error {
					calls++
					require.True(t, root.IsSet("profile"))
					require.Equal(t, profile, root.String("profile"))
					require.Equal(t, requestArgs[2:], generate.Args().Slice())
					return nil
				}
				count := len(requestArgs) + 2
				require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, parsed[i*count:(i+1)*count]...)))
				require.Equal(t, 1, calls)
			}
			_, err = os.Stat(filepath.Join(dir, "NOT_RUN"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
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
