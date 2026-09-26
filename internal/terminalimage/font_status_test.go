package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/stretchr/testify/require"
)

func TestFontStatusReadOnlySnapshots(t *testing.T) {
	for _, state := range []string{"new session", "selected image font", "changed text font", "narrow", "foreign gallery", "denied", "escaped font"} {
		t.Run(state, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			if state != "new session" {
				require.NoError(t, displayImageFont(t.Context(), io.Discard, image.NewRGBA(image.Rect(0, 0, 4, 4)), 16, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
			}
			size := testFontViewport()
			switch state {
			case "changed text font":
				bridge.status.FontName = "GoMono"
			case "escaped font":
				bridge.status.FontName = "Synthetic\x1b]52;unsafe\a"
			case "foreign gallery":
				bridge.status.FontName = "OpenAIImages-aaaaaaaa-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-Regular"
			case "narrow":
				size.Columns = 12
			}
			// Only snapshot is available: status must never resolve, register, activate,
			// restore or inspect via the mutation transaction, including after logout.
			services := fontServices{snapshot: bridge.services(t).snapshot}
			if state == "denied" {
				services.snapshot = func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
					return imagefontmac.ProfileStatus{}, errors.New("synthetic permission denial")
				}
			}
			before, selected := fontCacheFiles(t, directory), bridge.status
			var out bytes.Buffer
			err := inspectImageFont(t.Context(), &out, directory, "/dev/ttys001", size, services)
			if state == "foreign gallery" || state == "denied" {
				require.Error(t, err)
				require.Empty(t, out.String())
			} else {
				require.NoError(t, err)
				require.Contains(t, out.String(), "Read-only check")
				require.Contains(t, out.String(), "--inline on")
				switch state {
				case "new session":
					require.Contains(t, out.String(), "No preview cache")
				case "changed text font":
					require.Contains(t, out.String(), "Run openai images inline repair")
				case "selected image font":
					require.Contains(t, out.String(), "cached preview font is selected")
				case "narrow":
					require.Contains(t, out.String(), "at least 17 columns")
				case "escaped font":
					require.NotContains(t, out.String(), "\x1b")
					require.NotContains(t, out.String(), "\a")
				}
			}
			require.Equal(t, before, fontCacheFiles(t, directory))
			require.Equal(t, selected, bridge.status)
		})
	}
}

func TestFontStatusPropagatesWriterErrors(t *testing.T) {
	for _, short := range []bool{false, true} {
		expected := errors.New("closed report writer")
		if short {
			expected = io.ErrShortWrite
		}
		out := writerFunc(func(p []byte) (int, error) {
			if short {
				return len(p) - 1, nil
			}
			return 0, expected
		})
		err := inspectImageFont(t.Context(), out, filepath.Join(t.TempDir(), "missing"), "/dev/ttys001", testFontViewport(), newTestFontBridge().services(t))
		require.ErrorIs(t, err, expected)
	}
}
