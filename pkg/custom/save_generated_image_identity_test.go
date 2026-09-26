//go:build unix

package custom

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the complete saving/preview handoff in a real PTY. The context hook
// deterministically models an external writer after the original bytes have
// reached disk, without depending on a racing goroutine or timing sleeps. This
// uses Unix replacement of an open file; post-close cases also have reader tests.
func TestSavedImagePreviewRejectsChangedResponseTerminal(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("requires actual terminal stdout")
	}
	for _, key := range []string{"CI", "TMUX", "STY", "ZELLIJ", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		t.Setenv(key, "")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "kitty")
	encode := func(pixel color.NRGBA) []byte {
		img := image.NewNRGBA(image.Rect(0, 0, 2, 3))
		img.SetNRGBA(0, 0, pixel)
		var encoded bytes.Buffer
		require.NoError(t, png.Encode(&encoded, img))
		return encoded.Bytes()
	}
	original := encode(color.NRGBA{R: 255, A: 255})
	replacement := encode(color.NRGBA{B: 255, A: 255})
	response := []byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(original) + `"}]}`)
	for _, kind := range []string{"regular replacement", "symlink replacement", "in-place rewrite"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "robot.png")
			retained := filepath.Join(directory, "original.png")
			target := filepath.Join(directory, "replacement.png")
			require.NoError(t, os.WriteFile(target, replacement, 0o600))
			changed := false
			ctx := savedImageChangeContext{Context: t.Context(), check: func() {
				if changed {
					return
				}
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, original) {
					return
				}
				changed = true
				if kind != "in-place rewrite" {
					require.NoError(t, os.Rename(path, retained))
				}
				if kind == "symlink replacement" {
					require.NoError(t, os.Symlink(target, path))
				} else {
					require.NoError(t, os.WriteFile(path, replacement, 0o600))
				}
			}}
			var diagnostic bytes.Buffer
			plan := &imageOutputPlan{directory: directory, name: "robot", inline: "auto", diagnostics: &diagnostic}
			require.NoError(t, plan.save(ctx, response, os.Stdout))
			require.True(t, changed, "the replacement must occur after original bytes were written")
			require.Equal(t, "Inline preview skipped: the saved file changed before it could be displayed. Check the output file.\n", diagnostic.String())
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, replacement, data, "preview must preserve the external replacement")
			if kind != "in-place rewrite" {
				data, err := os.ReadFile(retained)
				require.NoError(t, err)
				require.Equal(t, original, data, "preview must preserve the saved original")
			}
		})
	}
}

type savedImageChangeContext struct {
	context.Context
	check func()
}

func (c savedImageChangeContext) Err() error {
	c.check()
	return c.Context.Err()
}
