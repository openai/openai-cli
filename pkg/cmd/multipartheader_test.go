package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilesCreateCLIRejectsNewlineFilenameBeforeRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain newline characters")
	}

	path := filepath.Join(t.TempDir(), "upload\nX-Injected: value.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic upload"), 0o600))

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	err := runFilesCreateCLI(t.Context(), server.URL+"/", path)
	require.ErrorContains(t, err, "invalid control character in multipart filename")
	require.Zero(t, requests.Load(), "invalid local filenames must be rejected before any request")
}
