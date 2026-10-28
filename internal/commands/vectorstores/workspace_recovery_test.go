package vectorstores

import (
	"net/http"
	"strings"
	"testing"

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
	// A held Enter can deliver this event immediately after the error update.
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
