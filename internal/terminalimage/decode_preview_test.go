package terminalimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodePreviewMatchesSavedDecoder(t *testing.T) {
	img := testImage(t)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, img))
	reader := base64.NewDecoder(base64.StdEncoding.Strict(), bytes.NewBufferString(base64.StdEncoding.EncodeToString(encoded.Bytes())))
	decoded, err := DecodePreview(t.Context(), reader)
	require.NoError(t, err)
	require.Equal(t, img.Bounds(), decoded.Bounds())
	for _, point := range []image.Point{{0, 0}, {3, 7}} {
		require.Equal(t, img.At(point.X, point.Y), decoded.At(point.X, point.Y))
	}
}

func TestDecodePreviewBoundsAndCancellation(t *testing.T) {
	reader := &previewCountingReader{}
	_, err := DecodePreview(t.Context(), reader)
	require.ErrorContains(t, err, "64 MiB")
	require.EqualValues(t, (64<<20)+1, reader.read)
	require.LessOrEqual(t, reader.largest, 32<<10)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	reader = &previewCountingReader{}
	_, err = DecodePreview(ctx, reader)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, reader.read)
	failure := errors.New("synthetic preview read failure")
	_, err = DecodePreview(t.Context(), previewErrorReader{failure})
	require.ErrorIs(t, err, failure)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 4097, 4096))))
	_, err = DecodePreview(t.Context(), &encoded)
	require.ErrorContains(t, err, "16 megapixel")
}

type previewCountingReader struct{ read, largest int }

func (r *previewCountingReader) Read(p []byte) (int, error) {
	clear(p)
	r.read += len(p)
	r.largest = max(r.largest, len(p))
	return len(p), nil
}

type previewErrorReader struct{ err error }

func (r previewErrorReader) Read([]byte) (int, error) { return 0, r.err }

var _ io.Reader = (*previewCountingReader)(nil)
