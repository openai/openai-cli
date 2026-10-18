package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func audioPreviewModel(t *testing.T, invocation string, flags ...string) *audioWorkbench {
	t.Helper()
	root := &cli.Command{Name: "synthetic-preview", Writer: io.Discard, ErrWriter: io.Discard,
		Metadata:       map[string]any{"help-invocation": invocation},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags:          []cli.Flag{NewRequestHeaderFlag()},
	}
	for _, name := range []string{"project", "organization", "base-url", "api-key", "admin-api-key", "webhook-secret",
		requestconfig.MTLSClientCertFileFlag, requestconfig.MTLSClientKeyFileFlag} {
		root.Flags = append(root.Flags, &cli.StringFlag{Name: name})
	}
	var model *audioWorkbench
	root.Action = func(ctx context.Context, command *cli.Command) error {
		model = newAudioWorkbench(ctx, command, nil, audiohistory.Store{})
		return nil
	}
	require.NoError(t, root.Run(t.Context(), append([]string{"synthetic-preview"}, flags...)))
	require.NotNil(t, model)
	model.color = false
	model.settings.Model = "synthetic-speech-model"
	model.settings.Voice = "coral"
	model.settings.Format = "wav"
	model.settings.Speed = 1.25
	return model
}

func audioPreviewExecutable(t *testing.T, name string) (string, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "CLI preview's directory")
	require.NoError(t, os.Mkdir(directory, 0700))
	path := filepath.Join(directory, name)
	// This executable records shell arguments and stdin. It cannot call the API.
	const script = `#!/bin/sh
set -eu
printf '%s\000' "$@" > "$AUDIO_PREVIEW_ARGS"
printf '%s' "${OPENAI_UNTRUSTED_STDIN-unset}" > "$AUDIO_PREVIEW_PROTECTED"
/bin/cat > "$AUDIO_PREVIEW_STDIN"
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0700))
	return path, directory
}

type audioPreviewCapture struct {
	args      []string
	stdin     []byte
	protected string
}

func executeAudioPreview(t *testing.T, shell, command, pathDirectory string) audioPreviewCapture {
	t.Helper()
	path := "/bin/" + shell
	if _, err := os.Stat(path); err != nil {
		if strings.Contains(","+os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS")+",", ","+shell+",") {
			t.Fatalf("required native shell %s is unavailable", shell)
		}
		t.Skipf("native %s is unavailable", shell)
	}
	directory := t.TempDir()
	argsPath, stdinPath, protectedPath := filepath.Join(directory, "argv"), filepath.Join(directory, "stdin"), filepath.Join(directory, "protected")
	args := []string{"--noprofile", "--norc", "-c", command}
	if shell == "zsh" {
		args = []string{"-f", "-c", command}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, path, args...)
	process.WaitDelay = 250 * time.Millisecond
	process.Dir = t.TempDir()
	process.Env = []string{"PATH=" + pathDirectory + ":/usr/bin:/bin", "LANG=en_US.UTF-8",
		"AUDIO_PREVIEW_ARGS=" + argsPath, "AUDIO_PREVIEW_STDIN=" + stdinPath,
		"AUDIO_PREVIEW_PROTECTED=" + protectedPath}
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	require.NoError(t, process.Run(), "shell=%s stderr=%q", shell, stderr.String())
	require.NoError(t, ctx.Err())
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())
	rawArgs, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	require.True(t, bytes.HasSuffix(rawArgs, []byte{0}))
	input, err := os.ReadFile(stdinPath)
	require.NoError(t, err)
	protected, err := os.ReadFile(protectedPath)
	require.NoError(t, err)
	return audioPreviewCapture{strings.Split(string(rawArgs[:len(rawArgs)-1]), "\x00"), input, string(protected)}
}

func assertAudioPreviewJSON(t *testing.T, model *audioWorkbench, input []byte) {
	t.Helper()
	want := map[string]any{"model": model.settings.Model, "voice": model.settings.Voice,
		"response_format": model.settings.Format, "speed": model.settings.Speed, "input": model.input.Value()}
	if model.instructions.Value() != "" {
		want["instructions"] = model.instructions.Value()
	}
	expected, err := json.Marshal(want)
	require.NoError(t, err)
	require.Equal(t, expected, input, "shell changed the complete JSON bytes")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(input, &decoded))
	require.Equal(t, want, decoded)
}

func TestAudioCommandPreviewNativeShellPreservesInputsAndRequestContext(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, directory := audioPreviewExecutable(t, "openai preview's binary")
			invocation := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
			for _, input := range []string{"@synthetic/file.txt", `\@synthetic/file.txt`, "世界 says 'hello'\nsecond line with \\\"quoted\\\" text"} {
				for _, instructions := range []string{"", "Speak 'gently'\nPreserve 世界 and @literal \\@literal"} {
					model := audioPreviewModel(t, invocation, "--project=proj synthetic's", "--organization=org-世界", "--base-url=https://example.invalid/proxy/v1")
					model.input.SetValue(input)
					model.instructions.SetValue(instructions)
					require.Equal(t, input, model.input.Value())
					require.Equal(t, instructions, model.instructions.Value())
					preview := model.commandPreview()
					require.NotContains(t, preview, "Bash/zsh:", "label must remain outside executable command")
					capture := executeAudioPreview(t, shell, preview, directory)
					require.Equal(t, []string{"--project=proj synthetic's", "--organization=org-世界", "--base-url=https://example.invalid/proxy/v1", "audio", "speak", "--output", "speech.wav"}, capture.args)
					require.Equal(t, "true", capture.protected)
					assertAudioPreviewJSON(t, model, capture.stdin)
				}
			}
		})
	}
}

func TestAudioCommandPreviewEscapesControlsWithoutChangingArguments(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, directory := audioPreviewExecutable(t, "openai")
			invocation := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
			project := "proj\x1b[2J\r\u202e's"
			model := audioPreviewModel(t, invocation, "--project="+project)
			model.settings.Model = "synthetic\x1b]52;c;fake\a"
			model.input.SetValue("plain\nUnicode 世界")
			preview := model.commandPreview()
			for _, char := range preview {
				require.False(t, unicode.IsControl(char) || unicode.Is(unicode.Cf, char), "literal control in copyable command")
			}
			capture := executeAudioPreview(t, shell, preview, directory)
			require.Equal(t, []string{"--project=" + project, "audio", "speak", "--output", "speech.wav"}, capture.args)
			assertAudioPreviewJSON(t, model, capture.stdin)
		})
	}
}

func TestAudioCommandPreviewPreservesEmptyRootOverride(t *testing.T) {
	binary, directory := audioPreviewExecutable(t, "openai")
	invocation := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
	model := audioPreviewModel(t, invocation, "--project=")
	model.input.SetValue("Synthetic text")
	capture := executeAudioPreview(t, "bash", model.commandPreview(), directory)
	require.Equal(t, []string{"--project=", "audio", "speak", "--output", "speech.wav"}, capture.args)
	assertAudioPreviewJSON(t, model, capture.stdin)
}

func TestAudioCommandPreviewGoRunUsesOriginDirectory(t *testing.T) {
	_, directory := audioPreviewExecutable(t, "go")
	origin := filepath.Join(t.TempDir(), "source checkout's path")
	require.NoError(t, os.Mkdir(origin, 0700))
	t.Chdir(origin)
	origin, err := os.Getwd()
	require.NoError(t, err)
	model := audioPreviewModel(t, "go run ./cmd/openai", "--project=synthetic-project")
	model.input.SetValue("Synthetic Go-run preview")
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			capture := executeAudioPreview(t, shell, model.commandPreview(), directory)
			require.Equal(t, []string{"-C", origin, "run", "./cmd/openai", "--project=synthetic-project", "audio", "speak", "--output", "speech.wav"}, capture.args)
			assertAudioPreviewJSON(t, model, capture.stdin)
		})
	}
}

func TestAudioCommandPreviewSuppressesPrivateOverrides(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-fake-inherited")
	for _, name := range []string{"OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", requestconfig.MTLSClientCertFileEnv, requestconfig.MTLSClientKeyFileEnv} {
		t.Setenv(name, "")
	}
	for _, flags := range [][]string{
		{"--api-key=sk-fake-override"}, {"--api-key="}, {"--admin-api-key=sk-fake-admin"},
		{"--webhook-secret=fake-secret"}, {"--header=Authorization: Bearer fake-secret"},
		{"--mtls-client-cert-file=/synthetic/private/cert.pem"}, {"--mtls-client-key-file=/synthetic/private/key.pem"},
		{"--base-url=https://fake:secret@example.invalid"}, {"--base-url=https://example.invalid/v1?key=fake-secret"},
		{"--base-url=https://example.invalid/v1#fake-secret"}, {"--project=proj\x00private"},
	} {
		model := audioPreviewModel(t, "openai", flags...)
		model.input.SetValue("SYNTHETIC_PRIVATE_EDITOR_TEXT")
		preview := model.commandPreview()
		require.Equal(t, "Command preview unavailable with private request overrides.", preview)
		for _, private := range []string{"sk-fake", "fake-secret", "/synthetic/private", "SYNTHETIC_PRIVATE_EDITOR_TEXT", "printf", "audio speak"} {
			require.NotContains(t, preview, private)
		}
	}
	model := audioPreviewModel(t, "openai", "--api-key=sk-fake-inherited")
	require.Contains(t, model.commandPreview(), "OPENAI_UNTRUSTED_STDIN=true")
	require.NotContains(t, model.commandPreview(), "sk-fake-inherited")
}

func TestAudioCommandPreviewFullTextAndVerticalScroll(t *testing.T) {
	model := audioPreviewModel(t, "openai")
	model.input.SetValue(strings.Repeat("long 世界 input ", 80) + "INPUT_TAIL_MARKER")
	model.instructions.SetValue(strings.Repeat("delivery guidance ", 60) + "INSTRUCTION_TAIL_MARKER")
	model.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	preview := model.commandPreview()
	require.Contains(t, preview, "INPUT_TAIL_MARKER")
	require.Contains(t, preview, "INSTRUCTION_TAIL_MARKER")
	require.Nil(t, audioReviewMore(t, model, "Show command"))
	require.Equal(t, "info", model.modal)
	require.Equal(t, preview, model.infoText, "the command modal changed the executable bytes")
	initial := model.View().Content
	require.Contains(t, ansi.Strip(initial), "printf")
	require.Contains(t, ansi.Strip(initial), "Bash/zsh", "the copyable command must identify its supported shells")
	require.Nil(t, audioReviewKey(model, tea.KeyDown), "scrolling must never generate speech")
	require.Positive(t, model.infoOffset)
	require.NotEqual(t, initial, model.View().Content)
	for _, marker := range []string{"INPUT_TAIL_MARKER", "INSTRUCTION_TAIL_MARKER"} {
		require.Nil(t, audioReviewKey(model, tea.KeyHome))
		found := false
		for range len(model.infoRows()) {
			view := audioFeedbackView(t, model)
			if strings.Contains(view, marker) {
				found = true
				break
			}
			require.Nil(t, audioReviewKey(model, tea.KeyDown))
			require.False(t, model.busy)
		}
		require.True(t, found, "complete command must remain reachable through vertical scrolling: %s", marker)
	}
	require.Nil(t, audioReviewKey(model, tea.KeyHome))
	require.Zero(t, model.infoOffset)
	require.Nil(t, audioReviewKey(model, tea.KeyEnter), "viewing a command must not generate speech")
	require.False(t, model.busy)
	require.Equal(t, "info", model.modal)
}

func TestAudioCommandPreviewSupportsInformationScrollKeys(t *testing.T) {
	model := audioPreviewModel(t, "openai")
	model.input.SetValue(strings.Repeat("Synthetic speech ", 130))
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	require.Nil(t, audioReviewMore(t, model, "Show command"))
	first := model.View().Content
	for _, key := range []rune{tea.KeyDown, tea.KeyUp} {
		require.Nil(t, audioReviewKey(model, key), "command scrolling generated speech")
		require.False(t, model.busy)
		if key == tea.KeyDown {
			require.NotEqual(t, first, model.View().Content)
		}
	}
	require.Zero(t, model.infoOffset)
	require.Equal(t, first, model.View().Content)
}

func TestAudioCommandPrintNativeShellPreservesPrintedBytes(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		for _, width := range []int{40, 90} {
			t.Run(fmt.Sprintf("%s/%d", shell, width), func(t *testing.T) {
				binary, directory := audioPreviewExecutable(t, "openai preview's binary")
				model := audioPreviewModel(t, audioShellQuote(binary), "--project=proj synthetic's", "--organization=org-世界", "--base-url=https://example.invalid/proxy/v1")
				model.Update(tea.WindowSizeMsg{Width: width, Height: 12})
				// At width 40, this input crosses a display boundary inside quoted JSON.
				model.input.SetValue(strings.Repeat("A", 60) + "\n世界 says 'hello' @literal \\@literal")
				model.instructions.SetValue("Preserve   spaces and 'quotes'.")
				preview := model.commandPreview()
				model.openInformation(audioCommandTitle, preview, "more")
				require.Greater(t, len(model.infoRows()), 1)
				require.Contains(t, ansi.Strip(model.View().Content), "Ctrl+P print/exit")
				require.Nil(t, audioReviewKey(model, tea.KeyEnter))
				require.False(t, model.quit)
				_, quit := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				require.NotNil(t, quit)
				require.IsType(t, tea.QuitMsg{}, quit())
				require.True(t, model.quit)
				require.False(t, model.busy)
				require.Zero(t, model.exitCode)
				require.Equal(t, preview, model.commandToPrint)
				var output audioCommandBuffer
				require.NoError(t, printAudioCommand(t.Context(), &output, model.commandToPrint))
				require.Equal(t, preview+"\n", output.String())
				capture := executeAudioPreview(t, shell, output.String(), directory)
				require.Equal(t, []string{"--project=proj synthetic's", "--organization=org-世界", "--base-url=https://example.invalid/proxy/v1", "audio", "speak", "--output", "speech.wav"}, capture.args)
				require.Equal(t, "true", capture.protected)
				assertAudioPreviewJSON(t, model, capture.stdin)
				_, queued := model.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
				require.Nil(t, queued)
				require.False(t, model.busy)
			})
		}
	}
}

func TestAudioCommandPrintPreservesGuardsAndCancellation(t *testing.T) {
	for _, mode := range []string{"form", "help", "busy", "tiny", "private", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			flags := []string{}
			if mode == "private" {
				flags = append(flags, "--header=Authorization: Bearer fake-private")
			}
			model := audioPreviewModel(t, "openai", flags...)
			model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
			model.input.SetValue("Synthetic print guard")
			model.openInformation(audioCommandTitle, model.commandPreview(), "more")
			switch mode {
			case "form":
				model.modal = ""
			case "help":
				model.infoTitle = "Help"
			case "busy":
				model.busy = true
			case "tiny":
				model.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
			case "private":
				require.NotContains(t, model.View().Content, "Ctrl+P")
			case "cancel":
				_, quit := model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
				require.NotNil(t, quit)
				require.Equal(t, 130, model.exitCode)
			}
			model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			require.Empty(t, model.commandToPrint)
			require.Equal(t, mode == "cancel", model.quit)
		})
	}
}

func TestAudioCommandPrintPreservesFailedActionAndStopsPlayback(t *testing.T) {
	model := audioPreviewModel(t, "openai")
	model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model.input.SetValue("Synthetic retry command")
	model.lastErr = errors.New("synthetic prior API failure")
	originalError := model.lastErr
	playbackCanceled := false
	model.playing = true
	model.playCancel = func() { playbackCanceled = true }
	model.openInformation(audioCommandTitle, model.commandPreview(), "more")
	_, quit := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.NotNil(t, quit)
	require.IsType(t, tea.QuitMsg{}, quit())
	require.True(t, playbackCanceled)
	require.False(t, model.playing)
	require.Same(t, originalError, model.lastErr)
	require.NotEmpty(t, model.commandToPrint)
}

type audioCommandBuffer struct{ bytes.Buffer }

func (output *audioCommandBuffer) WriteContext(_ context.Context, data []byte) (int, error) {
	return output.Write(data)
}

type audioCommandWriteFunc func(context.Context, []byte) (int, error)

func (write audioCommandWriteFunc) WriteContext(ctx context.Context, data []byte) (int, error) {
	return write(ctx, data)
}

func TestAudioCommandPrintOutputFailuresAndCancellation(t *testing.T) {
	failure := errors.New("synthetic output failure")
	for _, test := range []struct {
		name string
		n    int
		err  error
		want error
	}{
		{"failure", 0, failure, failure},
		{"partial_failure", 3, failure, failure},
		{"short_write", 3, nil, io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := audioCommandWriteFunc(func(context.Context, []byte) (int, error) { return test.n, test.err })
			require.ErrorIs(t, printAudioCommand(t.Context(), writer, "synthetic-command"), test.want)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var output audioCommandBuffer
	require.ErrorIs(t, printAudioCommand(ctx, &output, "synthetic-command"), context.Canceled)
	require.Empty(t, output.String())
	require.NoError(t, printAudioCommand(t.Context(), &output, ""))
	require.Empty(t, output.String())
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	writer := audioCommandWriteFunc(func(_ context.Context, data []byte) (int, error) {
		cancel()
		return len(data), nil
	})
	require.ErrorIs(t, printAudioCommand(ctx, writer, "synthetic-command"), context.Canceled)
}

func TestAudioCommandPrintCancelsBlockedContextWriter(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan context.Context, 1)
	finished := make(chan error, 1)
	writer := audioCommandWriteFunc(func(received context.Context, _ []byte) (int, error) {
		entered <- received
		<-received.Done()
		return 0, received.Err()
	})
	go func() { finished <- printAudioCommand(ctx, writer, "synthetic-command") }()
	select {
	case received := <-entered:
		require.Same(t, ctx, received, "printing must use the retained signal context")
	case <-time.After(5 * time.Second):
		t.Fatal("print did not enter the context writer")
	}
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("print did not stop after cancellation")
	}
}
