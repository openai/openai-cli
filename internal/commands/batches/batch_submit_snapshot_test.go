package batches

import (
	"context"
	"io"

	"os"
	"path/filepath"

	"testing"

	"github.com/stretchr/testify/require"
)

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
