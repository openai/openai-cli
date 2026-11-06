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

func TestSearchRequiresValidMatchFields(t *testing.T) {
	for _, tc := range []struct {
		name, row string
		valid     bool
	}{
		{"missing all fields", `{}`, false},
		{"missing score", `{"file_id":"file_demo","filename":"demo.txt","content":[{"type":"text","text":"passage"}]}`, false},
		{"missing text", `{"file_id":"file_demo","filename":"demo.txt","score":0.25,"content":[{"type":"text"}]}`, false},
		{"zero score and empty content", `{"file_id":"file_demo","filename":"demo.txt","score":0.0,"content":[]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/vector_stores/vs_demo/search", r.URL.Path)
				serviceJSON(w, 200, `{"data":[`+tc.row+`],"has_more":false}`)
			})
			m.enter("home")
			m.editor.SetValue("synthetic query")
			modelComplete(t, m, modelKey(m, tea.KeyEnter))
			if tc.valid {
				require.NoError(t, m.lastErr)
				require.True(t, m.searched)
				require.Len(t, m.results, 1)
				assert.Zero(t, m.results[0].Score)
				return
			}
			assert.Error(t, m.lastErr)
			assert.False(t, m.searched)
			assert.Empty(t, m.results)
		})
	}
}

func TestStoreListReflectsRefreshedStore(t *testing.T) {
	for _, tc := range []struct{ page, action string }{
		{"home", "Manage store"},
		{"stores", "Open store"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/vector_stores/vs_demo", r.URL.Path)
				serviceJSON(w, 200, `{"id":"vs_demo","name":"renamed remotely","status":"expired","file_counts":{"total":5}}`)
			})
			m.enter(tc.page)
			if tc.page == "home" {
				tabToAction(m, 5)
			}
			modelComplete(t, m, modelKey(m, tea.KeyEnter))
			require.Equal(t, "renamed remotely", m.store.Name)
			require.Equal(t, "expired", string(m.store.Status))

			if tc.page == "home" {
				modelKey(m, tea.KeyEscape)
			}
			modelKey(m, tea.KeyEscape)
			require.Equal(t, "stores", m.page)
			view := m.View().Content
			assert.Contains(t, view, "renamed remotely")
			assert.Contains(t, view, "expired")
		})
	}
}

func TestNarrowConfirmationDisplaysFullAttachmentAndBatchID(t *testing.T) {
	for _, tc := range []struct{ page, id string }{
		{"remove-file", "file-abcdefghijklmnopQRST123"},
		{"cancel-batch", "vsfb_1234567890abcdef1234567890abcdef"},
	} {
		t.Run(tc.page, func(t *testing.T) {
			m := managementModel(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("incorrect confirmation sent %s %s", r.Method, r.URL.Path)
			})
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
			m.file = &openai.VectorStoreFile{ID: tc.id}
			m.batch = &openai.VectorStoreFileBatch{ID: tc.id}
			managementForm(t, m, tc.page, "wrong")
			require.Equal(t, tc.page, m.page)
			var displayed strings.Builder
			for _, line := range strings.Split(m.View().Content, "\n") {
				_, body, ok := strings.Cut(line, "│")
				if ok {
					displayed.WriteString(strings.TrimSpace(strings.TrimSuffix(body, "│")))
				}
			}
			assert.Contains(t, displayed.String(), tc.id, "the user must enter the full ID")
		})
	}
}
