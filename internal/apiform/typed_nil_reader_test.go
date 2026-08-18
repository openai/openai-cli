package apiform

import (
	"bytes"
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

	var reader *panicOnRead
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.SetBoundary("xxx"))

	require.NotPanics(t, func() {
		require.NoError(t, Marshal(map[string]any{"file": reader}, writer))
		require.NoError(t, writer.Close())
	})

	require.Equal(t,
		"--xxx\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\n\r\n--xxx--\r\n",
		buf.String(),
	)
}
