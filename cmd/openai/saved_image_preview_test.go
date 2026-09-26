package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/imageprefs"
	"github.com/stretchr/testify/require"
)

func localImageTestEnvironment(t *testing.T, endpoint string) ([]string, string) {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME"} {
		t.Setenv(key, home)
	}
	config, err := os.UserConfigDir()
	require.NoError(t, err)
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home, "XDG_CONFIG_HOME=" + home, "OPENAI_BASE_URL=" + endpoint, "OPENAI_API_KEY=", "TERM=xterm-256color", "TERM_PROGRAM=unknown", "CI=", "NO_COLOR=", "CLICOLOR=", "TMUX=", "STY=", "ZELLIJ=", "SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}
	return env, filepath.Join(config, "openai", "image-preferences.json")
}

func TestMainImageInlinePreferenceCommandsAreLocal(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected API request", 500)
	}))
	defer server.Close()
	env, prefs := localImageTestEnvironment(t, server.URL)
	inputPath := filepath.Join(t.TempDir(), "not-api-stdin.json")
	require.NoError(t, os.WriteFile(inputPath, []byte(`{"sensitive":"synthetic input remains unread"}`), 0600))
	input, err := os.Open(inputPath)
	require.NoError(t, err)
	defer func() { _ = input.Close() }()
	for _, mode := range []string{"off", "on", "off"} {
		got := runMainDispatchWithStdin(t, "bash", env, input, "openai", "images", "inline", mode)
		require.Zero(t, got.code, got.stderr)
		require.Empty(t, got.stderr)
		require.Contains(t, got.stdout, "Automatic image previews "+mode)
		actual, err := imageprefs.Load(t.Context(), prefs)
		require.NoError(t, err)
		require.Equal(t, mode, actual)
	}
	position, err := input.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	require.Zero(t, position, "local commands must not consume API stdin")
	require.Zero(t, requests.Load())
	original, err := os.ReadFile(prefs)
	require.NoError(t, err)
	for _, args := range [][]string{{"--format", "json", "images", "inline", "on"}, {"--transform", "id", "images", "inline", "on"}, {"--raw-output", "images", "inline", "off"}, {"images", "inline", "on", "extra"}} {
		got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, args...)...)
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
		current, err := os.ReadFile(prefs)
		require.NoError(t, err)
		require.Equal(t, original, current)
	}
	require.NoError(t, os.WriteFile(prefs, []byte(`{"version":99,"inline":true,"future":"retained"}`), 0600))
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "images", "inline", "off")
	require.NotZero(t, got.code)
	require.Contains(t, got.stderr, "Existing settings were kept")
	current, err := os.ReadFile(prefs)
	require.NoError(t, err)
	require.Contains(t, string(current), "retained")
	require.Zero(t, requests.Load())
}

func TestMainSavedImagePreviewRejectsNonTerminalAndUnsupportedOutput(t *testing.T) {
	env, prefs := localImageTestEnvironment(t, "http://127.0.0.1:1")
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"images", "preview"}, "Provide one saved image"},
		{[]string{"images", "preview", "one", "two"}, "Provide one saved image"},
		{[]string{"images", "preview", "missing.png"}, "require a terminal"},
		{[]string{"--format", "json", "images", "preview", "missing.png"}, "readable output"},
		{[]string{"--transform", "id", "images", "preview", "missing.png"}, "cannot use"},
		{[]string{"--raw-output", "images", "preview", "missing.png"}, "cannot use"},
		{[]string{"images", "preview", "--inline", "off", "missing.png"}, "Use --inline auto or on"},
	} {
		got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, tc.args...)...)
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
		require.Contains(t, got.stderr, tc.message)
	}
	_, err := os.Stat(prefs)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// Run this group in a sized PTY. It exercises the real production entrypoint
// with no key, asserts no network calls, and never activates a font.
func TestMainSavedImagePreviewTerminal(t *testing.T) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("requires actual terminal stdout")
	}
	var requests atomic.Int32
	payload := imageGenerationPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, imageGenerationResponse(payload))
	}))
	defer server.Close()
	env, prefs := localImageTestEnvironment(t, server.URL)
	require.NoError(t, imageprefs.Save(t.Context(), prefs, false))
	source := filepath.Join(t.TempDir(), "saved image 雪\nname.png")
	require.NoError(t, os.WriteFile(source, payload, 0600))
	inputPath := filepath.Join(t.TempDir(), "stdin.json")
	require.NoError(t, os.WriteFile(inputPath, []byte(`{"should":"remain unread"}`), 0600))
	input, err := os.Open(inputPath)
	require.NoError(t, err)
	defer func() { _ = input.Close() }()
	run := func(args ...string) (int, string) {
		t.Helper()
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)...)
		child.Env = append([]string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1"}, env...)
		child.Stdout, child.Stdin = os.Stdout, input
		var diagnostic bytes.Buffer
		child.Stderr = &diagnostic
		err = child.Run()
		require.NoError(t, ctx.Err())
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			require.NoError(t, err)
		}
		return code, diagnostic.String()
	}
	code, diagnostic := run("images", "preview", source)
	require.Zero(t, code, diagnostic)
	require.Empty(t, diagnostic)
	require.Zero(t, requests.Load(), "local preview contacted the API")
	position, err := input.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	require.Zero(t, position)
	original, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, payload, original)
	for _, path := range []string{filepath.Join(t.TempDir(), "absent.png"), t.TempDir()} {
		code, diagnostic = run("images", "preview", path)
		require.NotZero(t, code)
		require.Contains(t, diagnostic, "Could not preview")
	}
	require.NoError(t, os.WriteFile(source, []byte("private-synthetic-not-image"), 0600))
	code, diagnostic = run("images", "preview", source)
	require.NotZero(t, code)
	require.Contains(t, diagnostic, "Could not preview")
	require.NotContains(t, diagnostic, "private-synthetic")
	var tall bytes.Buffer
	require.NoError(t, png.Encode(&tall, image.NewNRGBA(image.Rect(0, 0, 1, 16384))))
	require.NoError(t, os.WriteFile(source, tall.Bytes(), 0600))
	code, diagnostic = run("images", "preview", source)
	require.NotZero(t, code)
	require.Contains(t, diagnostic, "too tall")
	require.NotContains(t, diagnostic, "could not be completed")
	for _, format := range []string{"json", "yaml"} {
		code, diagnostic = run("--format-error", format, "images", "preview", source)
		require.NotZero(t, code)
		payload := decodeMainStructuredError(t, format, diagnostic)
		require.Contains(t, payload["message"], "too tall")
	}
	code, diagnostic = run("--format-error", "json", "--transform-error", "message", "images", "preview", source)
	require.NotZero(t, code)
	var message string
	require.NoError(t, json.Unmarshal([]byte(diagnostic), &message))
	require.Contains(t, message, "too tall")
	require.NoError(t, os.WriteFile(source, payload, 0600))
	require.NoError(t, os.WriteFile(prefs, []byte("invalid-private-settings"), 0600))
	code, diagnostic = run("images", "preview", source)
	require.Zero(t, code, diagnostic)
	require.Empty(t, diagnostic, "explicit preview ignores automatic preference")
	require.Zero(t, requests.Load())
	// Successful saving must not hide preference warnings in the generated error buffer.
	for _, flag := range []string{"", "auto"} {
		args := []string{"images", "generate", "--prompt", "synthetic preference test"}
		if flag != "" {
			args = append(args, "--inline", flag)
		}
		// Generation can read API stdin, so provide EOF for these separate calls.
		require.NoError(t, input.Close())
		input, err = os.Open(os.DevNull)
		require.NoError(t, err)
		code, diagnostic = run(args...)
		require.Zero(t, code, diagnostic)
		if flag == "" {
			require.Contains(t, diagnostic, "Could not read the inline preference")
		} else {
			require.Empty(t, diagnostic)
		}
	}
	require.Equal(t, int32(2), requests.Load())
	fmt.Fprintln(os.Stdout, "Local preview verified: no key or requests; source and stdin preserved.")
}
