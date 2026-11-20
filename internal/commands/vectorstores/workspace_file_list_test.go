package vectorstores

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

func TestFileActionsMatchVisibleScope(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.filesLoaded = true
	m.enter("files")
	require.Contains(t, m.View().Content, "No files attached yet.")
	require.Contains(t, m.View().Content, "› Upload file")
	require.Equal(t, []string{fileUpload, fileRefresh, fileBack}, m.fileActions())
	modelKey(m, tea.KeyEnter)
	require.Equal(t, "upload", m.page)
	modelKey(m, tea.KeyEscape)
	require.Equal(t, "files", m.page)

	m.files = []openai.VectorStoreFile{{ID: "file_demo"}}
	m.fileNext = "file_demo"
	m.enter("files")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	require.Contains(t, m.View().Content, "of 4")
	for _, action := range m.fileActions() {
		modelKey(m, tea.KeyTab)
		require.Contains(t, m.View().Content, "› "+action)
	}
	m.store.Status = "expired"
	m.enter("files")
	require.NotContains(t, m.fileActions(), fileUpload)
	m.store.Status = "completed"
	m.enter("batchfiles")
	require.NotContains(t, m.fileActions(), fileUpload)
}

func TestIndexedFileSurvivesRefreshFailure(t *testing.T) {
	m := modelForTest(t, modelService{listFiles: func(context.Context, string, string) (FilePage, error) {
		return FilePage{}, errors.New("Failed to refresh files.")
	}})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.filesLoaded, m.uploadFromFiles = true, true
	m.enter("upload")
	refresh := m.complete(workMessage{kind: "index", attached: &openai.VectorStoreFile{ID: "file_demo", Status: "completed"}})
	modelComplete(t, m, refresh)
	view := m.View().Content
	require.Equal(t, "files", m.page)
	require.Contains(t, view, "File indexed.")
	require.Contains(t, view, "Failed to refresh files.")
	require.Contains(t, view, "› Refresh files")
	require.NotContains(t, view, "0 loaded")
	require.NotContains(t, view, "No files attached yet.")
}
