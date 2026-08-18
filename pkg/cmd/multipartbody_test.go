package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const multipartTestTimeout = 5 * time.Second

func TestMultipartRequestBodyStreamsWithBackpressure(t *testing.T) {
	const uploadSize = int64(64 << 20)
	source := &countingReader{remaining: uploadSize}
	upload := &recordingReadCloser{reader: source}
	body := newMultipartRequestBody(map[string]any{
		"file": fileUpload{
			Reader:      upload,
			filename:    "large.bin",
			contentType: "application/octet-stream",
		},
	}, apiform.FormatBrackets)

	// Construction is fully lazy: it neither reads the upload nor starts a
	// producer that can be left blocked before an HTTP request consumes it.
	require.Zero(t, source.bytesRead.Load())

	mediaType, params, err := mime.ParseMediaType(body.ContentType())
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)

	part, err := multipart.NewReader(body, params["boundary"]).NextPart()
	require.NoError(t, err)
	require.Equal(t, "file", part.FormName())
	require.Equal(t, "large.bin", part.FileName())

	buf := make([]byte, 1)
	n, err := part.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Positive(t, source.bytesRead.Load())
	require.Less(t, source.bytesRead.Load(), uploadSize,
		"the producer must not read the complete upload ahead of the consumer")

	require.NoError(t, body.Close())
	waitForMultipartBody(t, body)
	require.Equal(t, int32(1), upload.closeCount.Load())
}

func TestMultipartRequestBodyPropagatesProducerErrors(t *testing.T) {
	readErr := errors.New("upload read failed")
	upload := &recordingReadCloser{reader: errorReader{err: readErr}}
	body := newMultipartRequestBody(map[string]any{
		"file": fileUpload{Reader: upload, filename: "broken.bin"},
	}, apiform.FormatBrackets)
	_, params, err := mime.ParseMediaType(body.ContentType())
	require.NoError(t, err)

	data, err := io.ReadAll(body)
	require.ErrorIs(t, err, readErr)
	require.NotContains(t, string(data), "--"+params["boundary"]+"--",
		"a failed file part must not be followed by a successful terminal boundary")
	waitForMultipartBody(t, body)
	require.Equal(t, int32(1), upload.closeCount.Load())
	require.NoError(t, body.Close())
}

func TestMultipartRequestBodyJoinsCleanupErrorAfterSourceError(t *testing.T) {
	readErr := errors.New("upload read failed")
	closeErr := errors.New("upload close failed")
	upload := &recordingReadCloser{reader: errorReader{err: readErr}, closeErr: closeErr}
	body := newMultipartRequestBody(map[string]any{
		"file": fileUpload{Reader: upload, filename: "broken.bin"},
	}, apiform.FormatBrackets)

	_, err := io.ReadAll(body)
	require.ErrorIs(t, err, readErr)
	require.ErrorIs(t, err, closeErr)
	waitForMultipartBody(t, body)
	require.Equal(t, int32(1), upload.closeCount.Load())
}

func TestMultipartRequestBodySourceErrorCannotBeParsedAsCompleteUpload(t *testing.T) {
	readErr := errors.New("upload read failed")
	parseErrCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parseErrCh <- r.ParseMultipartForm(1 << 20)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"file_123","object":"file"}`)
	}))
	t.Cleanup(server.Close)

	options, err := multipartRequestOptions(map[string]any{
		"file": fileUpload{Reader: errorReader{err: readErr}, filename: "broken.bin"},
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
	require.ErrorIs(t, err, readErr)
	select {
	case parseErr := <-parseErrCh:
		require.Error(t, parseErr, "the receiver must reject a file part without a terminal boundary")
	case <-time.After(multipartTestTimeout):
		t.Fatal("server did not receive the failed streaming request")
	}
}

func TestMultipartRequestBodyClosesOpenedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.bin")
	require.NoError(t, os.WriteFile(path, []byte("payload"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	file := uploadSourceFile(t, upload)
	body := newMultipartRequestBody(map[string]any{"file": upload}, apiform.FormatBrackets)

	_, err = io.ReadAll(body)
	require.NoError(t, err)
	waitForMultipartBody(t, body)
	_, err = file.Stat()
	require.ErrorIs(t, err, os.ErrClosed)
	require.NoError(t, body.Close())
}

func TestMultipartRequestBodyClosesEveryUpload(t *testing.T) {
	firstCloseErr := errors.New("first close failed")
	secondCloseErr := errors.New("second close failed")
	first := &recordingReadCloser{reader: strings.NewReader("first"), closeErr: firstCloseErr}
	second := &recordingReadCloser{reader: strings.NewReader("second"), closeErr: secondCloseErr}
	third := &recordingReadCloser{reader: strings.NewReader("third")}
	body := newMultipartRequestBody(map[string]any{
		"files": []any{
			fileUpload{Reader: first, filename: "first.txt"},
			map[string]any{"nested": fileUpload{Reader: second, filename: "second.txt"}},
		},
		"typed": map[string]fileUpload{
			"nested": {Reader: third, filename: "third.txt"},
		},
	}, apiform.FormatBrackets)

	_, err := io.ReadAll(body)
	require.ErrorIs(t, err, firstCloseErr)
	require.ErrorIs(t, err, secondCloseErr)
	waitForMultipartBody(t, body)
	require.Equal(t, int32(1), first.closeCount.Load())
	require.Equal(t, int32(1), second.closeCount.Load())
	require.Equal(t, int32(1), third.closeCount.Load())

	err = body.Close()
	require.ErrorIs(t, err, firstCloseErr)
	require.ErrorIs(t, err, secondCloseErr)
	require.Equal(t, int32(1), first.closeCount.Load())
	require.Equal(t, int32(1), second.closeCount.Load())
	require.Equal(t, int32(1), third.closeCount.Load())
}

func TestMultipartRequestBodyCloseCancelsEncoding(t *testing.T) {
	readErr := errors.New("upload closed while reading")
	upload := newCloseUnblocksReader(readErr)
	body := newMultipartRequestBody(map[string]any{
		"file": fileUpload{Reader: upload, filename: "blocked.bin"},
	}, apiform.FormatBrackets)

	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, body)
		readDone <- err
	}()

	select {
	case <-upload.readStarted:
	case <-time.After(multipartTestTimeout):
		t.Fatal("multipart encoder did not start reading the upload")
	}

	require.NoError(t, body.Close())
	select {
	case err := <-readDone:
		require.Error(t, err)
	case <-time.After(multipartTestTimeout):
		t.Fatal("request body read did not stop after Close")
	}
	waitForMultipartBody(t, body)
	require.Equal(t, int32(1), upload.closeCount.Load())
}

func TestMultipartRequestBodyCloseBeforeRead(t *testing.T) {
	source := &countingReader{remaining: 1024}
	upload := &recordingReadCloser{reader: source}
	body := newMultipartRequestBody(map[string]any{
		"file": fileUpload{Reader: upload, filename: "unused.bin"},
	}, apiform.FormatBrackets)

	require.NoError(t, body.Close())
	waitForMultipartBody(t, body)
	require.Zero(t, source.bytesRead.Load())
	require.Equal(t, int32(1), upload.closeCount.Load())
}

func TestMultipartRequestBodyDoesNotCloseUnownedReaders(t *testing.T) {
	stdinLikeReader := &recordingReadCloser{reader: strings.NewReader("stdin payload")}
	embedded, err := embedFiles(
		map[string]any{"file": "@-"},
		EmbedIOReader,
		&onceStdinReader{stdinReader: stdinLikeReader},
	)
	require.NoError(t, err)
	body := newMultipartRequestBody(embedded.(map[string]any), apiform.FormatBrackets)

	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Contains(t, string(data), "stdin payload")
	waitForMultipartBody(t, body)
	require.NoError(t, body.Close())
	require.Zero(t, stdinLikeReader.closeCount.Load())
}

func TestMultipartRequestOptionsReject307And308ForUploads(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "redirect-upload.txt")
			require.NoError(t, os.WriteFile(path, []byte("redirect payload"), 0o600))
			upload, err := openFileUpload(path)
			require.NoError(t, err)

			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount.Add(1)
				http.Redirect(w, r, "/upload", status)
			}))
			t.Cleanup(server.Close)

			options, err := multipartRequestOptions(map[string]any{
				"file":    upload,
				"purpose": "assistants",
			}, apiform.FormatBrackets)
			require.NoError(t, err)
			client := openai.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL+"/"),
			)

			_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
			require.ErrorContains(t, err, "streamed multipart uploads are not replayable")
			require.Equal(t, int32(1), requestCount.Load())
		})
	}
}

func TestMultipartRequestOptionsRetryScalarOnlyForms(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
			http.Error(w, "invalid multipart form", http.StatusBadRequest)
			return
		}
		assert.Equal(t, "hello", r.FormValue("prompt"))
		w.Header().Set("Content-Type", "application/json")
		if requestCount.Load() == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"retry me","type":"rate_limit_error"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"video_123","object":"video","status":"queued"}`)
	}))
	t.Cleanup(server.Close)

	options, err := multipartRequestOptions(map[string]any{"prompt": "hello"}, apiform.FormatBrackets)
	require.NoError(t, err)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Videos.New(context.Background(), openai.VideoNewParams{}, options...)
	require.NoError(t, err)
	require.Equal(t, int32(2), requestCount.Load())
}

func TestMultipartRequestOptionsDoNotRetryUploads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single-attempt.txt")
	require.NoError(t, os.WriteFile(path, []byte("single attempt"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	sourceFile := uploadSourceFile(t, upload)

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		assert.Positive(t, r.ContentLength)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"do not retry","type":"server_error"}}`)
	}))
	t.Cleanup(server.Close)

	options, err := multipartRequestOptions(map[string]any{
		"file":    upload,
		"purpose": "assistants",
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
	require.Error(t, err)
	require.Equal(t, int32(1), requestCount.Load())
	_, err = sourceFile.Stat()
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestMultipartRequestOptionsPreserveIncompleteTransportError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transport-error.txt")
	require.NoError(t, os.WriteFile(path, []byte("never consumed"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	sourceFile := uploadSourceFile(t, upload)

	transportErr := errors.New("dial failed before reading request body")
	doer := &failingHTTPDoer{err: transportErr}
	options, err := multipartRequestOptions(map[string]any{
		"file":    upload,
		"purpose": "assistants",
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL("https://example.com/"),
		option.WithHTTPClient(doer),
	)

	_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
	require.ErrorIs(t, err, transportErr)
	require.Equal(t, int32(1), doer.calls.Load())
	_, err = sourceFile.Stat()
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestMultipartRequestOptionsSetContentLengthForRegularFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.txt")
	require.NoError(t, os.WriteFile(path, []byte("known-size payload"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Positive(t, r.ContentLength)
		assert.Empty(t, r.TransferEncoding)
		contents, readErr := io.ReadAll(r.Body)
		assert.NoError(t, readErr)
		assert.Equal(t, r.ContentLength, int64(len(contents)))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"file_123","object":"file"}`)
	}))
	t.Cleanup(server.Close)

	options, err := multipartRequestOptions(map[string]any{
		"file":    upload,
		"purpose": "assistants",
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
	require.NoError(t, err)
}

func TestMultipartRequestOptionsUseChunkedEncodingForUnknownSources(t *testing.T) {
	type requestInfo struct {
		contentLength    int64
		transferEncoding []string
	}
	infoCh := make(chan requestInfo, 1)
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		infoCh <- requestInfo{r.ContentLength, append([]string(nil), r.TransferEncoding...)}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"do not retry","type":"server_error"}}`)
	}))
	t.Cleanup(server.Close)

	upload := &recordingReadCloser{reader: strings.NewReader("payload")}
	options, err := multipartRequestOptions(map[string]any{
		"file": fileUpload{Reader: upload, filename: "upload.txt"},
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
	require.Error(t, err)
	require.Equal(t, int32(1), requestCount.Load())
	info := <-infoCh
	require.Equal(t, int64(-1), info.contentLength)
	require.Contains(t, info.transferEncoding, "chunked")
	require.Equal(t, int32(1), upload.closeCount.Load())
}

func TestMultipartRequestOptionsCloseUploadsOnLengthFailure(t *testing.T) {
	first := &recordingReadCloser{reader: strings.NewReader("first")}
	second := &recordingReadCloser{reader: strings.NewReader("second")}
	_, err := multipartRequestOptions(map[string]any{
		"first":  fileUpload{Reader: first, filename: "first.bin", knownSize: true, size: math.MaxInt64},
		"second": fileUpload{Reader: second, filename: "second.bin", knownSize: true, size: 1},
	}, apiform.FormatBrackets)
	require.ErrorContains(t, err, "overflows int64")
	require.Equal(t, int32(1), first.closeCount.Load())
	require.Equal(t, int32(1), second.closeCount.Load())
}

func TestOpenFileUploadRejectsDirectoriesBeforeRequest(t *testing.T) {
	_, err := openFileUpload(t.TempDir())
	require.ErrorContains(t, err, "is a directory")
}

func TestOpenFileUploadStreamsProcfsWithUnknownLength(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procfs reproduction is Linux-specific")
	}
	const path = "/proc/version"
	upload, err := openFileUpload(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		t.Skipf("%s is unavailable: %v", path, err)
	}
	require.NoError(t, err)
	require.False(t, upload.knownSize)
	sourceFile := upload.Reader.(*os.File)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, int64(-1), r.ContentLength)
		assert.Contains(t, r.TransferEncoding, "chunked")
		file, _, parseErr := r.FormFile("file")
		if !assert.NoError(t, parseErr) {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		contents, readErr := io.ReadAll(file)
		assert.NoError(t, readErr)
		assert.NotEmpty(t, contents)
		_ = file.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"file_123","object":"file"}`)
	}))
	t.Cleanup(server.Close)

	options, err := multipartRequestOptions(map[string]any{
		"file":    upload,
		"purpose": "assistants",
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
	require.NoError(t, err)
	_, err = sourceFile.Stat()
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestOpenFileUploadTreatsEmptyRegularFileAsKnownLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, upload.Close()) })

	require.True(t, upload.knownSize)
	require.Zero(t, upload.size)
}

func TestEmbedFilesClosesPartialUploadsOnError(t *testing.T) {
	upload := &recordingReadCloser{reader: strings.NewReader("already opened")}
	missingPath := FilePathValue(t.TempDir() + "/missing.bin")

	_, err := embedFiles([]any{
		fileUpload{Reader: upload, filename: "opened.bin"},
		missingPath,
	}, EmbedIOReader, &onceStdinReader{})
	require.Error(t, err)
	require.Equal(t, int32(1), upload.closeCount.Load())
}

func waitForMultipartBody(t *testing.T, body *multipartRequestBody) {
	t.Helper()
	select {
	case <-body.done:
	case <-time.After(multipartTestTimeout):
		t.Fatal("multipart encoder did not stop")
	}
}

func uploadSourceFile(t *testing.T, upload fileUpload) *os.File {
	t.Helper()
	reader, ok := upload.Reader.(*exactLengthReadCloser)
	require.True(t, ok, "regular upload must use an exact-length reader")
	file, ok := reader.closer.(*os.File)
	require.True(t, ok, "regular upload must own its opened file")
	return file
}

type failingHTTPDoer struct {
	err   error
	calls atomic.Int32
}

func (d *failingHTTPDoer) Do(*http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return nil, d.err
}

type countingReader struct {
	remaining int64
	bytesRead atomic.Int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 0
	}
	n := len(p)
	r.remaining -= int64(n)
	r.bytesRead.Add(int64(n))
	return n, nil
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
