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
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/charmbracelet/x/term"
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
		rendered, err := displaySavedImage(t.Context(), imageoutput.SavedImage{Path: "/not-a-real-image"}, &out, &diagnostic, mode)
		require.NoError(t, err)
		require.False(t, rendered)
		require.Empty(t, out.String())
		require.Empty(t, diagnostic.String())
	}
}

func TestWarpImageProtocolSelection(t *testing.T) {
	native, withoutColor := "blocks", ""
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		native, withoutColor = "kitty", "kitty"
	}
	for _, tc := range []struct {
		name, mode, want string
		tty              bool
		extra            map[string]string
	}{
		{"automatic", "auto", native, true, nil},
		{"explicit on", "on", native, true, nil},
		{"off", "off", "", true, nil},
		{"pipe cannot be forced", "on", "", false, nil},
		{"ci", "on", "", true, map[string]string{"CI": "true"}},
		{"ci false", "auto", native, true, map[string]string{"CI": "false"}},
		{"dumb", "on", "", true, map[string]string{"TERM": "dumb"}},
		{"tmux", "on", "blocks", true, map[string]string{"TMUX": "active"}},
		{"tmux term", "on", "blocks", true, map[string]string{"TERM": "tmux-256color"}},
		{"screen", "on", "blocks", true, map[string]string{"STY": "active"}},
		{"screen term", "on", "blocks", true, map[string]string{"TERM": "screen-256color"}},
		{"zellij", "on", "blocks", true, map[string]string{"ZELLIJ": "active"}},
		{"no color preserves native", "auto", withoutColor, true, map[string]string{"NO_COLOR": "1"}},
		{"clicolor preserves native", "auto", withoutColor, true, map[string]string{"CLICOLOR": "0"}},
		{"no color disables mux fallback", "on", "", true, map[string]string{"TMUX": "active", "NO_COLOR": "1"}},
		{"missing identity", "auto", "blocks", true, map[string]string{"TERM_PROGRAM": ""}},
		{"different identity", "auto", "blocks", true, map[string]string{"TERM_PROGRAM": "unknown"}},
		{"wsl distro auto", "auto", "blocks", true, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}},
		{"wsl distro on", "on", "blocks", true, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}},
		{"wsl interop auto", "auto", "blocks", true, map[string]string{"WSL_INTEROP": "/run/WSL/42_interop"}},
		{"wsl interop on", "on", "blocks", true, map[string]string{"WSL_INTEROP": "/run/WSL/42_interop"}},
		{"ssh connection auto", "auto", "blocks", true, map[string]string{"SSH_CONNECTION": "192.0.2.1 1234 192.0.2.2 22"}},
		{"ssh connection on", "on", "blocks", true, map[string]string{"SSH_CONNECTION": "192.0.2.1 1234 192.0.2.2 22"}},
		{"ssh client auto", "auto", "blocks", true, map[string]string{"SSH_CLIENT": "192.0.2.1 1234 22"}},
		{"ssh client on", "on", "blocks", true, map[string]string{"SSH_CLIENT": "192.0.2.1 1234 22"}},
		{"ssh tty auto", "auto", "blocks", true, map[string]string{"SSH_TTY": "/dev/pts/1"}},
		{"ssh tty on", "on", "blocks", true, map[string]string{"SSH_TTY": "/dev/pts/1"}},
		{"no color disables wsl fallback", "on", "", true, map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "NO_COLOR": "1"}},
		{"clicolor disables wsl fallback", "auto", "", true, map[string]string{"WSL_INTEROP": "/run/WSL/42_interop", "CLICOLOR": "0"}},
		{"no color disables ssh fallback", "auto", "", true, map[string]string{"SSH_TTY": "/dev/pts/1", "NO_COLOR": "1"}},
		{"clicolor disables ssh fallback", "on", "", true, map[string]string{"SSH_CONNECTION": "192.0.2.1 1234 192.0.2.2 22", "CLICOLOR": "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"TERM_PROGRAM": "WarpTerminal", "TERM": "xterm-256color"}
			for key, value := range tc.extra {
				env[key] = value
			}
			getenv := func(key string) string { return env[key] }
			require.Equal(t, tc.want, savedImageProtocol(tc.mode, tc.tty, getenv))
			require.Equal(t, tc.want, imageProgressProtocol(tc.mode, tc.tty, getenv))
		})
	}
}

func TestVSCodeImageOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, mode, setting, want string
		tty                       bool
		extra                     map[string]string
	}{
		{"unknown", "auto", "", "blocks", true, nil},
		{"on is not capability", "on", "", "blocks", true, nil},
		{"disabled", "auto", "0", "blocks", true, nil},
		{"strict assertion", "auto", "true", "blocks", true, nil},
		{"auto opt in", "auto", "1", "iterm-auto", true, nil},
		{"on opt in", "on", "1", "iterm-auto", true, nil},
		{"off wins", "off", "1", "", true, nil},
		{"redirected", "on", "1", "", false, nil},
		{"ci", "on", "1", "", true, map[string]string{"CI": "true"}},
		{"dumb", "on", "1", "", true, map[string]string{"TERM": "dumb"}},
		{"tmux", "on", "1", "blocks", true, map[string]string{"TMUX": "active"}},
		{"screen", "on", "1", "blocks", true, map[string]string{"TERM": "screen-256color"}},
		{"zellij", "on", "1", "blocks", true, map[string]string{"ZELLIJ": "active"}},
		{"no identity", "auto", "1", "blocks", true, map[string]string{"TERM_PROGRAM": ""}},
		{"different identity", "auto", "1", "iterm", true, map[string]string{"TERM_PROGRAM": "iTerm.app"}},
		{"no color retains native", "auto", "1", "iterm-auto", true, map[string]string{"NO_COLOR": "1"}},
		{"ssh explicit assertion", "auto", "1", "iterm-auto", true, map[string]string{"SSH_TTY": "/dev/pts/1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"TERM_PROGRAM": "vscode", "TERM": "xterm-256color", "OPENAI_VSCODE_IMAGES": tc.setting}
			for key, value := range tc.extra {
				env[key] = value
			}
			getenv := func(key string) string { return env[key] }
			require.Equal(t, tc.want, savedImageProtocol(tc.mode, tc.tty, getenv))
			require.Equal(t, tc.want, imageProgressProtocol(tc.mode, tc.tty, getenv))
		})
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
			rendered, err := displaySavedImage(t.Context(), imageoutput.SavedImage{Path: file, SHA256: sha256.Sum256(encoded.Bytes())}, os.Stdout, &diagnostic, "auto")
			require.NoError(t, err)
			require.True(t, rendered)
			require.Empty(t, diagnostic.String())
			unchanged, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, encoded.Bytes(), unchanged)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rendered, err := displaySavedImage(ctx, imageoutput.SavedImage{Path: file, SHA256: sha256.Sum256(encoded.Bytes())}, os.Stdout, &bytes.Buffer{}, "auto")
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, rendered)
	var diagnostic bytes.Buffer
	rendered, err = displaySavedImage(t.Context(), imageoutput.SavedImage{Path: "/not-a-real-file"}, os.Stdout, &diagnostic, "auto")
	require.NoError(t, err)
	require.False(t, rendered)
	require.Contains(t, diagnostic.String(), "No need to generate again")
	tall := image.NewNRGBA(image.Rect(0, 0, 1, 16384))
	encoded.Reset()
	require.NoError(t, png.Encode(&encoded, tall))
	require.NoError(t, os.WriteFile(file, encoded.Bytes(), 0o600))
	diagnostic.Reset()
	rendered, err = displaySavedImage(t.Context(), imageoutput.SavedImage{Path: file, SHA256: sha256.Sum256(encoded.Bytes())}, os.Stdout, &diagnostic, "auto")
	require.NoError(t, err)
	require.False(t, rendered)
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
			require.Equal(t, tc.want, savedImagePreviewColumns(image.Rect(0, 0, tc.imageWidth, tc.imageHeight), tc.terminalWidth, tc.terminalRows, 1, 2))
		})
	}
}

func TestSavedImagePreviewColumnsAvoids32BitOverflow(t *testing.T) {
	// Exercise the previous expression at its 32-bit runtime width on every
	// host. This valid 16-megapixel image was incorrectly classified as too tall.
	imageWidth, imageHeight, rows := int32(16<<20), int32(1), int32(100)
	previous := min(int32(64), (rows-2)*2*imageWidth/imageHeight)
	require.Negative(t, previous)
	require.Equal(t, 64, savedImagePreviewColumns(image.Rect(0, 0, int(imageWidth), int(imageHeight)), 120, int(rows), 1, 2))
}

func TestSavedImagePreviewFitsMeasuredCells(t *testing.T) {
	for _, tc := range []struct {
		name                                                              string
		imageWidth, imageHeight, width, rows, cellWidth, cellHeight, want int
	}{
		{"wide cells square", 1024, 1024, 120, 24, 10, 16, 35},
		{"tall cells square", 1024, 1024, 120, 24, 8, 20, 55},
		{"wide cells portrait", 1024, 1536, 120, 24, 10, 16, 23},
		{"tall cells portrait", 1024, 1536, 120, 24, 8, 20, 36},
		{"wide cells landscape", 1536, 1024, 120, 24, 10, 16, 52},
		{"tall cells landscape", 1536, 1024, 120, 24, 8, 20, 64},
		{"narrow square", 1024, 1024, 9, 24, 10, 16, 8},
		{"short terminal", 1024, 1024, 120, 3, 10, 16, 1},
		{"exact one-column fit", 10, 352, 120, 24, 10, 16, 1},
		{"just too tall", 10, 353, 120, 24, 10, 16, 0},
		{"exact fit with nonterminating cell ratio", 7, 450, 120, 32, 7, 15, 1},
		{"just too tall with nonterminating cell ratio", 7, 451, 120, 32, 7, 15, 0},
		{"extreme wide", 16 << 20, 1, 65535, 65535, 8, 20, 64},
		{"maximum image and window products", 16 << 20, 1, 65535, 65535, 65535, 65535, 64},
		{"extreme tall", 1, 16 << 20, 65535, 65535, 10, 16, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := savedImagePreviewColumns(image.Rect(0, 0, tc.imageWidth, tc.imageHeight), tc.width, tc.rows, tc.cellWidth, tc.cellHeight)
			require.Equal(t, tc.want, got)
			if got > 0 {
				// Native images retain source proportions. Their physical height
				// must fit, including fractional rows rounded by the terminal.
				height := float64(got*tc.cellWidth) * float64(tc.imageHeight) / float64(tc.imageWidth)
				require.LessOrEqual(t, math.Ceil(height/float64(tc.cellHeight)), float64(tc.rows-2))
			}
		})
	}
}

func TestFontPreviewFitsPhysicalImageAndAllocatedRows(t *testing.T) {
	for _, tc := range []struct {
		name                                                                        string
		imageWidth, imageHeight, width, rows, cellWidth, cellHeight, physical, font int
	}{
		{"tall cells portrait", 300, 900, 80, 24, 8, 20, 18, 14},
		{"slightly tall cells portrait", 1024, 2048, 80, 24, 8, 17, 23, 22},
		{"wide cells portrait", 300, 900, 80, 24, 10, 16, 11, 11},
		{"square capped to font width", 1024, 1024, 80, 24, 8, 20, 55, 32},
		{"narrow portrait", 300, 900, 9, 24, 8, 20, 8, 8},
		{"exact one-column allocation", 1, 44, 80, 24, 8, 20, 1, 1},
		{"one allocated column is too tall", 1, 45, 80, 24, 8, 20, 1, 0},
		{"font row cap fits", 1, 66, 80, 34, 8, 20, 1, 1},
		{"two-row terminal", 1, 1, 80, 2, 8, 20, 0, 0},
		{"one-column terminal", 1, 1, 1, 24, 8, 20, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounds := image.Rect(0, 0, tc.imageWidth, tc.imageHeight)
			physical := savedImagePreviewColumns(bounds, tc.width, tc.rows, tc.cellWidth, tc.cellHeight)
			require.Equal(t, tc.physical, physical)
			columns := terminalimage.FontPreviewColumns(bounds, physical, tc.rows)
			require.Equal(t, tc.font, columns)
			if columns > 0 {
				allocated := min(32, int(math.Ceil(float64(columns)*float64(tc.imageHeight)/(2*float64(tc.imageWidth)))))
				require.LessOrEqual(t, allocated, tc.rows-2)
			}
		})
	}
}

// The external sized-PTY check counts emitted rows between these markers.
// Missing TERM_SESSION_ID guarantees no native font automation can run.
func TestSavedImageFallbackResizeTerminal(t *testing.T) {
	if !isTerminal(os.Stdout) || runtime.GOOS == "windows" {
		t.Skip("requires a Unix terminal stdout")
	}
	stty, err := exec.LookPath("stty")
	if err != nil {
		t.Skip("requires stty for controlled PTY resizing")
	}
	width, height, err := term.GetSize(os.Stdout.Fd())
	require.NoError(t, err)
	resize := func(columns, rows int) error {
		command := exec.Command(stty, "columns", strconv.Itoa(columns), "rows", strconv.Itoa(rows))
		command.Stdin = os.Stdout
		return command.Run()
	}
	t.Cleanup(func() { require.NoError(t, resize(width, height)) })
	require.NoError(t, resize(80, 24))
	t.Setenv("TERM_SESSION_ID", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "")
	var diagnostic bytes.Buffer
	notice := previewNoticeWriter(func(data []byte) (int, error) {
		require.NoError(t, resize(80, 3))
		return diagnostic.Write(data)
	})
	fmt.Fprintln(os.Stdout, "FONT-FALLBACK-RESIZE BEGIN")
	err = displayDecodedSavedImage(t.Context(), image.NewRGBA(image.Rect(0, 0, 16, 16)), os.Stdout, notice, "font")
	fmt.Fprintln(os.Stdout, "FONT-FALLBACK-RESIZE END")
	require.NoError(t, err)
	require.Contains(t, diagnostic.String(), "Sharp inline preview unavailable:")
}

type previewNoticeWriter func([]byte) (int, error)

func (w previewNoticeWriter) Write(data []byte) (int, error) { return w(data) }

func TestSavedImageProtocolKittyInheritedAppleIdentity(t *testing.T) {
	apple := "blocks"
	if runtime.GOOS == "darwin" {
		apple = "font"
	}
	for _, tc := range []struct {
		name, mode, want string
		tty              bool
		extra            map[string]string
	}{
		{"auto", "auto", "kitty", true, nil},
		{"on", "on", "kitty", true, nil},
		{"off", "off", "", true, nil},
		{"pipe", "on", "", false, nil},
		{"ci", "on", "", true, map[string]string{"CI": "true"}},
		{"dumb", "on", "", true, map[string]string{"TERM": "dumb"}},
		{"tmux", "on", "blocks", true, map[string]string{"TMUX": "active"}},
		{"screen", "on", "blocks", true, map[string]string{"STY": "active"}},
		{"zellij", "on", "blocks", true, map[string]string{"ZELLIJ": "active"}},
		{"tmux term", "on", "blocks", true, map[string]string{"TERM": "tmux-256color"}},
		{"screen term", "on", "blocks", true, map[string]string{"TERM": "screen-256color"}},
		{"no window identity", "on", apple, true, map[string]string{"KITTY_WINDOW_ID": ""}},
		{"inherited window identity", "on", apple, true, map[string]string{"TERM": "xterm-256color"}},
		{"vscode identity", "on", "blocks", true, map[string]string{"TERM_PROGRAM": "vscode"}},
		{"vscode opt in", "on", "iterm-auto", true, map[string]string{"TERM_PROGRAM": "vscode", "OPENAI_VSCODE_IMAGES": "1"}},
		{"iterm identity", "on", "iterm", true, map[string]string{"TERM_PROGRAM": "iTerm.app"}},
		{"no color", "auto", "kitty", true, map[string]string{"NO_COLOR": "1"}},
		{"ssh", "auto", "kitty", true, map[string]string{"SSH_TTY": "/dev/pts/1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{
				"TERM": "xterm-kitty", "TERM_PROGRAM": "Apple_Terminal",
				"KITTY_WINDOW_ID": "1", "COLORTERM": "truecolor",
			}
			for key, value := range tc.extra {
				env[key] = value
			}
			require.Equal(t, tc.want, savedImageProtocol(tc.mode, tc.tty, func(key string) string { return env[key] }))
		})
	}
}
