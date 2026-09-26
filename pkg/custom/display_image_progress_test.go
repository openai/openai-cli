package custom

import (
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestImageProgressProtocolPreservesFinalFontOptIn(t *testing.T) {
	for _, mode := range []string{"auto", "on", "off"} {
		for _, program := range []string{"Apple_Terminal", "kitty", "iTerm.app", "other"} {
			for _, disabled := range []string{"", "CI", "NO_COLOR", "CLICOLOR"} {
				env := map[string]string{"TERM_PROGRAM": program, "TERM": "xterm-256color"}
				if disabled != "" {
					env[disabled] = "1"
					if disabled == "CLICOLOR" {
						env[disabled] = "0"
					}
				}
				getenv := func(k string) string { return env[k] }
				progress := imageProgressProtocol(mode, true, getenv)
				require.NotEqual(t, "font", progress, "temporary previews must never register or cache fonts")
				require.Empty(t, imageProgressProtocol(mode, false, getenv))
				final := savedImageProtocol(mode, true, getenv)
				if final == "font" {
					want := "blocks"
					if disabled == "NO_COLOR" || disabled == "CLICOLOR" {
						want = ""
					}
					require.Equal(t, want, progress)
				} else {
					require.Equal(t, final, progress)
				}
			}
		}
	}
	if runtime.GOOS == "darwin" {
		require.Equal(t, "font", savedImageProtocol("on", true, func(k string) string {
			if k == "TERM_PROGRAM" {
				return "Apple_Terminal"
			}
			return ""
		}))
	}
}

func TestImageProgressDecodeFailureAndCancellation(t *testing.T) {
	for _, raw := range []string{`{}`, `{"b64_json":null}`, `{"b64_json":{}}`, `{"b64_json":"private-invalid-image"}`, `{"b64_json":"cHJpdmF0ZS1ub3QtYW4taW1hZ2U="}`} {
		var output, diagnostics bytes.Buffer
		plan := &imageOutputPlan{partialImages: 2, diagnostics: &diagnostics}
		unavailable, err := plan.displayImageProgress(t.Context(), gjson.Parse(raw), &output, "kitty")
		require.NoError(t, err)
		require.True(t, unavailable)
		require.Equal(t, "Progress preview unavailable; waiting for the final image.\n", output.String())
		require.Empty(t, diagnostics.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var diagnostics bytes.Buffer
	plan := &imageOutputPlan{diagnostics: &diagnostics}
	_, err := plan.displayImageProgress(ctx, gjson.Parse(`{}`), io.Discard, "kitty")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, diagnostics.String())
	sentinel := errors.New("synthetic warning output failure")
	_, err = plan.displayImageProgress(t.Context(), gjson.Parse(`{}`), failingImageWriter{sentinel}, "kitty")
	require.ErrorIs(t, err, sentinel)
}

func TestImageProgressHeaderFailureStops(t *testing.T) {
	event := progressTestEvent(t, "image_generation.partial_image", 0)
	sentinel := errors.New("synthetic output failure")
	for _, writer := range []io.Writer{failingImageWriter{sentinel}, savedPreviewFailWriter{}} {
		plan := &imageOutputPlan{partialImages: 1}
		_, err := plan.displayImageProgress(t.Context(), event, writer, "kitty")
		if _, ok := writer.(failingImageWriter); ok {
			require.ErrorIs(t, err, sentinel)
		} else {
			require.ErrorIs(t, err, io.ErrShortWrite)
		}
	}
}

func progressTestEvent(t *testing.T, kind string, index int) gjson.Result {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, img))
	return gjson.Parse(fmt.Sprintf(`{"type":%q,"partial_image_index":%d,"b64_json":%q}`, kind, index, base64.StdEncoding.EncodeToString(encoded.Bytes())))
}

type progressTestStream struct {
	savedImageTestStream
	beforeNext func(int)
}

func (s *progressTestStream) Next() bool {
	if s.beforeNext != nil {
		s.beforeNext(s.index)
	}
	return s.savedImageTestStream.Next()
}

// Run in a sized PTY. The external capture also asserts renderer sequences and
// exact progress-label counts between the case markers written here.
func TestImageProgressTerminal(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("requires actual terminal stdout")
	}
	for _, key := range []string{"CI", "TMUX", "STY", "ZELLIJ", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "NO_COLOR", "CLICOLOR"} {
		t.Setenv(key, "")
	}
	t.Setenv("TERM", "xterm-256color")
	for _, scenario := range []string{"kitty", "iterm", "apple", "off", "ci", "no-color", "malformed", "malformed-error", "malformed-cancel", "invalid-index", "stream-error", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", "kitty")
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CACHE_HOME", home)
			var diagnostic bytes.Buffer
			plan := &imageOutputPlan{directory: t.TempDir(), name: "final", inline: "on", partialImages: 2, diagnostics: &diagnostic}
			switch scenario {
			case "iterm":
				t.Setenv("TERM_PROGRAM", "iTerm.app")
			case "apple":
				t.Setenv("TERM_PROGRAM", "Apple_Terminal")
			case "off":
				plan.inline = "off"
			case "ci":
				t.Setenv("CI", "true")
			case "no-color":
				t.Setenv("TERM_PROGRAM", "Apple_Terminal")
				t.Setenv("NO_COLOR", "1")
			}
			first := progressTestEvent(t, "image_generation.partial_image", 0)
			second := progressTestEvent(t, "image_edit.partial_image", 1)
			if scenario == "malformed" || scenario == "malformed-error" || scenario == "malformed-cancel" || scenario == "off" || scenario == "ci" || scenario == "no-color" {
				first = gjson.Parse(`{"type":"image_generation.partial_image","partial_image_index":0,"b64_json":"private-invalid"}`)
			}
			if scenario == "invalid-index" {
				first = gjson.Parse(`{"type":"image_generation.partial_image","partial_image_index":0.5,"b64_json":"private-invalid"}`)
				second = gjson.Parse(`{"type":"image_generation.partial_image","b64_json":"private-invalid"}`)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream := &progressTestStream{savedImageTestStream: savedImageTestStream{events: []outputJSON{{first}, {first}, {second}, {progressTestEvent(t, "image_generation.completed", 0)}}}}
			stream.beforeNext = func(index int) {
				if index == 3 {
					// Final font policy is covered separately. Keep this synthetic
					// PTY from invoking native font APIs; partials must leave no cache.
					plan.inline = "off"
					files, err := os.ReadDir(home)
					require.NoError(t, err)
					require.Empty(t, files)
					if scenario == "cancel" || scenario == "malformed-cancel" {
						cancel()
					}
				}
			}
			if scenario == "stream-error" || scenario == "malformed-error" {
				stream.events = stream.events[:3]
				stream.err = errors.New("synthetic-private-stream-error")
			}
			fmt.Fprintln(os.Stdout, "PROGRESS-CASE", scenario)
			err := saveFinalImageStream(ctx, stream, plan, os.Stdout)
			fmt.Fprintln(os.Stdout, "PROGRESS-END", scenario)
			if scenario == "cancel" || scenario == "malformed-cancel" {
				require.ErrorIs(t, err, context.Canceled)
			} else if scenario == "stream-error" || scenario == "malformed-error" {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "synthetic-private")
			} else {
				require.NoError(t, err)
			}
			require.True(t, stream.closed)
			files, readErr := os.ReadDir(plan.directory)
			require.NoError(t, readErr)
			if scenario == "cancel" || scenario == "malformed-cancel" || scenario == "stream-error" || scenario == "malformed-error" {
				require.Empty(t, files)
			} else {
				require.Len(t, files, 1)
				require.Equal(t, "final.png", filepath.Base(files[0].Name()))
			}
			require.Empty(t, diagnostic.String())
		})
	}
}
