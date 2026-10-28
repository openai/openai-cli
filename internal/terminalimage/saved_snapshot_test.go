package terminalimage

import (
	"context"
	"errors"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const snapshotTestMagic = "openai saved snapshot test\n"

var registerSnapshotTestDecoder sync.Once

// This decoder deterministically models an in-place rewrite between inspecting
// an image's dimensions and decoding its pixels, without allocating a huge image.
func TestReadSavedDecodesValidatedSnapshot(t *testing.T) {
	registerSnapshotTestDecoder.Do(func() {
		image.RegisterFormat("png", snapshotTestMagic, decodeSnapshotTestImage, inspectSnapshotTestImage)
	})
	path := filepath.Join(t.TempDir(), "image.png")
	original := snapshotTestMagic + path + "\noriginal"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
	got, err := ReadSaved(context.Background(), path)
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 2, 3), got.Bounds())
	changed, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, snapshotTestMagic+path+"\nreplaced", string(changed))
}

func inspectSnapshotTestImage(reader io.Reader) (image.Config, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return image.Config{}, err
	}
	path, _, ok := strings.Cut(strings.TrimPrefix(string(data), snapshotTestMagic), "\n")
	if !ok {
		return image.Config{}, errors.New("invalid snapshot test image")
	}
	if err := os.WriteFile(path, []byte(snapshotTestMagic+path+"\nreplaced"), 0o600); err != nil {
		return image.Config{}, err
	}
	return image.Config{ColorModel: color.NRGBAModel, Width: 2, Height: 3}, nil
}

func decodeSnapshotTestImage(reader io.Reader) (image.Image, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(string(data), "\noriginal") {
		return nil, errors.New("decoded replaced bytes instead of the validated snapshot")
	}
	return image.NewNRGBA(image.Rect(0, 0, 2, 3)), nil
}
