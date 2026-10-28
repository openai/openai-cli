package batches

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowserUploadsReviewedSnapshot(t *testing.T) {
	const reviewed = "{\"url\":\"/v1/responses\",\"body\":{\"input\":\"reviewed request\"}}\n"
	const changed = "{\"url\":\"/v1/responses\",\"body\":{\"input\":\"modified request\"}}\n"
	require.Equal(t, len(reviewed), len(changed))
	path := filepath.Join(t.TempDir(), "original input 資料.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(reviewed), 0600))
	before, err := os.Stat(path)
	require.NoError(t, err)
	temporary := batchRecordsPrivateTemp(t)
	var mu sync.Mutex
	var uploaded, filename string
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/files":
			err := r.ParseMultipartForm(1 << 20)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			data, err := io.ReadAll(file)
			if err != nil {
				t.Error(err)
			}
			if err := file.Close(); err != nil {
				t.Error(err)
			}
			uploaded, filename = string(data), header.Filename
			io.WriteString(w, `{"id":"file_snapshot"}`)
		case "/batches":
			io.WriteString(w, `{"id":"batch_snapshot","status":"validating"}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-snapshot-key"))
	draft := newBatchBrowserDraft()
	draft.path = path
	show := func(ctx context.Context, screen *batchBrowserScreen) error {
		if screen.flow.mode == "busy" {
			screen.flow.work()
			return nil
		}
		if screen.flow.mode == "confirm" {
			require.NoError(t, os.WriteFile(path, []byte(changed), 0600))
			require.NoError(t, os.Chtimes(path, before.ModTime(), before.ModTime()))
			after, err := os.Stat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			require.Equal(t, before.Size(), after.Size())
			require.Equal(t, before.ModTime(), after.ModTime())
			screen.chosen = "confirm"
			return nil
		}
		screen.chosen = "review"
		return nil
	}
	command := &cli.Command{Writer: io.Discard, ErrWriter: io.Discard}
	result, err := (workflow{options: testOptions()}).createBatchInBrowser(t.Context(), command, client, nil, draft, show)
	require.NoError(t, err)
	require.Equal(t, "batch_snapshot", result.Get("id").Str)
	mu.Lock()
	require.Equal(t, reviewed, uploaded, "the upload must contain the reviewed bytes")
	require.Equal(t, filepath.Base(path), filename)
	require.Equal(t, []string{"POST /files", "POST /batches"}, requests)
	mu.Unlock()
	entries, err := os.ReadDir(temporary)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), "openai-batch-records-"), "snapshot remained after submission")
	}
}

func TestBatchSubmitInferredSnapshotSurvivesSourceRewrite(t *testing.T) {
	const reviewed = "{\"url\":\"/v1/responses\",\"body\":{\"input\":\"reviewed\"}}\n"
	const changed = "{\"url\":\"/v1/responses\",\"body\":{\"input\":\"modified\"}}\n"
	path := filepath.Join(t.TempDir(), "reviewed file.json")
	require.NoError(t, os.WriteFile(path, []byte(reviewed), 0600))
	temporary := batchRecordsPrivateTemp(t)
	endpoint, snapshot, err := inferBatchSubmitEndpoint(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshot.close()) })
	require.Equal(t, "/v1/responses", endpoint)
	require.Len(t, snapshot.store.files, 1, "snapshots must not allocate record indexes")
	require.NoError(t, os.WriteFile(path, []byte(changed), 0600))
	require.NoError(t, os.Chtimes(path, snapshot.ModTime(), snapshot.ModTime()))
	upload, err := openBatchSubmissionFile(t.Context(), path, snapshot)
	require.NoError(t, err)
	content, err := io.ReadAll(upload.Reader)
	require.NoError(t, err)
	require.Equal(t, reviewed, string(content))
	require.Equal(t, filepath.Base(path), upload.FileName)
	require.Equal(t, "application/json", upload.MediaType)
	require.Equal(t, int64(len(reviewed)), upload.Size)
	require.True(t, upload.KnownSize)
	require.NoError(t, upload.Close())
	require.NoError(t, snapshot.close())
	entries, err := os.ReadDir(temporary)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestBatchSubmissionSnapshotFailureCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("not valid JSONL\n"), 0600))
	temporary := batchRecordsPrivateTemp(t)
	_, snapshot, err := inferBatchSubmitEndpoint(t.Context(), path)
	require.ErrorContains(t, err, "not a valid JSON request object")
	require.Nil(t, snapshot)
	entries, err := os.ReadDir(temporary)
	require.NoError(t, err)
	require.Empty(t, entries)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	snapshot, err = prepareBatchSubmissionSnapshot(ctx, path)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, snapshot)
	entries, err = os.ReadDir(temporary)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestBatchBrowserInterruptedSubmissionHasManualRecovery(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := (workflow{options: testOptions()}).browserSubmissionError(&cli.Command{}, batchSubmission{Stage: "create", InputFileID: "file_snapshot"}, cause)
		require.ErrorIs(t, err, cause)
		require.Contains(t, err.Error(), "Batch creation is unconfirmed")
		require.Contains(t, err.Error(), "Uploaded input file: file_snapshot")
		require.Contains(t, err.Error(), "Check batches list before manually reusing")
		require.NotContains(t, err.Error(), "draft will reuse")
	}
}
