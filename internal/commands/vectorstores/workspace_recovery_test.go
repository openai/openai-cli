package vectorstores

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceBatchCursorDoesNotLeakIntoStoreFiles(t *testing.T) {
	var ordinaryCursors []string
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-should-retry", "false")
		if strings.Contains(r.URL.Path, "/file_batches/") {
			serviceJSON(w, 200, `{"data":[{"id":"file_batch_old","vector_store_id":"vs_demo","status":"completed"}],"has_more":true}`)
			return
		}
		require.Equal(t, "/vector_stores/vs_demo/files", r.URL.Path)
		after := r.URL.Query().Get("after")
		ordinaryCursors = append(ordinaryCursors, after)
		if after == "" {
			if len(ordinaryCursors) == 1 {
				serviceJSON(w, 502, `{"error":{"message":"synthetic initial list failure"}}`)
			} else {
				serviceJSON(w, 200, `{"data":[{"id":"file_store_first","vector_store_id":"vs_demo","status":"completed"}],"has_more":true}`)
			}
			return
		}
		serviceJSON(w, 200, `{"data":[{"id":"file_older","vector_store_id":"vs_demo","status":"completed"}],"has_more":false}`)
	})
	m.batch = &openai.VectorStoreFileBatch{ID: "vsfb_demo", VectorStoreID: "vs_demo"}
	m.enter("batch")
	modelKey(m, tea.KeyDown)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "batchfiles", m.page)
	require.Equal(t, "file_batch_old", m.fileNext)
	modelKey(m, tea.KeyEscape)
	modelKey(m, tea.KeyEscape)
	modelKey(m, tea.KeyEscape)
	require.Equal(t, "home", m.page)
	for range 3 {
		modelKey(m, tea.KeyTab)
	}
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "files", m.page)
	require.Error(t, m.lastErr)
	assert.Empty(t, m.files, "a failed first store-file load cannot display batch-only rows as the store list")
	assert.Empty(t, m.fileNext, "a batch cursor must not enter the complete store-file list")
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyTab)
	if more := modelKey(m, tea.KeyEnter); more != nil {
		modelComplete(t, m, more)
	}
	require.Equal(t, []string{""}, ordinaryCursors)
	modelKey(m, tea.KeyLeft) // Refresh this store.
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "file_store_first", m.fileNext)
	modelKey(m, tea.KeyRight)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, []string{"", "", "file_store_first"}, ordinaryCursors)
	require.Len(t, m.files, 2)
	require.Empty(t, m.fileNext)
}

func TestWorkspaceFailedCreateIgnoresImmediateRepeatedEnter(t *testing.T) {
	creates := 0
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		creates++
		w.Header().Set("x-should-retry", "false")
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/vector_stores", r.URL.Path)
		serviceJSON(w, 502, `{"error":{"message":"synthetic uncertain creation"}}`)
	})
	m.enter("create")
	m.editor.SetValue("synthetic store")
	modelKey(m, tea.KeyEnter)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, 1, creates)
	require.Error(t, m.lastErr)
	assert.Contains(t, m.note, "Refresh stores before retrying.")
	m.lastMutation = time.Now().Add(-time.Second)
	// A held Enter must not duplicate the write even after the debounce expires.
	repeated := modelKey(m, tea.KeyEnter)
	assert.Nil(t, repeated, "an immediate repeated Enter must not duplicate an uncertain mutation")
	if repeated != nil {
		modelComplete(t, m, repeated)
	}
	require.Equal(t, 1, creates)
	modelKey(m, tea.KeyTab)
	modelKey(m, tea.KeyLeft)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, 2, creates, "deliberate retry after navigation")
}

func TestWorkspaceCreateAndUploadReportUnconfirmedWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-source.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic content"), 0o600))
	for _, operation := range []string{"create", "upload"} {
		for _, tc := range []struct {
			name      string
			status    int
			uncertain bool
		}{
			{"explicit rejection", 403, false}, {"timeout", 408, true}, {"server failure", 502, true},
			{"missing receipt ID", 200, true}, {"lost response", 0, true},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				requests := 0
				m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
					requests++
					_, err := io.Copy(io.Discard, r.Body)
					require.NoError(t, err)
					if tc.status == 0 {
						conn, _, err := w.(http.Hijacker).Hijack()
						require.NoError(t, err)
						require.NoError(t, conn.Close())
						return
					}
					serviceJSON(w, tc.status, `{"error":{"message":"private response text"}}`)
				})
				m.errorMessage = func(error) string { return "Synthetic diagnostic." }
				m.enter(operation)
				value, warning := "synthetic store", "Refresh stores before retrying."
				if operation == "upload" {
					value, warning = path, "Check project Files before retrying."
				}
				m.editor.SetValue(value)
				modelKey(m, tea.KeyEnter)
				require.Nil(t, modelComplete(t, m, modelKey(m, tea.KeyEnter)))
				require.Error(t, m.lastErr)
				require.Equal(t, 1, requests)
				assert.Empty(t, m.pendingFile)
				assert.Contains(t, m.note, "Synthetic diagnostic.")
				assert.NotContains(t, m.note, "private response text")
				assert.NotContains(t, m.note, path)
				if tc.status >= 400 {
					var apiErr *openai.Error
					require.ErrorAs(t, m.lastErr, &apiErr)
					assert.Equal(t, tc.status, apiErr.StatusCode)
				}
				if tc.uncertain {
					assert.Contains(t, m.note, warning)
					m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
					assert.Contains(t, m.View().Content, strings.Split(warning, " before")[0])
					m.lastMutation = time.Time{}
					assert.Nil(t, modelKey(m, tea.KeyEnter), "held Enter cannot retry")
				} else {
					assert.NotContains(t, m.note, warning)
					m.lastMutation = time.Time{}
					assert.NotNil(t, modelKey(m, tea.KeyEnter), "a confirmed rejection permits retry")
				}
			})
		}
	}
}

func TestWorkspaceLocalUploadFailureDoesNotSuggestRemoteCheck(t *testing.T) {
	m := managementModel(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP request") })
	m.enter("upload")
	m.editor.SetValue(filepath.Join(t.TempDir(), "missing.txt"))
	modelKey(m, tea.KeyEnter)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.ErrorIs(t, m.lastErr, os.ErrNotExist)
	assert.NotContains(t, m.note, "Check project Files")
}

func TestWorkspaceCancelledCreationPointsToStoreList(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	})
	defer close(release)
	m.enter("create")
	m.editor.SetValue("synthetic store")
	modelKey(m, tea.KeyEnter)
	cmd := modelKey(m, tea.KeyEnter)
	completed := make(chan tea.Msg, 1)
	go func() { completed <- cmd() }()
	<-started
	modelKey(m, tea.KeyEscape)
	msg := (<-completed).(workMessage)
	require.True(t, errors.Is(msg.err, context.Canceled))
	m.Update(msg)
	assert.Contains(t, m.note, "Refresh stores before retrying.")
	assert.Contains(t, m.note, "Stopped local work.")
}

func TestWorkspaceCancelledStoreReadCannotReplaceSelection(t *testing.T) {
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/vector_stores/vs_other", r.URL.Path)
		serviceJSON(w, 200, `{"id":"vs_other","name":"other","status":"completed"}`)
	})
	m.stores = append(m.stores, openai.VectorStore{ID: "vs_other", Name: "other"})
	m.enter("stores")
	modelKey(m, tea.KeyDown)
	cmd := modelKey(m, tea.KeyEnter)
	require.NotNil(t, cmd)
	// The worker finished, but its completion message has not reached the UI.
	completed := cmd()
	require.IsType(t, workMessage{}, completed)
	modelKey(m, tea.KeyEscape)
	require.True(t, m.cancelling)
	m.Update(completed)
	assert.Equal(t, "stores", m.page, "a canceled read must not navigate to its result")
	assert.Equal(t, "vs_demo", m.store.ID, "a canceled read must not replace the current store")
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "home", m.page)
	require.Equal(t, "vs_other", m.store.ID)
}

func TestWorkspaceNarrowEmptySearchExplainsIndexing(t *testing.T) {
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		serviceJSON(w, 200, `{"data":[],"has_more":false}`)
	})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.enter("home")
	m.editor.SetValue("a missing ribbon")
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	view := m.View().Content
	assert.Contains(t, view, "No matches returned.")
	assert.Contains(t, view, "Indexing state was not checked.")
}

func TestWorkspaceNarrowDeleteShowsRiskAndFullID(t *testing.T) {
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("confirmation with a mismatched ID sent a request")
	})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.store.ID = "vs_" + strings.Repeat("a", 32)
	require.Nil(t, managementForm(t, m, "delete-store", "wrong"))
	view := m.View().Content
	assert.Contains(t, view, "Reference lookup is unavailable.")
	assert.Contains(t, view, "Deletion safety is not established")
	assert.Contains(t, view, m.store.ID[:34])
	assert.Contains(t, view, "│ a ")
}
