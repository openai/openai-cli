package apiform

import (
	"bytes"
	"io"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
)

type panicOnRead struct{}

func (*panicOnRead) Read([]byte) (int, error) {
	panic("Read called on typed nil receiver")
}

func TestMarshalTreatsTypedNilReaderAsEmptyField(t *testing.T) {
	t.Parallel()

	var concrete *panicOnRead
	var reader io.Reader = concrete
	tests := map[string]any{
		"concrete pointer in any map": map[string]any{"file": concrete},
		"pointer in reader map":       map[string]io.Reader{"file": reader},
		"nil reader interface":        map[string]io.Reader{"file": nil},
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			writer := multipart.NewWriter(&buf)
			require.NoError(t, writer.SetBoundary("xxx"))

			require.NotPanics(t, func() {
				require.NoError(t, Marshal(value, writer))
				require.NoError(t, writer.Close())
			})

			require.Equal(t,
				"--xxx\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\n\r\n--xxx--\r\n",
				buf.String(),
			)
		})
	}
}
