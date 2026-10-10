package custom

import (
	"bytes"
	"context"
	"encoding/json"
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
		mtlsClientCertFileFlag, mtlsClientKeyFileFlag} {
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
	for _, name := range []string{"OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", mtlsClientCertFileEnv, mtlsClientKeyFileEnv} {
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

func TestAudioCommandPreviewFullTextAndHorizontalScroll(t *testing.T) {
	model := audioPreviewModel(t, "openai")
	model.input.SetValue(strings.Repeat("long 世界 input ", 80) + "INPUT_TAIL_MARKER")
	model.instructions.SetValue(strings.Repeat("delivery guidance ", 60) + "INSTRUCTION_TAIL_MARKER")
	model.width, model.height, model.focus = 60, 18, 3
	preview := model.commandPreview()
	require.Contains(t, preview, "INPUT_TAIL_MARKER")
	require.Contains(t, preview, "INSTRUCTION_TAIL_MARKER")
	initial := model.View().Content
	require.Contains(t, initial, "Bash/zsh:")
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Nil(t, command, "scrolling must never generate speech")
	require.Greater(t, model.previewOffset, 0)
	require.NotEqual(t, initial, model.View().Content)
	for _, marker := range []string{"INPUT_TAIL_MARKER", "INSTRUCTION_TAIL_MARKER"} {
		prefix := "Bash/zsh: " + preview[:strings.Index(preview, marker)]
		model.previewOffset = ansi.StringWidth(prefix)
		view := model.View().Content
		require.Contains(t, view, marker, "complete command must remain reachable through horizontal scrolling")
		for _, row := range strings.Split(view, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(row), model.width-1)
		}
	}
	_, command = model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	require.Nil(t, command)
	require.Zero(t, model.previewOffset)
}
