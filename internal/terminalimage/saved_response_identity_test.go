package terminalimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/stretchr/testify/require"
)

func TestReadSavedMatchingRejectsChangedResponse(t *testing.T) {
	encode := func(pixel color.NRGBA) []byte {
		img := image.NewNRGBA(image.Rect(0, 0, 2, 3))
		img.SetNRGBA(0, 0, pixel)
		var encoded bytes.Buffer
		require.NoError(t, png.Encode(&encoded, img))
		return encoded.Bytes()
	}
	original := encode(color.NRGBA{R: 255, A: 255})
	replacement := encode(color.NRGBA{B: 255, A: 255})
	// Equal lengths and restored timestamps ensure metadata checks alone cannot
	// catch the in-place rewrite. Trailing bytes are valid for PNG decoding.
	size := max(len(original), len(replacement))
	original = append(original, make([]byte, size-len(original))...)
	replacement = append(replacement, make([]byte, size-len(replacement))...)
	response := []byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(original) + `"}]}`)
	for _, kind := range []string{"unchanged", "same-content replacement", "regular replacement", "symlink replacement", "in-place rewrite"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "symlink replacement" && runtime.GOOS == "windows" {
				t.Skip("creating Windows symlinks can require elevated privileges")
			}
			directory := t.TempDir()
			saved, err := imageoutput.SaveResponse(t.Context(), response, directory, "robot")
			require.NoError(t, err)
			require.Len(t, saved, 1)
			path := saved[0].Path
			before, err := os.Stat(path)
			require.NoError(t, err)
			retained := filepath.Join(directory, "original.png")
			want := replacement
			if kind == "unchanged" || kind == "same-content replacement" {
				want = original
			}
			switch kind {
			case "same-content replacement", "regular replacement", "symlink replacement":
				require.NoError(t, os.Rename(path, retained))
				if kind == "symlink replacement" {
					target := filepath.Join(t.TempDir(), "replacement.png")
					require.NoError(t, os.WriteFile(target, want, 0o600))
					require.NoError(t, os.Symlink(target, path))
				} else {
					require.NoError(t, os.WriteFile(path, want, 0o600))
				}
			case "in-place rewrite":
				require.NoError(t, os.WriteFile(path, want, 0o600))
				require.NoError(t, os.Chtimes(path, before.ModTime(), before.ModTime()))
				after, err := os.Stat(path)
				require.NoError(t, err)
				require.True(t, os.SameFile(before, after))
				require.Equal(t, before.Size(), after.Size())
				require.Equal(t, before.ModTime(), after.ModTime())
			}
			decoded, err := ReadSavedMatching(t.Context(), path, saved[0].SHA256)
			if bytes.Equal(want, original) {
				require.NoError(t, err)
				require.Equal(t, color.NRGBA{R: 255, A: 255}, color.NRGBAModel.Convert(decoded.At(0, 0)))
			} else {
				require.ErrorIs(t, err, ErrSavedImageChanged)
				require.Nil(t, decoded)
			}
			// Explicit local previews intentionally read the selected path. This
			// proves each replacement is a valid image, not a decoder failure.
			_, err = ReadSaved(t.Context(), path)
			require.NoError(t, err)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, want, data)
			if kind == "same-content replacement" || kind == "regular replacement" || kind == "symlink replacement" {
				data, err := os.ReadFile(retained)
				require.NoError(t, err)
				require.Equal(t, original, data)
			}
		})
	}
}

func TestReadSavedMatchingVerifiesBeforeDecoding(t *testing.T) {
	registerSnapshotTestDecoder.Do(func() {
		image.RegisterFormat("png", snapshotTestMagic, decodeSnapshotTestImage, inspectSnapshotTestImage)
	})
	path := filepath.Join(t.TempDir(), "image.png")
	data := []byte(snapshotTestMagic + path + "\noriginal")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	_, err := ReadSavedMatching(t.Context(), path, sha256.Sum256([]byte("different saved response")))
	require.ErrorIs(t, err, ErrSavedImageChanged)
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, unchanged, "the decoder must not run on mismatched bytes")
	decoded, err := ReadSavedMatching(t.Context(), path, sha256.Sum256(data))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 2, 3), decoded.Bounds(), "decoding must reuse the verified snapshot even when its source changes")
	changed, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, snapshotTestMagic+path+"\nreplaced", string(changed))
}

func TestReadSavedMatchingCancellationWhileReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.png")
	data := bytes.Repeat([]byte("synthetic snapshot"), 128<<10)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	checks := 0
	checking := savedIdentityCancelContext{Context: ctx, check: func() {
		checks++
		if checks == 6 {
			cancel()
		}
	}}
	_, err := ReadSavedMatching(checking, path, sha256.Sum256(data))
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 6, checks, "cancellation should interrupt incremental snapshot reading")
}

func TestSavedImageReadChecksCancellationAfterBoundedWork(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 2<<20)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	checks := 0
	checking := savedIdentityCancelContext{Context: ctx, check: func() {
		checks++
		if checks == 2 {
			cancel()
		}
	}}
	reader := contextReader{checking, bytes.NewReader(data)}
	buffer := make([]byte, len(data))
	n, err := reader.Read(buffer)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 32<<10, n, "large caller buffers must not defer cancellation for an entire snapshot")
	require.Equal(t, data[:n], buffer[:n])
}

type savedIdentityCancelContext struct {
	context.Context
	check func()
}

func (c savedIdentityCancelContext) Err() error {
	c.check()
	return c.Context.Err()
}
