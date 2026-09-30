package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"
)

func TestKittyPartialFrameCleanup(t *testing.T) {
	for _, frameNumber := range []int{1, 2} {
		for _, location := range []string{"escape", "header", "payload", "terminator"} {
			for _, failure := range []string{"error", "short write", "cancellation"} {
				t.Run(fmt.Sprintf("frame%d/%s/%s", frameNumber, location, failure), func(t *testing.T) {
					terminator := "\x1b\\"
					if location == "escape" || location == "terminator" {
						terminator = "\\"
					}
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
							cleanup := []string{terminator, "\x1b_Gq=2,m=0;\x1b\\"}
							require.LessOrEqual(t, writes-frameNumber, len(cleanup))
							require.Equal(t, cleanup[writes-frameNumber-1], string(data), "close APC before finishing the upload")
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
						expected.WriteString(terminator + "\x1b_Gq=2,m=0;\x1b\\")
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
					require.Equal(t, frameNumber+2, writes)
					require.Equal(t, expected.Bytes(), output.Bytes())
				})
			}
		}
	}
}

func TestKittyCleanupRetainsErrors(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		payloadErr, cleanupErr := errors.New("synthetic Kitty write failure"), errors.New("synthetic ST failure")
		finishErr := errors.New("synthetic final-chunk failure")
		writes := 0
		out := writerFunc(func(data []byte) (int, error) {
			writes++
			if writes == 1 {
				if canceled {
					cancel()
				}
				return 3, payloadErr
			}
			if writes == 2 {
				require.Equal(t, "\x1b\\", string(data))
				return 0, cleanupErr
			}
			require.Equal(t, "\x1b_Gq=2,m=0;\x1b\\", string(data))
			return 0, finishErr
		})
		err := Write(ctx, out, testImage(t), "kitty", 50)
		require.ErrorIs(t, err, payloadErr)
		require.ErrorIs(t, err, cleanupErr)
		require.ErrorIs(t, err, finishErr)
		if canceled {
			require.ErrorIs(t, err, context.Canceled)
		}
		require.Equal(t, 3, writes, "attempt each cleanup write only once even if it fails")
	}
}

func TestKittyClosedFramesFinishPendingTransfer(t *testing.T) {
	var success bytes.Buffer
	require.NoError(t, Write(t.Context(), &success, testImage(t), "kitty", 50))
	lastFrame := strings.Count(success.String(), "\x1b_G")
	require.Greater(t, lastFrame, 2)
	for _, frameNumber := range []int{1, 2, lastFrame} {
		for _, accepted := range []bool{false, true} {
			for _, canceled := range []bool{false, true} {
				ctx, cancel := context.WithCancel(t.Context())
				sentinel := errors.New("synthetic Kitty frame failure")
				writes := 0
				out := writerFunc(func(data []byte) (int, error) {
					writes++
					if writes > frameNumber {
						require.Equal(t, "\x1b_Gq=2,m=0;\x1b\\", string(data), "closed APC needs only the final chunk")
						return len(data), nil
					}
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
				wantWrites := frameNumber
				if (frameNumber > 1 || accepted) && !(frameNumber == lastFrame && accepted) {
					wantWrites++
				}
				require.Equal(t, wantWrites, writes, "finish only an upload that may still be pending")
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
