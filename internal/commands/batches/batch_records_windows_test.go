//go:build windows

package batches

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/readable"
	filepermstest "github.com/openai/openai-cli/internal/testutil/fileperms"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestWindowsBatchResultsPrivateScratchStorage(t *testing.T) {
	directory := batchRecordsPrivateTemp(t)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FR;;;WD)")
	require.NoError(t, err)
	dacl, _, err := descriptor.DACL()
	require.NoError(t, err)
	require.NoError(t, windows.SetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_example" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			return
		}
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Len(t, entries, 4)
		for _, entry := range entries {
			file, err := os.Open(filepath.Join(directory, entry.Name()))
			require.NoError(t, err)
			filepermstest.AssertPrivate(t, file)
			require.NoError(t, file.Close())
		}
		_, _ = io.WriteString(w, `{"custom_id":"one","response":{"body":"synthetic"}}`)
	}))
	defer server.Close()
	root := batchRecordsTestCommand(t, server.URL, io.Discard)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "results", "batch_example"}))
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
	store, err := newBatchRecordStore(t.Context())
	require.NoError(t, err)
	spilled := false
	factory := func() (*os.File, func() error, error) {
		file, cleanup, err := store.temporaryFile()
		if err == nil {
			filepermstest.AssertPrivate(t, file)
			spilled = true
		}
		return file, cleanup, err
	}
	require.NoError(t, readable.WriteJSONStream(t.Context(), io.Discard, strings.NewReader(`{"`+strings.Repeat("key", 10000)+`":"synthetic"}`), factory))
	require.True(t, spilled)
	require.Len(t, store.files, 4, "the renderer must release its key scratch file")
	require.NoError(t, store.close())
	entries, err = os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}
