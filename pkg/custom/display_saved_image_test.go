package custom

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/openai/openai-cli/internal/terminalimage"
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
		require.NoError(t, displaySavedImage(t.Context(), imageoutput.SavedImage{Path: "/not-a-real-image"}, &out, &diagnostic, mode))
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
			require.NoError(t, displaySavedImage(t.Context(), imageoutput.SavedImage{Path: file, SHA256: sha256.Sum256(encoded.Bytes())}, os.Stdout, &diagnostic, "auto"))
			require.Empty(t, diagnostic.String())
			unchanged, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, encoded.Bytes(), unchanged)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, displaySavedImage(ctx, imageoutput.SavedImage{Path: file, SHA256: sha256.Sum256(encoded.Bytes())}, os.Stdout, &bytes.Buffer{}, "auto"), context.Canceled)
	var diagnostic bytes.Buffer
	require.NoError(t, displaySavedImage(t.Context(), imageoutput.SavedImage{Path: "/not-a-real-file"}, os.Stdout, &diagnostic, "auto"))
	require.Contains(t, diagnostic.String(), "No need to generate again")
	tall := image.NewNRGBA(image.Rect(0, 0, 1, 16384))
	encoded.Reset()
	require.NoError(t, png.Encode(&encoded, tall))
	require.NoError(t, os.WriteFile(file, encoded.Bytes(), 0o600))
	diagnostic.Reset()
	require.NoError(t, displaySavedImage(t.Context(), imageoutput.SavedImage{Path: file, SHA256: sha256.Sum256(encoded.Bytes())}, os.Stdout, &diagnostic, "auto"))
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

func TestReportImageFontUnavailableOmitsFilesystemPaths(t *testing.T) {
	pathErr := &os.PathError{Op: "open", Path: "/Users/synthetic/Library/Caches/openai/private-gallery/font.ttf", Err: os.ErrPermission}
	linkErr := &os.LinkError{Op: "rename", Old: "/Users/synthetic/private-old/font.ttf", New: "/Users/synthetic/private-new/font.ttf", Err: os.ErrPermission}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"path", pathErr},
		{"link", linkErr},
		{"wrapped path", fmt.Errorf("cache at /Users/synthetic: %w", pathErr)},
		{"wrapped link", fmt.Errorf("install: %w", linkErr)},
		{"nested path", &os.PathError{Op: "write", Path: "/Users/synthetic/another-gallery/font.ttf", Err: linkErr}},
		{"joined paths", errors.Join(pathErr, linkErr)},
		{"wrapped join", fmt.Errorf("prepare: %w", errors.Join(errors.New("restore failed"), pathErr, linkErr))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, reportImageFontUnavailable(&out, &terminalimage.FontError{Err: tc.err}))
			require.Equal(t, "Sharp inline preview unavailable: image font files could not be accessed; open the saved file to view it. The image is saved; no need to generate again.\n", out.String())
		})
	}
}

func TestReportImageFontUnavailablePreservesGuidance(t *testing.T) {
	for _, message := range []string{
		"sharp previews require TERM_SESSION_ID; open a new Apple Terminal tab and retry the saved image",
		"this tab's image font is full; open a new Terminal tab to continue displaying sharp images",
		"Terminal font or spacing changed while preparing the image",
	} {
		var out bytes.Buffer
		require.NoError(t, reportImageFontUnavailable(&out, &terminalimage.FontError{Err: errors.New(message)}))
		require.Equal(t, "Sharp inline preview unavailable: "+message+". The image is saved; no need to generate again.\n", out.String())
	}
}

func TestReportImageFontUnavailableEscapesControls(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, reportImageFontUnavailable(&out, &terminalimage.FontError{Err: errors.New("font \x1b[31m\u202ename")}))
	require.NotContains(t, out.String(), "\x1b")
	require.NotContains(t, out.String(), "\u202e")
	require.Contains(t, out.String(), `\u202ename`)
}

func TestReportImageFontUnavailableRetainsOutputFailure(t *testing.T) {
	failure := errors.New("synthetic diagnostic failure")
	for _, cause := range []error{
		errors.New("open a new Terminal tab"),
		&os.PathError{Op: "open", Path: "/Users/synthetic/private-gallery/font.ttf", Err: os.ErrPermission},
	} {
		fontErr := &terminalimage.FontError{Err: cause}
		require.ErrorIs(t, reportImageFontUnavailable(savedPreviewFailWriter{failure}, fontErr), failure)
		require.ErrorIs(t, reportImageFontUnavailable(savedPreviewFailWriter{}, fontErr), io.ErrShortWrite)
	}
}

type savedPreviewFailWriter struct{ err error }

func (w savedPreviewFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestSavedImagePreviewColumns(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		imageWidth, imageHeight     int
		terminalWidth, terminalRows int
		want                        int
	}{
		{"square in tall terminal", 1024, 1024, 120, 40, 64},
		{"square in short terminal", 1024, 1024, 120, 24, 44},
		{"landscape", 1536, 1024, 120, 24, 64},
		{"portrait rounds down", 1024, 1536, 120, 24, 29},
		{"narrow terminal", 1024, 1024, 20, 40, 19},
		{"maximum wide image", 16 << 20, 1, 120, 100, 64},
		{"maximum wide image and terminal", 16 << 20, 1, 65535, 65535, 64},
		{"maximum tall image", 1, 16 << 20, 120, 100, 0},
		{"maximum tall image and terminal", 1, 16 << 20, 65535, 65535, 0},
		{"one column fits", 1, 196, 120, 100, 1},
		{"one column is too tall", 1, 197, 120, 100, 0},
		{"two row terminal", 1, 1, 120, 2, 0},
		{"one column terminal", 1, 1, 1, 40, 0},
		{"empty image", 0, 0, 120, 40, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, savedImagePreviewColumns(image.Rect(0, 0, tc.imageWidth, tc.imageHeight), tc.terminalWidth, tc.terminalRows))
		})
	}
}

func TestSavedImagePreviewColumnsAvoids32BitOverflow(t *testing.T) {
	// Exercise the previous expression at its 32-bit runtime width on every
	// host. This valid 16-megapixel image was incorrectly classified as too tall.
	imageWidth, imageHeight, rows := int32(16<<20), int32(1), int32(100)
	previous := min(int32(64), (rows-2)*2*imageWidth/imageHeight)
	require.Negative(t, previous)
	require.Equal(t, 64, savedImagePreviewColumns(image.Rect(0, 0, int(imageWidth), int(imageHeight)), 120, int(rows)))
}
