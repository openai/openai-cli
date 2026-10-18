package custom

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/apiform"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/stretchr/testify/require"
)

const multipartTestTimeout = 5 * time.Second

func TestMultipartRequestBodyDoesNotCloseUnownedReaders(t *testing.T) {
	stdinLikeReader := &recordingReadCloser{reader: strings.NewReader("stdin payload")}
	embedded, err := embedFiles(
		map[string]any{"file": "@-"},
		EmbedIOReader,
		&onceStdinReader{stdinReader: stdinLikeReader},
	)
	require.NoError(t, err)
	var body io.ReadCloser
	_, err = requestconfig.MultipartRequestOptions(embedded.(map[string]any), apiform.FormatBrackets, func(prepared io.Closer) {
		body = prepared.(io.ReadCloser)
	})
	require.NoError(t, err)
	require.NotNil(t, body)

	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Contains(t, string(data), "stdin payload")
	require.NoError(t, body.Close())
	require.Zero(t, stdinLikeReader.closeCount.Load())
}

func TestEmbedFilesClosesPartialUploadsOnError(t *testing.T) {
	upload := &recordingReadCloser{reader: strings.NewReader("already opened")}
	missingPath := requestconfig.FilePathValue(t.TempDir() + "/missing.bin")

	_, err := embedFiles([]any{
		requestconfig.FileUpload{Reader: upload, FileName: "opened.bin"},
		missingPath,
	}, EmbedIOReader, &onceStdinReader{})
	require.Error(t, err)
	require.Equal(t, int32(1), upload.closeCount.Load())
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type recordingReadCloser struct {
	reader     io.Reader
	closeErr   error
	closeCount atomic.Int32
}

func (r *recordingReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *recordingReadCloser) Close() error {
	r.closeCount.Add(1)
	return r.closeErr
}

type closeUnblocksReader struct {
	err         error
	readStarted chan struct{}
	closed      chan struct{}
	startOnce   sync.Once
	closeOnce   sync.Once
	closeCount  atomic.Int32
}

func newCloseUnblocksReader(err error) *closeUnblocksReader {
	return &closeUnblocksReader{
		err:         err,
		readStarted: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (r *closeUnblocksReader) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.readStarted) })
	<-r.closed
	return 0, r.err
}

func (r *closeUnblocksReader) Close() error {
	r.closeCount.Add(1)
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}
