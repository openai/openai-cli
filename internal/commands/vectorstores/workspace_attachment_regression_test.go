package vectorstores

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tabToAction(m *workspace, count int) {
	for range count {
		modelKey(m, tea.KeyTab)
	}
}

func openSourcePicker(t *testing.T, m *workspace) {
	t.Helper()
	require.Equal(t, "home", m.page)
	tabToAction(m, 5)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "manage", m.page)
	for range 4 {
		modelKey(m, tea.KeyDown)
	}
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "sources", m.page)
}

func setWorkspaceAttachmentOptions(t *testing.T, m *workspace, action int, value string) {
	t.Helper()
	page := m.page
	tabToAction(m, action)
	modelKey(m, tea.KeyEnter)
	require.Equal(t, "attach-options", m.page)
	m.editor.SetValue(value)
	modelKey(m, tea.KeyEnter)
	modelKey(m, tea.KeyEnter)
	require.Equal(t, page, m.page)
}

func TestUploadRetryKeepsOptionsAfterEditingSourceBatch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		indexing bool
	}{
		{"attachment failed", false},
		{"attachment disappeared during indexing", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testUploadRetryOptions(t, tc.indexing)
		})
	}
}

func testUploadRetryOptions(t *testing.T, indexing bool) {
	t.Helper()
	var attachments []map[string]any
	var reads int
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-should-retry", "false")
		switch r.Method + " " + r.URL.Path {
		case "POST /files":
			serviceJSON(w, 200, `{"id":"file_upload","filename":"demo.txt"}`)
		case "POST /vector_stores/vs_demo/files":
			var body map[string]any
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				return
			}
			attachments = append(attachments, body)
			if len(attachments) == 1 {
				if indexing {
					serviceJSON(w, 200, `{"id":"file_upload","vector_store_id":"vs_demo","status":"in_progress"}`)
					return
				}
				serviceJSON(w, 502, `{"error":{"message":"synthetic upload attachment failure"}}`)
				return
			}
			serviceJSON(w, 200, `{"id":"file_upload","vector_store_id":"vs_demo","status":"completed"}`)
		case "GET /vector_stores/vs_demo/files/file_upload":
			reads++
			if indexing && reads == 1 {
				serviceJSON(w, 502, `{"error":{"message":"synthetic index read failure"}}`)
				return
			}
			serviceJSON(w, 404, `{"error":{"message":"attachment not found","type":"not_found"}}`)
		case "GET /files":
			serviceJSON(w, 200, `{"data":[{"id":"file_other","filename":"other.txt"}],"has_more":false}`)
		case "GET /vector_stores/vs_demo":
			serviceJSON(w, 200, `{"id":"vs_demo","name":"demo","status":"completed"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			serviceJSON(w, 400, `{}`)
		}
	})

	m.enter("home")
	tabToAction(m, 2)
	modelKey(m, tea.KeyEnter)
	setWorkspaceAttachmentOptions(t, m, 4, `{"attributes":{"scope":"upload"},"chunking_strategy":{"type":"static","static":{"max_chunk_size_tokens":800,"chunk_overlap_tokens":200}}}`)
	path := filepath.Join(t.TempDir(), "demo.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic sample"), 0600))
	m.editor.SetValue(path)
	modelKey(m, tea.KeyEnter)
	attach := modelComplete(t, m, modelKey(m, tea.KeyEnter))
	poll := modelComplete(t, m, attach)
	if indexing {
		modelComplete(t, m, poll)
	}
	require.Equal(t, "file_upload", m.pendingFile)
	require.Error(t, m.lastErr)

	modelKey(m, tea.KeyEscape)
	openSourcePicker(t, m)
	setWorkspaceAttachmentOptions(t, m, 3, `{"attributes":{"scope":"batch"}}`)
	modelKey(m, tea.KeyEscape)
	modelKey(m, tea.KeyEscape)
	tabToAction(m, 2)
	modelKey(m, tea.KeyEnter)
	tabToAction(m, 2)
	attach = modelComplete(t, m, modelKey(m, tea.KeyEnter))
	modelComplete(t, m, attach)

	require.Len(t, attachments, 2)
	assert.Equal(t, "file_upload", attachments[0]["file_id"])
	assert.Equal(t, map[string]any{"scope": "upload"}, attachments[0]["attributes"])
	assert.Contains(t, attachments[0], "chunking_strategy")
	assert.Equal(t, attachments[0], attachments[1])
}

func TestSuccessfulBatchConsumesOptionsBeforeNextBatch(t *testing.T) {
	var batches []map[string]any
	m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /files":
			serviceJSON(w, 200, `{"data":[{"id":"file_one","filename":"one.txt"},{"id":"file_two","filename":"two.txt"}],"has_more":false}`)
		case "GET /vector_stores/vs_demo":
			serviceJSON(w, 200, `{"id":"vs_demo","name":"demo","status":"completed"}`)
		case "POST /vector_stores/vs_demo/file_batches":
			var body map[string]any
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				return
			}
			batches = append(batches, body)
			serviceJSON(w, 200, `{"id":"vsfb_demo","vector_store_id":"vs_demo","status":"in_progress"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			serviceJSON(w, 400, `{}`)
		}
	})

	m.enter("home")
	openSourcePicker(t, m)
	setWorkspaceAttachmentOptions(t, m, 3, `{"attributes":{"scope":"first"}}`)
	modelKey(m, tea.KeyEnter)
	modelKey(m, tea.KeyTab)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, "batch", m.page)

	modelKey(m, tea.KeyEscape)
	modelKey(m, tea.KeyEscape)
	openSourcePicker(t, m)
	modelKey(m, tea.KeyDown)
	modelKey(m, tea.KeyEnter)
	modelKey(m, tea.KeyTab)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))

	require.Len(t, batches, 2)
	assert.Equal(t, []any{"file_one"}, batches[0]["file_ids"])
	assert.Equal(t, map[string]any{"scope": "first"}, batches[0]["attributes"])
	assert.Equal(t, map[string]any{"file_ids": []any{"file_two"}}, batches[1])
}

func TestStaticChunkingRequiresNumericJSONValues(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"quoted size", `{"chunking_strategy":{"type":"static","static":{"max_chunk_size_tokens":"800","chunk_overlap_tokens":200}}}`},
		{"quoted overlap", `{"chunking_strategy":{"type":"static","static":{"max_chunk_size_tokens":800,"chunk_overlap_tokens":"200"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := modelForTest(t, modelService{})
			m.optionsBack = "upload"
			managementForm(t, m, "attach-options", tc.value)
			assert.Equal(t, "attach-options", m.page)
			assert.NotEmpty(t, m.note)
		})
	}
}
