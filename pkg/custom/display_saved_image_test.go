package custom

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSavedImageProtocolSelection(t *testing.T) {
	apple := "blocks"
	if runtime.GOOS == "darwin" {
		apple = "font"
	}
	for _, tc := range []struct {
		name, mode string
		tty        bool
		env        map[string]string
		want       string
	}{
		{"pipe cannot be forced", "on", false, map[string]string{"TERM_PROGRAM": "kitty"}, ""},
		{"off", "off", true, map[string]string{"TERM_PROGRAM": "kitty"}, ""},
		{"kitty", "auto", true, map[string]string{"TERM_PROGRAM": "kitty"}, "kitty"},
		{"ghostty", "auto", true, map[string]string{"TERM_PROGRAM": "ghostty"}, "kitty"},
		{"iterm", "auto", true, map[string]string{"TERM_PROGRAM": "iTerm.app"}, "iterm"},
		{"wezterm", "auto", true, map[string]string{"TERM_PROGRAM": "WezTerm"}, "iterm"},
		{"ssh kitty", "auto", true, map[string]string{"TERM": "xterm-kitty", "SSH_CONNECTION": "remote"}, "kitty"},
		{"conflicting identity", "on", true, map[string]string{"TERM": "xterm-kitty", "TERM_PROGRAM": "vscode"}, ""},
		{"ci", "on", true, map[string]string{"TERM_PROGRAM": "kitty", "CI": "true"}, ""},
		{"ci false", "auto", true, map[string]string{"TERM_PROGRAM": "kitty", "CI": "false"}, "kitty"},
		{"dumb", "on", true, map[string]string{"TERM_PROGRAM": "kitty", "TERM": "dumb"}, ""},
		{"tmux env", "on", true, map[string]string{"TERM_PROGRAM": "kitty", "TMUX": "active"}, ""},
		{"tmux term", "on", true, map[string]string{"TERM_PROGRAM": "kitty", "TERM": "tmux-256color"}, "blocks"},
		{"screen", "auto", true, map[string]string{"TERM_PROGRAM": "iTerm.app", "STY": "active"}, ""},
		{"zellij", "auto", true, map[string]string{"TERM_PROGRAM": "ghostty", "ZELLIJ": "active"}, ""},
		{"apple auto never activates font", "auto", true, map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, "blocks"},
		{"apple opt in", "on", true, map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, apple},
		{"apple ssh", "on", true, map[string]string{"TERM_PROGRAM": "Apple_Terminal", "SSH_TTY": "/dev/pts/1"}, "blocks"},
		{"apple mux", "on", true, map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "screen-256color"}, "blocks"},
		{"no color fallback", "auto", true, map[string]string{"TERM": "xterm-256color", "NO_COLOR": "1"}, ""},
		{"no color native retained", "auto", true, map[string]string{"TERM_PROGRAM": "kitty", "NO_COLOR": "1"}, "kitty"},
		{"color disabled", "auto", true, map[string]string{"TERM": "xterm-256color", "CLICOLOR": "0"}, ""},
		{"basic terminal", "auto", true, map[string]string{"TERM": "vt100"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, savedImageProtocol(tc.mode, tc.tty, func(k string) string { return tc.env[k] }))
		})
	}
}

func TestSavedImagePreviewSkipsNonTerminalWithoutReadingFile(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	for _, mode := range []string{"auto", "on", "off"} {
		var out, diagnostic bytes.Buffer
		require.NoError(t, displaySavedImage(t.Context(), "/not-a-real-image", &out, &diagnostic, mode))
		require.Empty(t, out.String())
		require.Empty(t, diagnostic.String())
	}
}

// Run in a real PTY as well as the ordinary suite. This test writes synthetic
// native protocol output; it must never activate the Apple Terminal font path.
func TestSavedImagePreviewTerminal(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("requires actual terminal stdout")
	}
	for _, key := range []string{"CI", "TMUX", "STY", "ZELLIJ", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "NO_COLOR", "CLICOLOR"} {
		t.Setenv(key, "")
	}
	t.Setenv("TERM", "xterm-256color")
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, img))
	file := filepath.Join(t.TempDir(), "synthetic.png")
	require.NoError(t, os.WriteFile(file, encoded.Bytes(), 0o600))
	for _, program := range []string{"kitty", "iTerm.app", "unknown"} {
		t.Run(program, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", program)
			var diagnostic bytes.Buffer
			require.NoError(t, displaySavedImage(t.Context(), file, os.Stdout, &diagnostic, "auto"))
			require.Empty(t, diagnostic.String())
			unchanged, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, encoded.Bytes(), unchanged)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, displaySavedImage(ctx, file, os.Stdout, &bytes.Buffer{}, "auto"), context.Canceled)
	var diagnostic bytes.Buffer
	require.NoError(t, displaySavedImage(t.Context(), "/not-a-real-file", os.Stdout, &diagnostic, "auto"))
	require.Contains(t, diagnostic.String(), "No need to generate again")
	tall := image.NewNRGBA(image.Rect(0, 0, 1, 16384))
	encoded.Reset()
	require.NoError(t, png.Encode(&encoded, tall))
	require.NoError(t, os.WriteFile(file, encoded.Bytes(), 0o600))
	diagnostic.Reset()
	require.NoError(t, displaySavedImage(t.Context(), file, os.Stdout, &diagnostic, "auto"))
	require.Contains(t, diagnostic.String(), "too tall")
	diagnostic.Reset()
	require.ErrorIs(t, displayDecodedSavedImage(t.Context(), tall, os.Stdout, &diagnostic, "blocks"), errImagePreviewUnavailable)
	require.Contains(t, diagnostic.String(), "too tall")
}

func TestReportImagePreviewUnavailableRetainsOutputFailure(t *testing.T) {
	var out bytes.Buffer
	require.ErrorIs(t, reportImagePreviewUnavailable(&out, "Image too tall"), errImagePreviewUnavailable)
	require.Equal(t, "Image too tall\n", out.String())
	failure := errors.New("synthetic diagnostic failure")
	require.ErrorIs(t, reportImagePreviewUnavailable(savedPreviewFailWriter{failure}, "unavailable"), failure)
	require.ErrorIs(t, reportImagePreviewUnavailable(savedPreviewFailWriter{}, "unavailable"), io.ErrShortWrite)
}

type savedPreviewFailWriter struct{ err error }

func (w savedPreviewFailWriter) Write([]byte) (int, error) { return 0, w.err }
