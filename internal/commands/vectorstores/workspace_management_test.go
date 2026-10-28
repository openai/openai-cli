package vectorstores

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func managementModel(t *testing.T, handler http.HandlerFunc) *workspace {
	t.Helper()
	m := modelForTest(t, testService(t, handler))
	m.store = &openai.VectorStore{ID: "vs_demo", Name: "demo", Status: "completed"}
	m.stores = []openai.VectorStore{*m.store}
	return m
}

func managementForm(t *testing.T, m *workspace, page, value string) tea.Cmd {
	t.Helper()
	m.lastMutation = time.Time{}
	m.enter(page)
	m.editor.SetValue(value)
	modelKey(m, tea.KeyEnter)
	require.False(t, m.busy, "editing or the first Enter must not submit")
	return modelKey(m, tea.KeyEnter)
}

func TestManagementStoreFormsAndConfirmation(t *testing.T) {
	for _, tc := range []struct{ page, value, body string }{
		{"rename", "new @literal", `{"name":"new @literal"}`},
		{"expiration", "7", `{"expires_after":{"anchor":"last_active_at","days":7}}`},
		{"expiration", "never", `{"expires_after":null}`},
		{"metadata", `{"category":"notes"}`, `{"metadata":{"category":"notes"}}`},
	} {
		t.Run(tc.page+tc.value, func(t *testing.T) {
			calls := 0
			m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "/vector_stores/vs_demo", r.URL.Path)
				var body any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				encoded, _ := json.Marshal(body)
				assert.JSONEq(t, tc.body, string(encoded))
				serviceJSON(w, 200, `{"id":"vs_demo","name":"updated"}`)
			})
			cmd := managementForm(t, m, tc.page, tc.value)
			require.Zero(t, calls)
			require.Nil(t, modelKey(m, tea.KeyEnter), "repeated Enter during submission must be ignored")
			modelComplete(t, m, cmd)
			require.Equal(t, 1, calls)
			assert.Equal(t, "manage", m.page)
			assert.Equal(t, "updated", m.store.Name)
			assert.Equal(t, "updated", m.stores[0].Name)
		})
	}
	deletes := 0
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		deletes++
		assert.Equal(t, "DELETE", r.Method)
		assert.Equal(t, "/vector_stores/vs_demo", r.URL.Path)
		serviceJSON(w, 200, `{"id":"vs_demo","deleted":true}`)
	})
	m.stores = append(m.stores, openai.VectorStore{ID: "vs_keep"})
	m.pendingFile = "file_source"
	m.retainedUploads["vs_demo"] = retainedUpload{file: "file_source"}
	require.Nil(t, managementForm(t, m, "delete-store", "wrong"))
	require.Zero(t, deletes)
	assert.Contains(t, m.View().Content, "Deletion safety is not established")
	modelKey(m, tea.KeyEscape)
	require.Zero(t, deletes)
	modelComplete(t, m, managementForm(t, m, "delete-store", "vs_demo"))
	assert.Equal(t, 1, deletes)
	assert.Nil(t, m.store)
	require.Len(t, m.stores, 1)
	assert.Equal(t, "vs_keep", m.stores[0].ID)
	assert.Empty(t, m.retainedUploads)
	assert.Contains(t, m.note, "Source files remain")
}

func TestManagementFileContentAttributesAndRemoval(t *testing.T) {
	mutations := 0
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasPrefix(r.URL.Path, "/vector_stores/vs_demo/files/file_demo"))
		switch r.Method {
		case "GET":
			serviceJSON(w, 200, `{"data":[{"type":"text","text":"first passage"},{"type":"text","text":"final passage"}]}`)
		case "POST":
			mutations++
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, map[string]any{"enabled": true, "revision": float64(7)}, body["attributes"])
			serviceJSON(w, 200, `{"id":"file_demo","vector_store_id":"vs_demo","attributes":{"enabled":true,"revision":7}}`)
		case "DELETE":
			mutations++
			serviceJSON(w, 200, `{"id":"file_demo","deleted":true}`)
		}
	})
	m.file = &openai.VectorStoreFile{ID: "file_demo", VectorStoreID: "vs_demo", Status: "completed"}
	m.files = []openai.VectorStoreFile{*m.file}
	m.fileOrigin = "files"
	m.enter("file")
	modelKey(m, tea.KeyDown)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	assert.Equal(t, "document", m.page)
	assert.Contains(t, m.View().Content, "first passage")
	assert.Contains(t, m.View().Content, "final passage")
	modelKey(m, tea.KeyEscape)
	assert.Equal(t, "file", m.page)
	modelComplete(t, m, managementForm(t, m, "attributes", `{"enabled":true,"revision":7}`))
	assert.Equal(t, 1, mutations)
	m.pendingFile = "file_recovery"
	m.results = []openai.VectorStoreSearchResponse{{FileID: "file_demo"}}
	m.searched = true
	require.Nil(t, managementForm(t, m, "remove-file", "wrong"))
	assert.Equal(t, 1, mutations)
	modelComplete(t, m, managementForm(t, m, "remove-file", "file_demo"))
	assert.Equal(t, 2, mutations)
	assert.Empty(t, m.files)
	assert.Empty(t, m.results)
	assert.Equal(t, "file_recovery", m.pendingFile)
	assert.Equal(t, "files", m.page)
	assert.Contains(t, m.note, "source file remains")
}

func TestManagementSelectExistingFilesAndBatchControls(t *testing.T) {
	created, cancelled, listed := 0, 0, 0
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/files":
			serviceJSON(w, 200, `{"data":[{"id":"file_a","filename":"one.txt"},{"id":"file_b","filename":"two.txt"}],"has_more":false}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/file_batches"):
			created++
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, []any{"file_a", "file_b"}, body["file_ids"])
			serviceJSON(w, 200, `{"id":"vsfb_demo","vector_store_id":"vs_demo","status":"in_progress","file_counts":{"total":2,"completed":1,"in_progress":1}}`)
		case strings.HasSuffix(r.URL.Path, "/files"):
			listed++
			serviceJSON(w, 200, `{"data":[{"id":"file_a","vector_store_id":"vs_demo","status":"completed"}],"has_more":false}`)
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			cancelled++
			serviceJSON(w, 200, `{"id":"vsfb_demo","vector_store_id":"vs_demo","status":"cancelled","file_counts":{"total":2,"completed":1,"cancelled":1}}`)
		default:
			serviceJSON(w, 200, `{"id":"vsfb_demo","vector_store_id":"vs_demo","status":"completed","file_counts":{"total":2,"completed":2}}`)
		}
	})
	m.enter("manage")
	for range 4 {
		modelKey(m, tea.KeyDown)
	}
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	assert.Equal(t, "sources", m.page)
	modelKey(m, tea.KeyEnter)
	modelKey(m, tea.KeyDown)
	modelKey(m, tea.KeyEnter)
	require.Zero(t, created, "selection must not attach files")
	modelKey(m, tea.KeyTab)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	assert.Equal(t, 1, created)
	assert.Equal(t, "batch", m.page)
	assert.Equal(t, int64(1), m.batch.FileCounts.Completed)
	m.lastMutation = time.Time{}
	modelKey(m, tea.KeyDown)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	assert.Equal(t, "batchfiles", m.page)
	assert.Equal(t, 1, listed)
	modelKey(m, tea.KeyEscape)
	assert.Equal(t, "batch", m.page)
	require.Nil(t, managementForm(t, m, "cancel-batch", "wrong"))
	assert.Zero(t, cancelled)
	modelComplete(t, m, managementForm(t, m, "cancel-batch", "vsfb_demo"))
	assert.Equal(t, 1, cancelled)
	assert.Equal(t, int64(1), m.batch.FileCounts.Completed)
	assert.Equal(t, "cancelled", string(m.batch.Status))
}

func TestManagementFilteringExpiryAndCancellation(t *testing.T) {
	requests := 0
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) { requests++; serviceJSON(w, 500, `{}`) })
	m.stores = append(m.stores, openai.VectorStore{ID: "vs_other", Name: "other"})
	m.storeNext = "vs_more"
	require.Nil(t, managementForm(t, m, "filter", "OTHER"))
	assert.Equal(t, "stores", m.page)
	require.Len(t, m.filteredStores(), 1)
	assert.Equal(t, "vs_other", m.filteredStores()[0].ID)
	assert.Contains(t, m.View().Content, "loaded stores only")
	m.store.Status = "expired"
	for _, page := range []string{"rename", "expiration", "metadata", "attributes", "cancel-batch"} {
		m.file = &openai.VectorStoreFile{ID: "file_demo"}
		m.batch = &openai.VectorStoreFileBatch{ID: "vsfb_demo"}
		value := "example"
		if page == "cancel-batch" {
			value = "vsfb_demo"
		}
		require.Nil(t, managementForm(t, m, page, value))
		assert.Contains(t, m.note, "read-only")
	}
	assert.Zero(t, requests)
	m.store.Status = "completed"
	m.cancelling = true
	m.complete(workMessage{kind: "manage-store", store: &openai.VectorStore{ID: "vs_demo", Name: "cancelled read"}})
	assert.Equal(t, "demo", m.store.Name)
	m.cancelling = true
	m.complete(workMessage{kind: "update-store", store: &openai.VectorStore{ID: "vs_demo", Name: "committed update"}})
	assert.Equal(t, "committed update", m.store.Name)
}

func TestManagementOptionValidation(t *testing.T) {
	for _, text := range []string{`{}`, `{"attributes":{"flag":true,"revision":7,"name":"notes"}}`, `{"chunking_strategy":{"type":"auto"}}`, `{"chunking_strategy":{"type":"static","static":{"max_chunk_size_tokens":800,"chunk_overlap_tokens":400}}}`} {
		_, err := parseAttachOptions(text)
		require.NoError(t, err, text)
	}
	for _, text := range []string{`null`, `{} {}`, `{"unknown":true}`, `{"attributes":{"nested":{}}}`, `{"chunking_strategy":{"type":"static","static":{"max_chunk_size_tokens":800,"chunk_overlap_tokens":401}}}`, `{"chunking_strategy":{"type":"static","static":{"max_chunk_size_tokens":4097,"chunk_overlap_tokens":0}}}`} {
		_, err := parseAttachOptions(text)
		require.Error(t, err, text)
	}
	_, err := decodePairs(`{"revision":7}`, false)
	require.Error(t, err)
}

func TestManagementStorageEstimateUsesActiveMonth(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		created, expired time.Time
		hours            float64
	}{
		{"created this month", now.Add(-24 * time.Hour), time.Time{}, 24},
		{"created before month", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Time{}, 20*24 + 12},
		{"expired this month", now.Add(-48 * time.Hour), now.Add(-24 * time.Hour), 24},
		{"expired before month", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), 0},
		{"future creation", now.Add(time.Hour), time.Time{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &openai.VectorStore{UsageBytes: 1 << 30, CreatedAt: tc.created.Unix()}
			if !tc.expired.IsZero() {
				store.ExpiresAt = tc.expired.Unix()
			}
			assert.Equal(t, tc.hours, estimatedStorageHours(store, now)/(1<<30))
		})
	}
}
