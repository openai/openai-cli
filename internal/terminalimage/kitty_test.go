package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"
)

func TestKittyPartialFrameCleanup(t *testing.T) {
	for _, frameNumber := range []int{1, 2} {
		for _, location := range []string{"escape", "header", "payload", "terminator"} {
			for _, failure := range []string{"error", "short write", "cancellation"} {
				t.Run(fmt.Sprintf("frame%d/%s/%s", frameNumber, location, failure), func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					sentinel := errors.New("synthetic partial Kitty write")
					var output, expected bytes.Buffer
					writes := 0
					out := writerFunc(func(data []byte) (int, error) {
						writes++
						if writes < frameNumber {
							expected.Write(data)
							return output.Write(data)
						}
						if writes > frameNumber {
							require.Equal(t, "\x1b\\", string(data), "cleanup emits only ST")
							return output.Write(data)
						}
						accepted := 1
						switch location {
						case "header":
							accepted = 3
						case "payload":
							accepted = bytes.IndexByte(data, ';') + 17
						case "terminator":
							accepted = len(data) - 1
						}
						expected.Write(data[:accepted])
						expected.WriteString("\x1b\\")
						n, err := output.Write(data[:accepted])
						if failure == "error" {
							err = sentinel
						} else if failure == "cancellation" {
							cancel()
						}
						return n, err
					})
					err := Write(ctx, out, testImage(t), "kitty", 50)
					switch failure {
					case "error":
						require.ErrorIs(t, err, sentinel)
					case "short write":
						require.ErrorIs(t, err, io.ErrShortWrite)
					case "cancellation":
						require.ErrorIs(t, err, context.Canceled)
					}
					require.Equal(t, frameNumber+1, writes)
					require.Equal(t, expected.Bytes(), output.Bytes())
				})
			}
		}
	}
}

func TestKittyCleanupRetainsBothErrors(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		payloadErr, cleanupErr := errors.New("synthetic Kitty write failure"), errors.New("synthetic ST failure")
		writes := 0
		out := writerFunc(func(data []byte) (int, error) {
			writes++
			if writes == 1 {
				if canceled {
					cancel()
				}
				return 3, payloadErr
			}
			require.Equal(t, "\x1b\\", string(data))
			return 0, cleanupErr
		})
		err := Write(ctx, out, testImage(t), "kitty", 50)
		require.ErrorIs(t, err, payloadErr)
		require.ErrorIs(t, err, cleanupErr)
		if canceled {
			require.ErrorIs(t, err, context.Canceled)
		}
		require.Equal(t, 2, writes, "attempt cleanup once even if it fails")
	}
}

func TestKittyClosedFramesNeedNoCleanup(t *testing.T) {
	for _, frameNumber := range []int{1, 2} {
		for _, accepted := range []bool{false, true} {
			for _, canceled := range []bool{false, true} {
				ctx, cancel := context.WithCancel(t.Context())
				sentinel := errors.New("synthetic Kitty frame failure")
				writes := 0
				out := writerFunc(func(data []byte) (int, error) {
					writes++
					n := len(data)
					if writes == frameNumber {
						if !accepted {
							n = 0
						}
						if canceled {
							cancel()
							return n, nil
						}
						return n, sentinel
					}
					return n, nil
				})
				err := Write(ctx, out, testImage(t), "kitty", 50)
				cancel()
				if canceled {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorIs(t, err, sentinel)
				}
				require.Equal(t, frameNumber, writes, "zero acceptance or a complete ST cannot leave a partial frame")
			}
		}
	}
}

func TestKittySuccessMatchesOriginalEncoder(t *testing.T) {
	for _, columns := range []int{0, 50} {
		img := testImage(t)
		var got, want bytes.Buffer
		require.NoError(t, kitty.EncodeGraphics(&want, img, &kitty.Options{Action: kitty.TransmitAndPut, Transmission: kitty.Direct, Format: kitty.PNG, Quite: 2, Columns: columns, Chunk: true}))
		require.NoError(t, Write(t.Context(), &got, img, "kitty", columns))
		require.Equal(t, want.Bytes(), got.Bytes())
	}
}
