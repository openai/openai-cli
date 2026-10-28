package vectorstores

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagementServiceRequestsAndMutationRetries(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, response string
		mutation                           bool
		run                                func(ManagementService) error
	}{
		{"rename", "POST", "/vector_stores/vs_demo", `{"name":"literal @name"}`, `{"id":"vs_demo","name":"literal @name"}`, true, func(s ManagementService) error {
			_, err := s.UpdateStore(t.Context(), "vs_demo", map[string]any{"name": "literal @name"})
			return err
		}},
		{"clear expiration", "POST", "/vector_stores/vs_demo", `{"expires_after":null}`, `{"id":"vs_demo","expires_after":null}`, true, func(s ManagementService) error {
			_, err := s.UpdateStore(t.Context(), "vs_demo", map[string]any{"expires_after": nil})
			return err
		}},
		{"set expiration", "POST", "/vector_stores/vs_demo", `{"expires_after":{"anchor":"last_active_at","days":7}}`, `{"id":"vs_demo"}`, true, func(s ManagementService) error {
			_, err := s.UpdateStore(t.Context(), "vs_demo", map[string]any{"expires_after": map[string]any{"anchor": "last_active_at", "days": 7}})
			return err
		}},
		{"store deletion", "DELETE", "/vector_stores/vs_demo", "", `{"id":"vs_demo","deleted":true}`, true, func(s ManagementService) error { return s.DeleteStore(t.Context(), "vs_demo") }},
		{"attributes", "POST", "/vector_stores/vs_demo/files/file_demo", `{"attributes":{"enabled":true,"revision":7,"category":"notes"}}`, `{"id":"file_demo","vector_store_id":"vs_demo"}`, true, func(s ManagementService) error {
			_, err := s.UpdateFile(t.Context(), "vs_demo", "file_demo", map[string]any{"enabled": true, "revision": json.Number("7"), "category": "notes"})
			return err
		}},
		{"empty attributes", "POST", "/vector_stores/vs_demo/files/file_demo", `{"attributes":{}}`, `{"id":"file_demo","vector_store_id":"vs_demo"}`, true, func(s ManagementService) error {
			_, err := s.UpdateFile(t.Context(), "vs_demo", "file_demo", map[string]any{})
			return err
		}},
		{"attachment removal", "DELETE", "/vector_stores/vs_demo/files/file_demo", "", `{"id":"file_demo","deleted":true}`, true, func(s ManagementService) error { return s.RemoveFile(t.Context(), "vs_demo", "file_demo") }},
		{"batch creation", "POST", "/vector_stores/vs_demo/file_batches", `{"file_ids":["file_a","file_b"],"attributes":{"revision":7},"chunking_strategy":{"type":"static","static":{"max_chunk_size_tokens":800,"chunk_overlap_tokens":200}}}`, `{"id":"vsfb_demo","vector_store_id":"vs_demo"}`, true, func(s ManagementService) error {
			_, err := s.CreateBatch(t.Context(), "vs_demo", []string{"file_a", "file_b"}, AttachOptions{Attributes: map[string]any{"revision": 7}, Chunking: map[string]any{"type": "static", "static": map[string]any{"max_chunk_size_tokens": 800, "chunk_overlap_tokens": 200}}})
			return err
		}},
		{"configured attachment", "POST", "/vector_stores/vs_demo/files", `{"file_id":"file_demo","attributes":{"category":"notes"}}`, `{"id":"file_demo","vector_store_id":"vs_demo"}`, true, func(s ManagementService) error {
			_, err := s.AttachConfigured(t.Context(), "vs_demo", "file_demo", AttachOptions{Attributes: map[string]any{"category": "notes"}})
			return err
		}},
		{"batch status", "GET", "/vector_stores/vs_demo/file_batches/vsfb_demo", "", `{"id":"vsfb_demo","vector_store_id":"vs_demo","status":"completed"}`, false, func(s ManagementService) error { _, err := s.GetBatch(t.Context(), "vs_demo", "vsfb_demo"); return err }},
		{"batch cancel", "POST", "/vector_stores/vs_demo/file_batches/vsfb_demo/cancel", "", `{"id":"vsfb_demo","vector_store_id":"vs_demo","status":"cancelled"}`, true, func(s ManagementService) error {
			_, err := s.CancelBatch(t.Context(), "vs_demo", "vsfb_demo")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fail := range []bool{false, true} {
				if fail && !tc.mutation {
					continue
				}
				calls := 0
				s := testService(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					assert.Equal(t, tc.method, r.Method)
					assert.Equal(t, tc.path, r.URL.Path)
					assert.Equal(t, "proj_test", r.Header.Get("OpenAI-Project"))
					assert.Equal(t, "org_test", r.Header.Get("OpenAI-Organization"))
					assert.Equal(t, "inherited", r.Header.Get("X-Workspace-Test"))
					body, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					if tc.body != "" {
						assert.JSONEq(t, tc.body, string(body))
					} else {
						assert.Empty(t, body)
					}
					if fail {
						serviceJSON(w, 500, `{"error":{"message":"synthetic failure","type":"server_error"}}`)
						return
					}
					serviceJSON(w, 200, tc.response)
				}).(ManagementService)
				err := tc.run(s)
				if fail {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, 1, calls, "management mutations must not retry automatically")
			}
		})
	}
}

func TestManagementListsAndContent(t *testing.T) {
	large := strings.Repeat("synthetic indexed text\n", 60000) + "final complete content"
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files":
			assert.Equal(t, "assistants", r.URL.Query().Get("purpose"))
			assert.Equal(t, "file_prior", r.URL.Query().Get("after"))
			assert.Equal(t, "20", r.URL.Query().Get("limit"))
			serviceJSON(w, 200, `{"data":[{"id":"file_next","filename":"notes.txt"}],"has_more":true}`)
		case "/vector_stores/vs_demo/file_batches/vsfb_demo/files":
			assert.Equal(t, "file_prior", r.URL.Query().Get("after"))
			serviceJSON(w, 200, `{"data":[{"id":"file_next","vector_store_id":"vs_demo"}],"has_more":true}`)
		case "/vector_stores/vs_demo/files/file_demo/content":
			data, err := json.Marshal(map[string]any{"data": []any{map[string]any{"type": "text", "text": large}}})
			require.NoError(t, err)
			serviceJSON(w, 200, string(data))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			serviceJSON(w, 400, `{}`)
		}
	}).(ManagementService)
	files, err := s.ListSourceFiles(t.Context(), "file_prior")
	require.NoError(t, err)
	assert.Equal(t, "file_next", files.Next)
	batch, err := s.ListBatchFiles(t.Context(), "vs_demo", "vsfb_demo", "file_prior")
	require.NoError(t, err)
	assert.Equal(t, "file_next", batch.Next)
	content, err := s.FileContent(t.Context(), "vs_demo", "file_demo")
	require.NoError(t, err)
	assert.Equal(t, large, content)
}

func TestManagementRejectsUnconfirmedResults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		run        func(ManagementService) error
	}{
		{"store identity", `{"id":"vs_other"}`, func(s ManagementService) error {
			_, err := s.UpdateStore(t.Context(), "vs_demo", map[string]any{"name": "demo"})
			return err
		}},
		{"store deletion", `{"id":"vs_demo","deleted":false}`, func(s ManagementService) error { return s.DeleteStore(t.Context(), "vs_demo") }},
		{"file deletion", `{"id":"file_other","deleted":true}`, func(s ManagementService) error { return s.RemoveFile(t.Context(), "vs_demo", "file_demo") }},
		{"batch identity", `{"id":"vsfb_demo","vector_store_id":"vs_other"}`, func(s ManagementService) error { _, err := s.GetBatch(t.Context(), "vs_demo", "vsfb_demo"); return err }},
		{"missing content", `{}`, func(s ManagementService) error {
			_, err := s.FileContent(t.Context(), "vs_demo", "file_demo")
			return err
		}},
		{"nontext content", `{"data":[{"type":"binary","text":"private"}]}`, func(s ManagementService) error {
			_, err := s.FileContent(t.Context(), "vs_demo", "file_demo")
			return err
		}},
		{"missing source IDs", `{"data":[{}],"has_more":false}`, func(s ManagementService) error { _, err := s.ListSourceFiles(t.Context(), ""); return err }},
		{"invalid batch cursor", `{"data":[],"has_more":true}`, func(s ManagementService) error {
			_, err := s.ListBatchFiles(t.Context(), "vs_demo", "vsfb_demo", "")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testService(t, func(w http.ResponseWriter, r *http.Request) { serviceJSON(w, 200, tc.body) }).(ManagementService)
			require.Error(t, tc.run(s))
		})
	}
}
