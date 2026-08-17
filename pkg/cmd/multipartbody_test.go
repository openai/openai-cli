package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	data, err := io.ReadAll(body)
	require.ErrorIs(t, err, readErr)
	require.NotContains(t, string(data), "--"+body.Boundary()+"--",
		"a failed file part must not be followed by a successful terminal boundary")
	waitForMultipartBody(t, body)
	require.Equal(t, int32(1), upload.closeCount.Load())
	require.NoError(t, body.Close())
}

func TestMultipartRequestBodyJoinsCleanupErrorAfterSourceError(t *testing.T) {
	readErr := errors.New("upload read failed")
	closeErr := errors.New("upload close failed")
	upload := &recordingReadCloser{
		reader:   errorReader{err: readErr},
		closeErr: closeErr,
	}
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
	file := upload.source.file
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

func TestMultipartRequestOptionsFollow307And308ForRegularFiles(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "redirect-upload.txt")
			require.NoError(t, os.WriteFile(path, []byte("redirect payload"), 0o600))
			upload, err := openFileUpload(path)
			require.NoError(t, err)
			originalFile := upload.source.file

			var requestCount atomic.Int32
			var uploaded string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount.Add(1)
				switch r.URL.Path {
				case "/files":
					http.Redirect(w, r, "/upload", status)
				case "/upload":
					assert.Positive(t, r.ContentLength)
					assert.Empty(t, r.TransferEncoding)
					file, _, parseErr := r.FormFile("file")
					if !assert.NoError(t, parseErr) {
						http.Error(w, parseErr.Error(), http.StatusBadRequest)
						return
					}
					contents, readErr := io.ReadAll(file)
					assert.NoError(t, readErr)
					assert.NoError(t, file.Close())
					uploaded = string(contents)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"file_123","object":"file"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			options, err := multipartRequestOptions(map[string]any{
				"file":    upload,
				"purpose": "assistants",
			}, apiform.FormatBrackets)
			require.NoError(t, err)
			var response []byte
			options = append(options, option.WithResponseBodyInto(&response))
			client := openai.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL+"/"),
			)

			_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
			require.NoError(t, err)
			require.Equal(t, int32(2), requestCount.Load())
			require.Equal(t, "redirect payload", uploaded)
			_, err = originalFile.Stat()
			require.ErrorIs(t, err, os.ErrClosed)
		})
	}
}

func TestMultipartRequestOptionsReject307And308ForUnknownSources(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount.Add(1)
				http.Redirect(w, r, "/upload", status)
			}))
			t.Cleanup(server.Close)

			upload := &recordingReadCloser{reader: strings.NewReader("stdin payload")}
			options, err := multipartRequestOptions(map[string]any{
				"file": fileUpload{Reader: upload, filename: "stdin"},
			}, apiform.FormatBrackets)
			require.NoError(t, err)
			var response []byte
			options = append(options, option.WithResponseBodyInto(&response))
			client := openai.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL+"/"),
			)

			_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
			require.ErrorContains(t, err, "non-replayable source")
			require.Equal(t, int32(1), requestCount.Load())
			require.Equal(t, int32(1), upload.closeCount.Load())
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
	var response []byte
	options = append(options, option.WithResponseBodyInto(&response))
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Videos.New(context.Background(), openai.VideoNewParams{}, options...)
	require.NoError(t, err)
	require.Equal(t, int32(2), requestCount.Load())
}

func TestMultipartRequestOptionsRetryRegularFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retry-upload.txt")
	require.NoError(t, os.WriteFile(path, []byte("retry payload"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	sourceFile := upload.source.file

	var requestCount atomic.Int32
	var uploadsMu sync.Mutex
	var uploads []string
	var contentLengths []int64
	var retryCounts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		file, _, parseErr := r.FormFile("file")
		if !assert.NoError(t, parseErr) {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		contents, readErr := io.ReadAll(file)
		assert.NoError(t, readErr)
		assert.NoError(t, file.Close())
		uploadsMu.Lock()
		uploads = append(uploads, string(contents))
		contentLengths = append(contentLengths, r.ContentLength)
		retryCounts = append(retryCounts, r.Header.Get("X-Stainless-Retry-Count"))
		uploadsMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if requestCount.Load() == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"retry me","type":"rate_limit_error"}}`)
			return
		}
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
	require.Equal(t, int32(2), requestCount.Load())
	uploadsMu.Lock()
	require.Equal(t, []string{"retry payload", "retry payload"}, uploads)
	require.Len(t, contentLengths, 2)
	require.Positive(t, contentLengths[0])
	require.Equal(t, contentLengths[0], contentLengths[1])
	require.Equal(t, []string{"0", "1"}, retryCounts)
	uploadsMu.Unlock()
	_, err = sourceFile.Stat()
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestMultipartRequestOptionsCloseRegularFilesAfterFinalRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failed-retry-upload.txt")
	require.NoError(t, os.WriteFile(path, []byte("retry payload"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	sourceFile := upload.source.file

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		file, _, parseErr := r.FormFile("file")
		if !assert.NoError(t, parseErr) {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		contents, readErr := io.ReadAll(file)
		assert.NoError(t, readErr)
		assert.Equal(t, "retry payload", string(contents))
		assert.NoError(t, file.Close())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"retry me","type":"server_error"}}`)
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
	require.Equal(t, int32(3), requestCount.Load())
	_, err = sourceFile.Stat()
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestMultipartRequestOptionsCloseRegularFilesWhenBackoffCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canceled-retry-upload.txt")
	require.NoError(t, os.WriteFile(path, []byte("retry payload"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	sourceFile := upload.source.file

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"retry me","type":"rate_limit_error"}}`)
	}))
	t.Cleanup(server.Close)

	options, err := multipartRequestOptions(map[string]any{
		"file":    upload,
		"purpose": "assistants",
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	attemptReturned := make(chan struct{})
	observeAttempt := option.WithMiddleware(func(
		req *http.Request,
		next option.MiddlewareNext,
	) (*http.Response, error) {
		res, requestErr := next(req)
		close(attemptReturned)
		return res, requestErr
	})
	options = append([]option.RequestOption{observeAttempt}, options...)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan error, 1)
	go func() {
		_, requestErr := client.Files.New(ctx, openai.FileNewParams{}, options...)
		requestDone <- requestErr
	}()

	select {
	case <-attemptReturned:
	case <-time.After(multipartTestTimeout):
		t.Fatal("first retryable attempt did not return")
	}
	cancel()
	select {
	case requestErr := <-requestDone:
		require.ErrorIs(t, requestErr, context.Canceled)
	case <-time.After(multipartTestTimeout):
		t.Fatal("request did not stop after cancellation during retry backoff")
	}
	require.Equal(t, int32(1), requestCount.Load())
	require.Eventually(t, func() bool {
		_, statErr := sourceFile.Stat()
		return errors.Is(statErr, os.ErrClosed)
	}, time.Second, time.Millisecond, "cancellation cleanup must close the replay source")
}

func TestMultipartRequestOptionsLetEarlySuccessfulUploadFinish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "early-response-upload.txt")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("payload", 1024)), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	sourceFile := upload.source.file

	doer := newEarlyResponseHTTPDoer()
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
	require.NoError(t, err)
	close(doer.release)
	select {
	case <-doer.done:
	case <-time.After(multipartTestTimeout):
		t.Fatal("transport did not finish consuming the request body")
	}
	require.NoError(t, doer.err)
	require.Positive(t, doer.contentLength)
	require.Equal(t, doer.contentLength, doer.bytesRead)
	_, err = sourceFile.Stat()
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestMultipartReplayAttemptAbortDoesNotWaitForBlockedReader(t *testing.T) {
	source := newManuallyReleasedReader()
	body := newMultipartRequestBody(map[string]any{
		"file": fileUpload{Reader: source, filename: "blocked.bin"},
	}, apiform.FormatBrackets)
	attempt := &multipartReplayAttempt{bodies: []*multipartRequestBody{body}}
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, body)
		readDone <- err
	}()
	select {
	case <-source.started:
	case <-time.After(multipartTestTimeout):
		t.Fatal("multipart encoder did not start the blocked source read")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- attempt.abort() }()
	returnedPromptly := false
	select {
	case <-closeDone:
		returnedPromptly = true
	case <-time.After(100 * time.Millisecond):
	}
	close(source.release)
	if !returnedPromptly {
		select {
		case <-closeDone:
		case <-time.After(multipartTestTimeout):
			t.Fatal("attempt cleanup did not return after releasing the source")
		}
	}
	select {
	case <-readDone:
	case <-time.After(multipartTestTimeout):
		t.Fatal("request body read did not stop")
	}
	require.True(t, returnedPromptly, "attempt cleanup must not wait for an uninterruptible source read")
}

func TestMultipartRequestOptionsRedirectReplayUsesOpenedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit atomically replacing this open test file")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "redirect-upload.txt")
	replacement := filepath.Join(dir, "replacement.txt")
	require.NoError(t, os.WriteFile(path, []byte("benign-data"), 0o600))
	require.NoError(t, os.WriteFile(replacement, []byte("secret-data"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)

	var requestCount atomic.Int32
	var uploadsMu sync.Mutex
	var uploads []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		file, _, parseErr := r.FormFile("file")
		if !assert.NoError(t, parseErr) {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		contents, readErr := io.ReadAll(file)
		assert.NoError(t, readErr)
		assert.NoError(t, file.Close())
		uploadsMu.Lock()
		uploads = append(uploads, string(contents))
		uploadsMu.Unlock()

		if r.URL.Path == "/files" {
			if !assert.NoError(t, os.Rename(replacement, path)) {
				http.Error(w, "could not replace upload path", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/upload", http.StatusTemporaryRedirect)
			return
		}
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
	require.Equal(t, int32(2), requestCount.Load())
	uploadsMu.Lock()
	require.Equal(t, []string{"benign-data", "benign-data"}, uploads)
	uploadsMu.Unlock()
	require.FileExists(t, path)
	replacedContents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "secret-data", string(replacedContents))
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
	var response []byte
	options = append(options, option.WithResponseBodyInto(&response))
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
	require.NoError(t, err)
}

func TestMultipartRequestOptionsDisableRetriesForUnknownSources(t *testing.T) {
	type requestInfo struct {
		contentLength    int64
		transferEncoding []string
	}
	requestInfoCh := make(chan requestInfo, 4)
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		requestInfoCh <- requestInfo{
			contentLength:    r.ContentLength,
			transferEncoding: r.TransferEncoding,
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"retry me","type":"server_error"}}`)
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
	info := <-requestInfoCh
	require.Equal(t, int64(-1), info.contentLength)
	require.Contains(t, info.transferEncoding, "chunked")
	require.Equal(t, int32(1), upload.closeCount.Load())
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
	require.False(t, upload.canReplay())
	sourceFile := upload.Reader.(*os.File)

	type requestInfo struct {
		contentLength    int64
		transferEncoding []string
		contents         string
	}
	infoCh := make(chan requestInfo, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, _, parseErr := r.FormFile("file")
		if parseErr != nil {
			infoCh <- requestInfo{contentLength: r.ContentLength, transferEncoding: r.TransferEncoding}
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		contents, readErr := io.ReadAll(file)
		_ = file.Close()
		if readErr != nil {
			infoCh <- requestInfo{contentLength: r.ContentLength, transferEncoding: r.TransferEncoding}
			http.Error(w, readErr.Error(), http.StatusBadRequest)
			return
		}
		infoCh <- requestInfo{
			contentLength:    r.ContentLength,
			transferEncoding: append([]string(nil), r.TransferEncoding...),
			contents:         string(contents),
		}
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
	info := <-infoCh
	require.Equal(t, int64(-1), info.contentLength)
	require.Contains(t, info.transferEncoding, "chunked")
	require.NotEmpty(t, info.contents)
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
	require.True(t, upload.canReplay())
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

type earlyResponseHTTPDoer struct {
	release chan struct{}
	done    chan struct{}

	contentLength int64
	bytesRead     int64
	err           error
}

func newEarlyResponseHTTPDoer() *earlyResponseHTTPDoer {
	return &earlyResponseHTTPDoer{
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (d *earlyResponseHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	readStarted := make(chan struct{})
	d.contentLength = req.ContentLength
	go func() {
		defer close(d.done)
		buf := make([]byte, 1)
		n, readErr := req.Body.Read(buf)
		d.bytesRead += int64(n)
		close(readStarted)
		<-d.release
		if readErr == nil {
			copied, copyErr := io.Copy(io.Discard, req.Body)
			d.bytesRead += copied
			readErr = copyErr
		}
		if errors.Is(readErr, io.EOF) {
			readErr = nil
		}
		d.err = errors.Join(readErr, req.Body.Close())
	}()
	<-readStarted
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"file_123","object":"file"}`)),
		Request:    req,
	}, nil
}

type manuallyReleasedReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newManuallyReleasedReader() *manuallyReleasedReader {
	return &manuallyReleasedReader{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *manuallyReleasedReader) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, io.EOF
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
