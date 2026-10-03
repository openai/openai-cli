package terminalimage

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// kittyTransfers models chunk assembly, not a terminal's visible rendering.
// A new a=T does not replace a pending transfer: subsequent payloads belong to
// that transfer until m=0, including across CLI invocations. See:
// https://sw.kovidgoyal.net/kitty/graphics-protocol/#remote-client
type kittyTransfers struct {
	pending   []byte
	completed [][]byte
}

func (s *kittyTransfers) accept(t *testing.T, wire []byte) {
	t.Helper()
	for _, frame := range strings.Split(strings.TrimSuffix(string(wire), "\x1b\\"), "\x1b\\") {
		header, payload, ok := strings.Cut(frame, ";")
		require.True(t, ok)
		require.True(t, strings.HasPrefix(header, "\x1b_G"))
		require.Contains(t, header, "q=2", "recovery must not inject a reply into shell input")
		require.NotContains(t, header, "a=d", "recovery must preserve earlier images")
		decoded, err := base64.StdEncoding.DecodeString(payload)
		require.NoError(t, err)
		s.pending = append(s.pending, decoded...)
		if !strings.Contains(header, "m=1") {
			s.completed = append(s.completed, bytes.Clone(s.pending))
			s.pending = nil
		}
	}
}

func TestKittyFailedTransferPreservesNextImage(t *testing.T) {
	for _, failure := range []string{"error after chunk", "error before next chunk"} {
		t.Run(failure, func(t *testing.T) {
			var state kittyTransfers
			var previous bytes.Buffer
			require.NoError(t, Write(t.Context(), &previous, testImage(t), "kitty", 50))
			state.accept(t, previous.Bytes())
			require.Len(t, state.completed, 1)
			prior := bytes.Clone(state.completed[0])

			var interrupted bytes.Buffer
			writes := 0
			sinkErr := errors.New("synthetic chunk failure")
			out := writerFunc(func(data []byte) (int, error) {
				writes++
				if failure == "error before next chunk" && writes == 2 {
					return 0, sinkErr
				}
				n, err := interrupted.Write(data)
				if failure == "error after chunk" && writes == 1 {
					err = sinkErr
				}
				return n, err
			})
			err := Write(t.Context(), out, testImage(t), "kitty", 50)
			require.ErrorIs(t, err, sinkErr)
			state.accept(t, interrupted.Bytes())
			assertedPending := len(state.pending)

			img := image.NewNRGBA(image.Rect(0, 0, 5, 3))
			img.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 5, B: 61, A: 128})
			var retry bytes.Buffer
			require.NoError(t, Write(t.Context(), &retry, img, "kitty", 20))
			state.accept(t, retry.Bytes())
			rendered, decodeErr := png.Decode(bytes.NewReader(state.completed[len(state.completed)-1]))
			require.NoError(t, decodeErr, "the next image must not contain the failed upload")
			require.Zero(t, assertedPending, "a closed APC must also complete the chunked transfer")
			require.Empty(t, state.pending)
			require.Equal(t, img.Bounds(), rendered.Bounds())
			for y := 0; y < img.Bounds().Dy(); y++ {
				for x := 0; x < img.Bounds().Dx(); x++ {
					require.Equal(t, img.At(x, y), rendered.At(x, y))
				}
			}
			require.Equal(t, prior, state.completed[0], "earlier image data must remain unchanged")
		})
	}
}
