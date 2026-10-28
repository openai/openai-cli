package terminalimage

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadSavedDecodesSupportedFormats(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var pngData, jpegData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, img))
	require.NoError(t, jpeg.Encode(&jpegData, img, &jpeg.Options{Quality: 95}))
	// Synthetic 2x2 red PNG encoded locally with ffmpeg 4.4.8/libwebp, lossless.
	webpData, err := base64.StdEncoding.DecodeString("UklGRhwAAABXRUJQVlA4TA8AAAAvAUAAAAcQ/Y/+ByKi/wEA")
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		data []byte
	}{{"png", pngData.Bytes()}, {"jpeg", jpegData.Bytes()}, {"webp", webpData}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "image."+tc.name)
			require.NoError(t, os.WriteFile(path, tc.data, 0600))
			decoded, err := ReadSaved(t.Context(), path)
			require.NoError(t, err)
			require.Equal(t, image.Rect(0, 0, 2, 2), decoded.Bounds())
			verified, err := ReadSavedMatching(t.Context(), path, sha256.Sum256(tc.data))
			require.NoError(t, err)
			require.Equal(t, decoded, verified)
			original, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, tc.data, original)
		})
	}
}

func TestReadSavedRejectsAnimatedGIF(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var encoded bytes.Buffer
	require.NoError(t, gif.EncodeAll(&encoded, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{1, 1}}))
	path := filepath.Join(t.TempDir(), "animated.gif")
	require.NoError(t, os.WriteFile(path, encoded.Bytes(), 0600))
	_, err := ReadSaved(t.Context(), path)
	require.Error(t, err)
	// Importing image/gif above globally registers it with image.Decode. The
	// preview decoder must still reject unsupported formats explicitly.
}
