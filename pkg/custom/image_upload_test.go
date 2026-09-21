package custom

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageMultipartScalarInspectionPreservesFileMetadataAndOwnership(t *testing.T) {
	source := &recordingReadCloser{reader: strings.NewReader("A purple sky\r\n")}
	original := fileUpload{Reader: source, filename: "prompt.txt", contentType: "text/plain"}
	inspected, replay, err := inspectImageMultipartSetting(t.Context(), "prompt", original)
	require.NoError(t, err)
	require.Equal(t, "A purple sky\r\n", inspected)
	upload := replay.(fileUpload)
	require.Equal(t, original.filename, upload.filename)
	require.Equal(t, original.contentType, upload.contentType)
	require.EqualValues(t, len("A purple sky\r\n"), upload.size)
	require.True(t, upload.knownSize)
	contents, err := io.ReadAll(upload)
	require.NoError(t, err)
	require.Equal(t, "A purple sky\r\n", string(contents))
	require.NoError(t, closeFileUploads(replay))
	require.EqualValues(t, 1, source.closeCount.Load())
}

func TestImageMultipartScalarInspectionReadFailureIsPrivateAndClosesSource(t *testing.T) {
	source := &recordingReadCloser{reader: errorReader{err: errors.New("synthetic-private-file-contents")}}
	_, replay, err := inspectImageMultipartSetting(t.Context(), "response_format", fileUpload{Reader: source})
	require.ErrorContains(t, err, "--response-format")
	require.NotContains(t, err.Error(), "synthetic-private")
	require.NoError(t, closeFileUploads(replay))
	require.EqualValues(t, 1, source.closeCount.Load())
}

func TestImageMultipartScalarInspectionCancellationReleasesOwnedRead(t *testing.T) {
	source := &closeUnblocksReader{readStarted: make(chan struct{}), closed: make(chan struct{}), err: errors.New("closed")}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := inspectImageMultipartSetting(ctx, "prompt", fileUpload{Reader: source})
		done <- err
	}()
	select {
	case <-source.readStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("scalar inspection did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not release scalar source")
	}
}
