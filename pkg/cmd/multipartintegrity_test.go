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

func TestFilesCreateCLISnapshotsBeforeSourceShrinks(t *testing.T) {
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

	require.NoError(t, runFilesCreateCLI(t.Context(), server.URL+"/", path))
	result := <-resultCh
	require.NoError(t, result.err)
	require.True(t, result.committed)
	require.Equal(t, int64(uploadSize), result.bytes)
}

func TestMultipartReplayPrematureEOFDoesNotCommitFinalBoundary(t *testing.T) {
	const uploadSize = 8 << 20
	path := filepath.Join(t.TempDir(), "replay-source.bin")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("a", uploadSize)), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	source := &replayableFileSource{file: file, size: uploadSize}
	upload := fileUpload{
		Reader:      io.NewSectionReader(file, 0, uploadSize),
		filename:    filepath.Base(path),
		contentType: "application/octet-stream",
		size:        uploadSize,
		knownSize:   true,
		source:      source,
		ownsSource:  true,
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

func TestOpenFileUploadRemovesSnapshotOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.txt")
	require.NoError(t, os.WriteFile(path, []byte("snapshot payload"), 0o600))
	upload, err := openFileUpload(path)
	require.NoError(t, err)
	snapshotPath := upload.source.file.Name()

	require.NoError(t, upload.Close())
	_, err = os.Stat(snapshotPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSnapshotReplayableFileRejectsPrematureEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.txt")
	require.NoError(t, os.WriteFile(path, []byte("short"), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)

	_, err = snapshotReplayableFile(file, int64(len("longer than short")), path)
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
