package transformers

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentsImageEscapesValidateWithoutChangingSemantics(t *testing.T) {
	for _, test := range []struct {
		encoded string
		count   int
	}{
		{"QUJDRA==", 8}, {"QUJDRA%3D%3d", 8}, {"QUJD%0aRA%3D%3D", 9},
		{"", 0}, {"QUJDRA%", 0}, {"QUJDRA%3", 0}, {"QUJDRA%XX", 0},
		{"QUJDRA%00%3D", 0}, {"QUJDRA%253D%253D", 0},
	} {
		t.Run(test.encoded, func(t *testing.T) {
			count, err := agentsImageBase64Length(t.Context(), test.encoded)
			require.NoError(t, err)
			require.Equal(t, test.count, count)
		})
	}
}

func TestAgentsImageEscapesCancelBeforeRemainingPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: 4}
	encoded := strings.Repeat("%51%55%4a%44", 1<<18)
	reader := &agentsImageDataReader{ctx: controlled, encoded: encoded}
	_, err := io.Copy(io.Discard, reader)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, reader.offset, len(encoded)/2)
	require.Equal(t, reader.offset/3, reader.characters)
}

func TestAgentsImageEscapesAvoidPayloadSizedAllocations(t *testing.T) {
	encoded := strings.Repeat("%51%55%4a%44", 1<<18)
	result := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			count, err := agentsImageBase64Length(context.Background(), encoded)
			if err != nil || count != 1<<20 {
				b.Fatalf("count=%d err=%v", count, err)
			}
		}
	})
	// The decoder uses fixed buffers. A complete unescaped copy costs 1 MiB.
	require.Less(t, result.AllocedBytesPerOp(), int64(64*1024))
}
