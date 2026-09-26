package terminalimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testImage(t *testing.T) image.Image {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 80, 60))
	_, err := rand.New(rand.NewSource(1)).Read(img.Pix)
	require.NoError(t, err)
	return img
}

func TestWritePreservesImageAndFitsWidth(t *testing.T) {
	img := testImage(t)
	for _, protocol := range []string{"kitty", "iterm"} {
		t.Run(protocol, func(t *testing.T) {
			var output bytes.Buffer
			require.NoError(t, Write(t.Context(), &output, img, protocol, 50))
			var payload string
			if protocol == "kitty" {
				frames := strings.Split(strings.TrimSuffix(output.String(), "\x1b\\"), "\x1b\\")
				require.Greater(t, len(frames), 1, "fixture should exercise chunking")
				for i, frame := range frames {
					header, chunk, ok := strings.Cut(frame, ";")
					require.True(t, ok)
					require.True(t, strings.HasPrefix(header, "\x1b_G"))
					require.Contains(t, header, "q=2")
					if i == 0 {
						require.Contains(t, header, "a=T")
						require.Contains(t, header, "f=100")
						require.Contains(t, header, "c=50")
						require.NotContains(t, header, "r=")
					}
					if i == len(frames)-1 {
						require.Contains(t, header, "m=0")
					} else {
						require.Contains(t, header, "m=1")
					}
					require.LessOrEqual(t, len(chunk), 4096)
					payload += chunk
				}
			} else {
				header, data, ok := strings.Cut(output.String(), ":")
				require.True(t, ok)
				require.Equal(t, "\x1b]1337;File=width=50;height=auto;inline=1", header)
				require.True(t, strings.HasSuffix(data, "\x07"))
				payload = strings.TrimSuffix(data, "\x07")
			}
			decoded, err := base64.StdEncoding.DecodeString(payload)
			require.NoError(t, err)
			rendered, err := png.Decode(bytes.NewReader(decoded))
			require.NoError(t, err)
			require.Equal(t, img.Bounds(), rendered.Bounds())
			require.Equal(t, img.At(12, 15), rendered.At(12, 15))
		})
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(data []byte) (int, error) { return f(data) }

func TestWriteErrorsAndCancellation(t *testing.T) {
	img := testImage(t)
	for _, protocol := range []string{"kitty", "iterm", "blocks"} {
		t.Run(protocol, func(t *testing.T) {
			sinkErr := errors.New("synthetic output failure")
			require.ErrorIs(t, Write(t.Context(), writerFunc(func([]byte) (int, error) {
				return 0, sinkErr
			}), img, protocol, 50), sinkErr)
			require.ErrorIs(t, Write(t.Context(), writerFunc(func([]byte) (int, error) {
				return 0, nil
			}), img, protocol, 50), io.ErrShortWrite)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			writes := 0
			writer := writerFunc(func(data []byte) (int, error) {
				writes++
				cancel()
				return len(data), nil
			})
			require.ErrorIs(t, Write(ctx, writer, img, protocol, 50), context.Canceled)
			require.Equal(t, 1, writes)
			require.ErrorIs(t, Write(ctx, writer, img, protocol, 50), context.Canceled)
			require.Equal(t, 1, writes, "canceled context must not write")
		})
	}
	var output bytes.Buffer
	require.Error(t, Write(t.Context(), &output, nil, "kitty", 50))
	require.Error(t, Write(t.Context(), &output, img, "unknown", 50))
	require.Empty(t, output.String())
}

func TestWriteColorBlocks(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for x := 0; x < 2; x++ {
		img.Set(x, 0, color.RGBA{R: 255, A: 255})
		img.Set(x, 1, color.RGBA{B: 255, A: 255})
	}
	var output bytes.Buffer
	require.NoError(t, Write(t.Context(), &output, img, "blocks", 2))
	require.Equal(t, strings.Repeat("\x1b[38;5;196;48;5;21m▀", 2)+"\x1b[0m", output.String())
}
