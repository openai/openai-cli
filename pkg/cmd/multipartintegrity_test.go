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
	"strings"
	"sync"
	"testing"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestFilesCreateCLIUsesImmutableReplayContent(t *testing.T) {
	for _, status := range []int{
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
		http.StatusTooManyRequests,
	} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upload.txt")
			require.NoError(t, os.WriteFile(path, []byte("benign-data"), 0o600))

			var mu sync.Mutex
			var uploads []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				contents, readErr := readMultipartUpload(r)
				if readErr != nil {
					http.Error(w, readErr.Error(), http.StatusBadRequest)
					return
				}
				mu.Lock()
				uploads = append(uploads, string(contents))
				requestNumber := len(uploads)
				mu.Unlock()

				if requestNumber == 1 {
					if writeErr := os.WriteFile(path, []byte("secret-data"), 0o600); writeErr != nil {
						http.Error(w, writeErr.Error(), http.StatusInternalServerError)
						return
					}
					if status == http.StatusTooManyRequests {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Retry-After", "0")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, `{"error":{"message":"retry me","type":"rate_limit_error"}}`)
						return
					}
					http.Redirect(w, r, "/upload", status)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"file_123","object":"file"}`)
			}))
			t.Cleanup(server.Close)

			require.NoError(t, runFilesCreateCLI(t.Context(), server.URL+"/", path))
			mu.Lock()
			require.Equal(t, []string{"benign-data", "benign-data"}, uploads)
			mu.Unlock()
		})
	}
}

func TestFilesCreateCLIAbortsWhenSourceShrinksDuringFirstSend(t *testing.T) {
	const uploadSize = 16 << 20
	path := filepath.Join(t.TempDir(), "shrinking.bin")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("a", uploadSize)), 0o600))

	type receiverResult struct {
		bytes     int64
		committed bool
		err       error
	}
	resultCh := make(chan receiverResult, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if parseErr != nil || mediaType != "multipart/form-data" {
			resultCh <- receiverResult{err: parseErr}
			http.Error(w, "invalid content type", http.StatusBadRequest)
			return
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		part, partErr := nextFilePart(reader)
		if partErr != nil {
			resultCh <- receiverResult{err: partErr}
			http.Error(w, partErr.Error(), http.StatusBadRequest)
			return
		}
		buf := make([]byte, 1)
		n, readErr := part.Read(buf)
		if readErr == nil {
			readErr = os.Truncate(path, 0)
		}
		copied, copyErr := io.Copy(io.Discard, part)
		if readErr == nil {
			readErr = copyErr
		}
		finalErr := consumeMultipartRemainder(reader)
		resultCh <- receiverResult{
			bytes:     int64(n) + copied,
			committed: errors.Is(finalErr, io.EOF),
			err:       errors.Join(readErr, incompleteMultipartError(finalErr)),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"file_123","object":"file"}`)
	}))
	t.Cleanup(server.Close)

	err := runFilesCreateCLI(t.Context(), server.URL+"/", path)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	result := <-resultCh
	require.Error(t, result.err)
	require.False(t, result.committed, "a truncated first stream must not emit a terminal multipart boundary")
	require.Less(t, result.bytes, int64(uploadSize))
}

func TestMultipartReplayPrematureEOFDoesNotCommitFinalBoundary(t *testing.T) {
	const uploadSize = 8 << 20
	path := filepath.Join(t.TempDir(), "replay-source.bin")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("a", uploadSize)), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	upload := fileUpload{
		Reader: &exactLengthReadCloser{
			exactLengthReader: exactLengthReader{
				reader:    io.NewSectionReader(file, 0, uploadSize),
				remaining: uploadSize,
			},
			closer: file,
		},
		filename:    filepath.Base(path),
		contentType: "application/octet-stream",
		size:        uploadSize,
		knownSize:   true,
	}

	type receiverResult struct {
		committed bool
		err       error
	}
	resultCh := make(chan receiverResult, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if parseErr != nil || mediaType != "multipart/form-data" {
			resultCh <- receiverResult{err: parseErr}
			return
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		part, partErr := nextFilePart(reader)
		if partErr != nil {
			resultCh <- receiverResult{err: partErr}
			return
		}
		buf := make([]byte, 1)
		_, readErr := part.Read(buf)
		if readErr == nil {
			readErr = os.Truncate(path, 0)
		}
		if readErr == nil {
			_, readErr = io.Copy(io.Discard, part)
		}
		finalErr := consumeMultipartRemainder(reader)
		resultCh <- receiverResult{
			committed: errors.Is(finalErr, io.EOF),
			err:       errors.Join(readErr, incompleteMultipartError(finalErr)),
		}
	}))
	t.Cleanup(server.Close)

	options, err := multipartRequestOptions(map[string]any{
		"file":    upload,
		"purpose": "assistants",
	}, apiform.FormatBrackets)
	require.NoError(t, err)
	options = append(options, option.WithMaxRetries(0))
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/"),
	)

	_, err = client.Files.New(context.Background(), openai.FileNewParams{}, options...)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	result := <-resultCh
	require.False(t, result.committed, "an incomplete replay source must not emit a terminal multipart boundary")
	require.Error(t, result.err)
}

func TestOpenFileUploadDoesNotRequireSnapshotBeforeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.txt")
	require.NoError(t, os.WriteFile(path, []byte("streamed payload"), 0o600))
	setUnavailableTempDir(t)
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	contents, err := io.ReadAll(upload.Reader)
	require.NoError(t, err)
	require.Equal(t, "streamed payload", string(contents))
	require.NoError(t, upload.Close())
}

func TestExactLengthUploadRejectsPrematureEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.txt")
	require.NoError(t, os.WriteFile(path, []byte("short"), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	reader := &exactLengthReadCloser{
		exactLengthReader: exactLengthReader{
			reader:    file,
			remaining: int64(len("longer than short")),
		},
		closer: file,
	}
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func readMultipartUpload(r *http.Request) ([]byte, error) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return nil, err
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func nextFilePart(reader *multipart.Reader) (*multipart.Part, error) {
	for {
		part, err := reader.NextPart()
		if err != nil {
			return nil, err
		}
		if part.FormName() == "file" {
			return part, nil
		}
		if _, err := io.Copy(io.Discard, part); err != nil {
			return nil, err
		}
		if err := part.Close(); err != nil {
			return nil, err
		}
	}
}

func consumeMultipartRemainder(reader *multipart.Reader) error {
	for {
		part, err := reader.NextPart()
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, part); err != nil {
			return err
		}
		if err := part.Close(); err != nil {
			return err
		}
	}
}

func incompleteMultipartError(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func runFilesCreateCLI(ctx context.Context, baseURL, path string) error {
	create := filesCreate
	command := &cli.Command{
		Name: "openai",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "debug"},
			&cli.StringFlag{Name: "base-url"},
			&cli.StringFlag{Name: "format", Value: "json"},
			&cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
			&cli.StringFlag{Name: "api-key"},
		},
		Commands: []*cli.Command{{
			Name:     "files",
			Commands: []*cli.Command{&create},
		}},
	}
	return command.Run(ctx, []string{
		"openai",
		"--api-key", "test-key",
		"--base-url", baseURL,
		"files", "create",
		"--file", path,
		"--purpose", "assistants",
	})
}
