//go:build !windows

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const multipartResourceHelperEnv = "OPENAI_MULTIPART_RESOURCE_HELPER"

func TestFilesCreateCLICancelClosesStalledFIFO(t *testing.T) {
	fifoPath := filepath.Join(t.TempDir(), "upload.fifo")
	require.NoError(t, unix.Mkfifo(fifoPath, 0o600))

	writerRelease := make(chan struct{})
	writerProbe := make(chan chan<- error)
	writerDone := make(chan error, 1)
	go func() {
		writer, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			writerDone <- err
			return
		}
		defer writer.Close()
		if _, err := writer.Write([]byte("partial upload")); err != nil {
			writerDone <- err
			return
		}
		select {
		case result := <-writerProbe:
			_, err := writer.Write([]byte("still open"))
			result <- err
			<-writerRelease
		case <-writerRelease:
		}
		writerDone <- nil
	}()
	var releaseWriterOnce sync.Once
	releaseWriter := func() { releaseWriterOnce.Do(func() { close(writerRelease) }) }

	requestStarted := make(chan struct{})
	receiverDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			receiverDone <- fmt.Errorf("authorization header = %q", got)
			close(requestStarted)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			receiverDone <- fmt.Errorf("parse content type: %w", err)
			close(requestStarted)
			http.Error(w, "invalid content type", http.StatusBadRequest)
			return
		}
		if mediaType != "multipart/form-data" {
			receiverDone <- fmt.Errorf("content type = %q", mediaType)
			close(requestStarted)
			http.Error(w, "invalid content type", http.StatusBadRequest)
			return
		}
		part, err := nextFilePart(multipart.NewReader(r.Body, params["boundary"]))
		if err != nil {
			receiverDone <- err
			close(requestStarted)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		buf := make([]byte, 1)
		_, err = part.Read(buf)
		close(requestStarted)
		if err == nil {
			_, err = io.Copy(io.Discard, part)
		}
		receiverDone <- err
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"file_123","object":"file"}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(releaseWriter)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requestDone := make(chan error, 1)
	go func() {
		requestDone <- runFilesCreateCLI(ctx, server.URL+"/", fifoPath)
	}()

	select {
	case <-requestStarted:
	case <-time.After(multipartTestTimeout):
		releaseWriter()
		t.Fatal("authenticated receiver did not observe the FIFO upload")
	}
	cancel()

	select {
	case err := <-requestDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		releaseWriter()
		err := <-requestDone
		t.Fatalf("canceled FIFO upload waited for its producer to close: %v", err)
	}
	select {
	case err := <-writerDone:
		t.Fatalf("FIFO producer exited before the test released it: %v", err)
	default:
	}
	probeResult := make(chan error, 1)
	writerProbe <- probeResult
	require.ErrorIs(t, <-probeResult, unix.EPIPE, "cancellation must close the owned FIFO reader")

	releaseWriter()
	require.NoError(t, <-writerDone)
	require.Error(t, <-receiverDone, "canceled multipart stream must end without a terminal boundary")
}

func TestFilesCreateCLIStreamsSparseFileWithLowFileSizeLimit(t *testing.T) {
	const uploadSize = 256 << 10
	path := filepath.Join(t.TempDir(), "sparse.bin")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(uploadSize))
	require.NoError(t, file.Close())

	received := make(chan int64, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contents, err := readMultipartUpload(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- int64(len(contents))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"file_123","object":"file"}`)
	}))
	t.Cleanup(server.Close)

	output, err := runMultipartResourceHelper(t, map[string]string{
		multipartResourceHelperEnv: "file-size",
		"OPENAI_HELPER_BASE_URL":   server.URL + "/",
		"OPENAI_HELPER_PATH":       path,
		"TMPDIR":                   t.TempDir(),
	})
	require.NoError(t, err, output)
	require.Equal(t, int64(uploadSize), <-received)
}

func TestImagesEditCLIStreamsMultipleFilesWithLowDescriptorLimit(t *testing.T) {
	paths := make([]string, 16)
	for i := range paths {
		paths[i] = filepath.Join(t.TempDir(), "image.png")
		require.NoError(t, os.WriteFile(paths[i], []byte{byte(i)}, 0o600))
	}
	encodedPaths, err := json.Marshal(paths)
	require.NoError(t, err)

	received := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var fileCount int
		for _, files := range r.MultipartForm.File {
			fileCount += len(files)
		}
		received <- fileCount
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(server.Close)

	output, err := runMultipartResourceHelper(t, map[string]string{
		multipartResourceHelperEnv: "file-descriptors",
		"OPENAI_HELPER_BASE_URL":   server.URL + "/",
		"OPENAI_HELPER_PATHS":      string(encodedPaths),
	})
	require.NoError(t, err, output)
	require.Equal(t, 16, <-received)
}

func TestMultipartResourceHelper(t *testing.T) {
	mode := os.Getenv(multipartResourceHelperEnv)
	if mode == "" {
		t.Skip("helper process")
	}

	switch mode {
	case "file-size":
		require.NoError(t, unix.Setrlimit(unix.RLIMIT_FSIZE, &unix.Rlimit{Cur: 64 << 10, Max: 64 << 10}))
		require.NoError(t, runFilesCreateCLI(t.Context(), os.Getenv("OPENAI_HELPER_BASE_URL"), os.Getenv("OPENAI_HELPER_PATH")))
	case "file-descriptors":
		require.NoError(t, unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: 23, Max: 23}))
		var paths []string
		require.NoError(t, json.Unmarshal([]byte(os.Getenv("OPENAI_HELPER_PATHS")), &paths))
		require.NoError(t, runImagesEditCLI(t.Context(), os.Getenv("OPENAI_HELPER_BASE_URL"), paths))
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func runMultipartResourceHelper(t *testing.T, env map[string]string) (string, error) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(executable, "-test.run=^TestMultipartResourceHelper$")
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}
