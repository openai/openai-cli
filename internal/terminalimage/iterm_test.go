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
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/iterm2"
	"github.com/stretchr/testify/require"
)

func TestITermWritesBoundedChunks(t *testing.T) {
	var output bytes.Buffer
	largest, writes := 0, 0
	out := writerFunc(func(data []byte) (int, error) {
		largest = max(largest, len(data))
		writes++
		return output.Write(data)
	})
	require.NoError(t, Write(t.Context(), out, testImage(t), "iterm", 50))
	require.LessOrEqual(t, largest, 4096, "base64 payload must stream through bounded writes")
	require.Greater(t, writes, 3)
	require.True(t, bytes.HasPrefix(output.Bytes(), []byte("\x1b]1337;File=")))
	require.True(t, bytes.HasSuffix(output.Bytes(), []byte("\x07")))
}

func TestITermOutputMatchesOriginalEncoder(t *testing.T) {
	offset := image.NewNRGBA(image.Rect(7, 9, 23, 27))
	for y := 9; y < 27; y++ {
		for x := 7; x < 23; x++ {
			offset.SetNRGBA(x, y, color.NRGBA{uint8(x * 17), uint8(y * 19), 233, [...]uint8{0, 1, 128, 255}[(x+y)%4]})
		}
	}
	for _, img := range []image.Image{testImage(t), offset} {
		var encoded bytes.Buffer
		require.NoError(t, png.Encode(&encoded, img))
		for _, columns := range []int{-1, 0, 1, 50} {
			width := iterm2.Auto
			if columns > 0 {
				width = iterm2.Cells(columns)
			}
			want := ansi.ITerm2(iterm2.File{Inline: true, Width: width, Height: iterm2.Auto, Content: []byte(base64.StdEncoding.EncodeToString(encoded.Bytes()))})
			var got bytes.Buffer
			require.NoError(t, Write(t.Context(), &got, img, "iterm", columns))
			require.Equal(t, want, got.String())
		}
	}
}

func TestITermCancellationTerminatesPartialOSC(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var output bytes.Buffer
	writes := 0
	out := writerFunc(func(data []byte) (int, error) {
		writes++
		if writes == 3 {
			cancel()
		}
		return output.Write(data)
	})
	require.ErrorIs(t, Write(ctx, out, testImage(t), "iterm", 50), context.Canceled)
	require.Equal(t, 4, writes, "cancel after two payload chunks, then send only the OSC terminator")
	require.True(t, bytes.HasSuffix(output.Bytes(), []byte("\x1b\\")))
	before := output.Len()
	require.ErrorIs(t, Write(ctx, out, testImage(t), "iterm", 50), context.Canceled)
	require.Equal(t, before, output.Len(), "an already-canceled request must not emit protocol bytes")
}

func TestITermWriterFailureTerminatesPartialOSC(t *testing.T) {
	for _, failWrite := range []int{1, 2, 3} {
		for _, partial := range []bool{false, true} {
			var output bytes.Buffer
			writes := 0
			sentinel := errors.New("synthetic iTerm output failure")
			out := writerFunc(func(data []byte) (int, error) {
				writes++
				if writes == failWrite {
					if partial {
						n, _ := output.Write(data[:1])
						return n, nil
					}
					return 0, sentinel
				}
				return output.Write(data)
			})
			err := Write(t.Context(), out, testImage(t), "iterm", 50)
			if partial {
				require.ErrorIs(t, err, io.ErrShortWrite)
			} else {
				require.ErrorIs(t, err, sentinel)
			}
			if failWrite == 1 && !partial {
				require.Empty(t, output.Bytes())
				require.Equal(t, 1, writes)
			} else {
				require.True(t, bytes.HasSuffix(output.Bytes(), []byte("\x1b\\")))
				require.Equal(t, failWrite+1, writes, "one best-effort terminator after the failed write")
			}
		}
	}
}

func TestITermCleanupRetainsBothErrors(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		payloadErr := errors.New("synthetic payload failure")
		cleanupErr := errors.New("synthetic cleanup failure")
		writes := 0
		out := writerFunc(func(data []byte) (int, error) {
			writes++
			switch writes {
			case 1:
				return len(data), nil
			case 2:
				if canceled {
					cancel()
					return len(data), nil
				}
				return 0, payloadErr
			default:
				require.Equal(t, "\x1b\\", string(data), "cleanup must send only ST")
				return 0, cleanupErr
			}
		})
		err := Write(ctx, out, testImage(t), "iterm", 50)
		if canceled {
			payloadErr = context.Canceled
		}
		require.ErrorIs(t, err, payloadErr)
		require.ErrorIs(t, err, cleanupErr)
		require.Equal(t, 3, writes, "cleanup failure must not cause retries")
	}
}

func TestITermFinalTerminatorFailure(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var output bytes.Buffer
		cleanup := 0
		out := writerFunc(func(data []byte) (int, error) {
			if string(data) == "\x1b\\" {
				cleanup++
			}
			if string(data) == "\x07" {
				cancel()
				if !accepted {
					return 0, context.Canceled
				}
			}
			return output.Write(data)
		})
		require.ErrorIs(t, Write(ctx, out, testImage(t), "iterm", 50), context.Canceled)
		if accepted {
			require.Zero(t, cleanup, "an accepted final BEL already closes the OSC")
			require.True(t, bytes.HasSuffix(output.Bytes(), []byte("\x07")))
		} else {
			require.Equal(t, 1, cleanup)
			require.True(t, bytes.HasSuffix(output.Bytes(), []byte("\x1b\\")))
		}
	}
}

func TestITermBase64PaddingMatchesOriginal(t *testing.T) {
	for _, size := range []int{1, 2, 3, 4, 767, 768, 769, 4096} {
		payload := bytes.Repeat([]byte{171}, size)
		var got bytes.Buffer
		require.NoError(t, writeITermPayload(t.Context(), &got, payload, 50))
		want := ansi.ITerm2(iterm2.File{Inline: true, Width: "50", Height: iterm2.Auto, Content: []byte(base64.StdEncoding.EncodeToString(payload))})
		require.Equal(t, want, got.String())
	}
}
