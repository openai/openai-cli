package vectorstores

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

func TestPastedUploadPathRemainsVisible(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 120, Height: 32}, {Width: 122, Height: 32}, {Width: 124, Height: 32}} {
		t.Run(fmt.Sprint(size.Width), func(t *testing.T) {
			m := modelForTest(t, modelService{})
			m.Update(size)
			m.enter("upload")
			const name = "f15-search-demo.txt"
			path := "/demo/" + strings.Repeat("x", 49-len("/demo//")-len(name)) + "/" + name
			m.Update(tea.PasteMsg{Content: path})
			view := func() string { return ansi.Strip(m.View().Content) }
			require.Contains(t, view(), name)
			modelKey(m, tea.KeyHome)
			require.Contains(t, view(), "/demo/")
			m.Update(tea.KeyPressMsg{Code: '.', Text: "."})
			require.Equal(t, "."+path, m.editor.Value())
			modelKey(m, tea.KeyEnter)
			require.Contains(t, view(), name)
			require.Contains(t, view(), "› Upload")
			require.False(t, m.busy)
		})
	}
}

func TestUploadPathEditingSurvivesResize(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.enter("upload")
	const name = "重👩‍🔬é\u2066.txt"
	path := "/" + strings.Repeat("界", 43) + "/" + name
	m.Update(tea.PasteMsg{Content: path})
	modelKey(m, tea.KeyLeft)
	modelKey(m, tea.KeyLeft)
	column := m.editor.Column()
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	field := ansi.Strip(m.uploadPathField())
	require.Contains(t, field, cleanLine(name))
	require.NotContains(t, field, "\u2066")
	require.LessOrEqual(t, ansi.StringWidth(field), m.editor.Width())
	require.Equal(t, column, m.editor.Column())
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	m.Update(tea.PasteMsg{Content: "T"})
	require.Equal(t, strings.TrimSuffix(path, ".txt")+".Txt", m.editor.Value())
	modelKey(m, tea.KeyEnter)
	require.Contains(t, ansi.Strip(m.View().Content), cleanLine(strings.TrimSuffix(name, ".txt")+".Txt"))
}

func TestUploadPathPasteKeepsFilenameSpaces(t *testing.T) {
	for _, ending := range []string{"", "\n", "\r\n", "\r"} {
		// Both the leading space and the trailing space belong to this filename.
		const path = " moon flower.txt "
		var uploaded string
		m := modelForTest(t, modelService{upload: func(_ context.Context, p string) (*openai.FileObject, error) {
			uploaded = p
			return &openai.FileObject{ID: "file_demo"}, nil
		}})
		m.store = &openai.VectorStore{ID: "vs_demo"}
		m.enter("upload")
		m.Update(tea.PasteMsg{Content: path + ending})
		require.Equal(t, path, m.editor.Value())
		modelKey(m, tea.KeyEnter)
		require.Empty(t, uploaded)
		modelComplete(t, m, modelKey(m, tea.KeyEnter))
		require.Equal(t, path, uploaded)
	}
}

func TestReopenedUploadShowsNextAction(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 32}, {Width: 40, Height: 12}} {
		m := modelForTest(t, modelService{})
		m.Update(size)
		m.store = &openai.VectorStore{ID: "vs_demo"}
		m.pendingFile, m.pendingName = "file_demo", "moonflower.txt"
		for _, attached := range []bool{false, true} {
			m.pendingAttached = attached
			m.enter("upload")
			view := ansi.Strip(m.View().Content)
			require.Contains(t, view, "moonflower.txt")
			if attached {
				require.Contains(t, view, "› Resume indexing")
				require.Contains(t, view, "Enter check indexing")
			} else {
				require.Contains(t, view, "› Attach uploaded file")
				require.Contains(t, view, "Enter check attachment")
			}
			require.NotContains(t, view, "review upload")
		}
	}
}

func TestIndexedUploadShowsFilenameAndClearsOldDraft(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{}
	require.NoError(t, json.Unmarshal([]byte(`{"id":"vs_demo","file_counts":{"total":0}}`), m.store))
	m.stores = []openai.VectorStore{*m.store}
	m.uploadPath = "/tmp/moonflower.txt"
	m.pendingFile, m.pendingName = "file_demo", "moonflower.txt"
	m.enter("upload")
	m.complete(workMessage{kind: "index", attached: &openai.VectorStoreFile{ID: "file_demo", Status: "completed"}})
	require.Equal(t, "home", m.page)
	require.True(t, m.editing())
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "Ready to search.")
	require.Contains(t, view, "moonflower.txt")
	require.NotContains(t, view, "Files   0")
	require.False(t, m.stores[0].FileCounts.JSON.Total.Valid())
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	require.Contains(t, ansi.Strip(m.View().Content), "moonflower.txt")
	m.enter("upload")
	require.Empty(t, m.editor.Value())
}

func TestUploadMenuFollowsArrowOrder(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 32}, {Width: 40, Height: 12}} {
		m := modelForTest(t, modelService{})
		m.Update(size)
		m.enter("upload")
		m.Update(tea.PasteMsg{Content: "notes.txt"})
		for _, label := range []string{"Browse files", "Upload", "Back", "Options"} {
			modelKey(m, tea.KeyDown)
			require.Contains(t, ansi.Strip(m.View().Content), "› "+label)
			require.False(t, m.busy)
		}
		for range 4 {
			modelKey(m, tea.KeyUp)
		}
		require.True(t, m.editing())
		require.Equal(t, "notes.txt", m.editor.Value())
		modelKey(m, tea.KeyEnter)
		require.Contains(t, ansi.Strip(m.View().Content), "› Upload")
		require.False(t, m.busy)
	}
}

func TestBrowseFindAndSelectFile(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".cache", "Documents"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, name), 0700))
	}
	for i := range 25 {
		require.NoError(t, os.WriteFile(filepath.Join(root, fmt.Sprintf("Notes-%02d.txt", i)), nil, 0600))
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 32}, {Width: 40, Height: 12}} {
		m := modelForTest(t, modelService{})
		m.Update(size)
		m.enter("browse")
		modelComplete(t, m, m.readDirectory(root))
		require.Len(t, m.entries, 26)
		view := func() string { return ansi.Strip(m.View().Content) }
		require.Contains(t, view(), "› Documents/")
		require.NotContains(t, view(), ".cache")
		require.Contains(t, view(), "/ find")
		modelKey(m, '/')
		m.Update(tea.PasteMsg{Content: "NOTES-"})
		require.Len(t, m.entries, 25)
		modelKey(m, tea.KeyPgDown)
		require.Greater(t, m.selected, 0)
		require.Contains(t, view(), "› "+m.entries[m.selected].name)
		modelKey(m, tea.KeyEsc)
		require.Equal(t, "browse", m.page)
		modelKey(m, '/')
		m.Update(tea.PasteMsg{Content: "24"})
		require.Contains(t, view(), "Notes-24.txt")
		modelKey(m, tea.KeyEnter)
		require.Equal(t, "upload", m.page)
		require.Equal(t, filepath.Join(root, "Notes-24.txt"), m.selectedUploadPath)
		require.Equal(t, 2, m.focus)
		require.False(t, m.busy)
	}
}

func TestBrowseHiddenFolderAndEmptyFilter(t *testing.T) {
	root := t.TempDir()
	hidden := filepath.Join(root, ".sample")
	require.NoError(t, os.Mkdir(hidden, 0700))
	m := modelForTest(t, modelService{})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.enter("browse")
	modelComplete(t, m, m.readDirectory(root))
	view := func() string { return ansi.Strip(m.View().Content) }
	require.Contains(t, view(), "Only hidden entries")
	modelKey(m, '/')
	m.Update(tea.PasteMsg{Content: "missing"})
	require.Contains(t, view(), "No names match")
	modelKey(m, tea.KeyEnter)
	require.True(t, m.editing())
	modelKey(m, tea.KeyEsc)
	modelKey(m, '/')
	m.Update(tea.PasteMsg{Content: "."})
	require.Contains(t, view(), ".sample/")
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, hidden, m.directory)
	require.Empty(t, m.browserFilter)
	require.Contains(t, view(), "This folder is empty.")
	require.Contains(t, view(), "› Parent folder")
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, root, m.directory)
	require.Empty(t, m.entries)
}

func TestBrowserActionsRemainVisibleAfterReadError(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.enter("browse")
	m.entries = []browserEntry{{name: "notes.txt"}}
	m.note = "Could not read this folder.\nChoose another folder or go back."
	modelKey(m, tea.KeyTab)
	require.Contains(t, ansi.Strip(m.View().Content), "› Parent folder")
	modelKey(m, tea.KeyDown)
	require.Contains(t, ansi.Strip(m.View().Content), "› Back")
}
