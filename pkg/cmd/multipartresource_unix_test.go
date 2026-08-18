//go:build !windows

package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const multipartResourceHelperEnv = "OPENAI_MULTIPART_RESOURCE_HELPER"

func TestFilesCreateCLIStreamsSparseFileWhenSnapshotQuotaExceeded(t *testing.T) {
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

func TestImagesEditCLIDialsBeforeOpeningReplayDescriptors(t *testing.T) {
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
