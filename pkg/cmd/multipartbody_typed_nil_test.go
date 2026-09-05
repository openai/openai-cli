package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func typedNilMultipartReader() io.Reader {
	var reader *strings.Reader
	return reader
}

func TestInspectMultipartBodyTreatsTypedNilReaderAsScalar(t *testing.T) {
	info := inspectMultipartBody(map[string]any{"file": typedNilMultipartReader()})

	require.False(t, info.hasUpload)
	require.True(t, info.knownLength)
}

func TestMultipartRequestOptionsRetryTypedNilReaderAsScalar(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
			http.Error(w, "invalid multipart form", http.StatusBadRequest)
			return
		}
		assert.Equal(t, []string{""}, r.MultipartForm.Value["file"])
		assert.Empty(t, r.MultipartForm.File["file"])
		assert.Positive(t, r.ContentLength)

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

	options, err := multipartRequestOptions(map[string]any{
		"file":   typedNilMultipartReader(),
		"prompt": "hello",
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Videos.New(context.Background(), openai.VideoNewParams{}, options...)
	require.NoError(t, err)
	require.Equal(t, int32(2), requestCount.Load())
}

func TestMultipartRequestOptionsReplayTypedNilReaderAcrossRedirects(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount.Add(1)
				if r.URL.Path != "/redirected" {
					http.Redirect(w, r, "/redirected", status)
					return
				}

				if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
					http.Error(w, "invalid multipart form", http.StatusBadRequest)
					return
				}
				assert.Equal(t, []string{""}, r.MultipartForm.Value["file"])
				assert.Empty(t, r.MultipartForm.File["file"])
				assert.Positive(t, r.ContentLength)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"video_123","object":"video","status":"queued"}`)
			}))
			t.Cleanup(server.Close)

			options, err := multipartRequestOptions(map[string]any{
				"file":   typedNilMultipartReader(),
				"prompt": "hello",
			}, apiform.FormatBrackets)
			require.NoError(t, err)
			client := openai.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL+"/"),
			)

			_, err = client.Videos.New(context.Background(), openai.VideoNewParams{}, options...)
			require.NoError(t, err)
			require.Equal(t, int32(2), requestCount.Load())
		})
	}
}

func TestMultipartRequestOptionsKnownUploadAllowsTypedNilReaderField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.txt")
	require.NoError(t, os.WriteFile(path, []byte("payload"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		assert.Positive(t, r.ContentLength)
		if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
			http.Error(w, "invalid multipart form", http.StatusBadRequest)
			return
		}
		assert.Equal(t, []string{""}, r.MultipartForm.Value["optional"])
		assert.Len(t, r.MultipartForm.File["file"], 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"file_123","object":"file","bytes":7,"filename":"payload.txt","purpose":"assistants"}`)
	}))
	t.Cleanup(server.Close)

	options, err := multipartRequestOptions(map[string]any{
		"file":     upload,
		"optional": typedNilMultipartReader(),
		"purpose":  "assistants",
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
	require.NoError(t, err)
	require.Equal(t, int32(1), requestCount.Load())
}
