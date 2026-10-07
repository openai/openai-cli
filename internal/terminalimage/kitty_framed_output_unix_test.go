//go:build darwin || linux

package terminalimage

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKittySmallFramesPreservePNG(t *testing.T) {
	for _, columns := range []int{0, 1, 80, int(^uint(0) >> 1)} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			img := testImage(t)
			var normal, records bytes.Buffer
			require.NoError(t, Write(t.Context(), &normal, img, "kitty", columns))
			require.NoError(t, Write(t.Context(), kittyFramePipeWriter{&records}, img, "kitty", columns))
			var payload strings.Builder
			frames := 0
			require.NoError(t, readKittyFrames(&records, func(data []byte) error {
				require.LessOrEqual(t, len(data), 64)
				require.True(t, bytes.HasPrefix(data, []byte("\x1b_G")))
				require.True(t, bytes.HasSuffix(data, []byte("\x1b\\")))
				header, chunk, ok := strings.Cut(string(data[:len(data)-2]), ";")
				require.True(t, ok)
				if frames == 0 {
					require.Contains(t, header, "a=T")
					require.Contains(t, header, "f=100")
				}
				require.Contains(t, header, "q=2")
				payload.WriteString(chunk)
				frames++
				return nil
			}))
			require.Greater(t, frames, 3)
			var original strings.Builder
			for _, frame := range strings.Split(strings.TrimSuffix(normal.String(), "\x1b\\"), "\x1b\\") {
				_, chunk, ok := strings.Cut(frame, ";")
				require.True(t, ok)
				original.WriteString(chunk)
			}
			decoded, err := base64.StdEncoding.DecodeString(payload.String())
			require.NoError(t, err)
			want, err := base64.StdEncoding.DecodeString(original.String())
			require.NoError(t, err)
			require.Equal(t, want, decoded, "smaller frames must preserve the complete encoded image")
		})
	}
}

func TestKittyFrameRecordsRejectInvalidInput(t *testing.T) {
	for _, data := range [][]byte{{0}, {65}, {255}, {4, 'a', 'b'}, {1}} {
		t.Run(fmt.Sprintf("%x", data), func(t *testing.T) {
			called := false
			err := readKittyFrames(bytes.NewReader(data), func([]byte) error { called = true; return nil })
			require.Error(t, err)
			require.False(t, called, "never send a truncated record to the terminal")
		})
	}
	var encoded bytes.Buffer
	w := kittyFramePipeWriter{&encoded}
	_, err := w.Write(bytes.Repeat([]byte{'a'}, 65))
	require.Error(t, err)
	require.Empty(t, encoded.Bytes())
	_, err = w.Write([]byte("first"))
	require.NoError(t, err)
	_, err = w.Write([]byte("second"))
	require.NoError(t, err)
	cause := errors.New("terminal output closed")
	calls := 0
	err = readKittyFrames(&encoded, func([]byte) error { calls++; return cause })
	require.ErrorIs(t, err, cause)
	require.Equal(t, 1, calls, "stop without writing any following records")
}

func TestKittyFramePipeRetainsShortWrite(t *testing.T) {
	for _, accepted := range []int{0, 1, 2} {
		w := kittyFramePipeWriter{writerFunc(func([]byte) (int, error) { return accepted, nil })}
		n, err := w.Write([]byte("abc"))
		require.Equal(t, max(0, accepted-1), n)
		require.ErrorIs(t, err, io.ErrShortWrite)
	}
}
