package vectorstores

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWideSearchShowsDraftAndActionsBesideStore(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_synthetic", Name: "Demo", Status: "completed"}
	m.enter("home")
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 16})
	draft := strings.Repeat("a", 52) + "Z"
	m.Update(tea.PasteMsg{Content: draft})
	modelKey(m, tea.KeyLeft)
	for _, width := range []int{96, 80, 120, 96} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 16})
		// The virtual cursor uses reverse video for its current character.
		assert.Contains(t, m.View().Content, "\x1b[7mZ", "cursor at %d columns", width)
	}
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.Equal(t, strings.TrimSuffix(draft, "Z")+"qZ", m.editor.Value())

	modelKey(m, tea.KeyDown)
	for _, action := range []string{"Search", "Upload file", "Files", "Switch store", "Manage store"} {
		left, right := searchPaneText(m.View().Content)
		assert.Contains(t, left, "› "+action)
		assert.Contains(t, left, "Manage store")
		assert.Contains(t, right, "vs_synthetic")
		assert.Contains(t, right, "completed")
		modelKey(m, tea.KeyRight)
	}
	assert.False(t, m.busy)
}

func TestSearchQuestionWrapsWithoutChangingItsValue(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo", Name: "Demo"}
	m.enter("home")
	m.Update(tea.WindowSizeMsg{Width: 122, Height: 32})

	first := "Where do volunteers meet for the moonflower"
	m.Update(tea.PasteMsg{Content: first + " walk?"})
	left, right := searchPaneText(m.View().Content)
	rows := strings.Split(left, "\n")
	for i := range rows {
		rows[i] = strings.TrimSpace(rows[i])
	}
	require.Contains(t, strings.Join(rows, "\n"), "› "+first+"\nwalk?\n")
	require.Contains(t, right, "vs_demo")
	require.Equal(t, first+" walk?", m.editor.Value())

	for range len(" walk?") {
		modelKey(m, tea.KeyBackspace)
	}
	left, _ = searchPaneText(m.View().Content)
	require.Contains(t, left, first)
	require.Equal(t, 1, m.editor.Height())
}

func TestLongSearchQuestionKeepsCursorActionAndFooterVisible(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.enter("home")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	question := "Start " + strings.Repeat("moonflower walk ", 32) + "finish?"
	m.Update(tea.PasteMsg{Content: question})
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "finish?")
	require.Contains(t, view, "Actions")
	require.Contains(t, view, "Tab actions")
	require.LessOrEqual(t, len(strings.Split(view, "\n")), 12)

	modelKey(m, tea.KeyHome)
	require.Contains(t, ansi.Strip(m.View().Content), "Start")
	modelKey(m, tea.KeyTab)
	require.Contains(t, ansi.Strip(m.View().Content), "› Search")
	m.Update(tea.WindowSizeMsg{Width: 122, Height: 32})
	left, _ := searchPaneText(m.View().Content)
	require.Contains(t, left, "Start")
	require.Contains(t, left, "finish?")
	require.Equal(t, question, m.editor.Value())
}

func TestWideSearchKeepsSelectedPreviewAcrossResize(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_test", Name: "Demo"}
	m.submittedQuery, m.query = "synthetic ribbons", "synthetic ribbons"
	m.enter("home")
	var matches []openai.VectorStoreSearchResponse
	for i := range 17 {
		matches = append(matches, openai.VectorStoreSearchResponse{
			FileID: fmt.Sprintf("file_%02d", i), Filename: fmt.Sprintf("reference-%02d.txt", i),
			Score: 0.875,
			Content: []openai.VectorStoreSearchResponseContent{
				{Text: fmt.Sprintf("Ribbon number %02d.", i)}, {Text: "Last passage chunk."},
			},
		})
	}
	m.complete(workMessage{kind: "search", search: SearchPage{Results: matches, More: true}})
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 16})
	for range len(matches) - 1 {
		modelKey(m, tea.KeyDown)
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 96, Height: 16}, {Width: 95, Height: 16}, {Width: 140, Height: 15}, {Width: 140, Height: 30}} {
		m.Update(size)
		view := ansi.Strip(m.View().Content)
		assert.Contains(t, view, "› reference-16.txt")
		assert.Contains(t, view, "Ribbon number 16.")
		assert.Contains(t, view, "More matches exist")
		left, right := searchPaneText(view)
		if size.Width >= 96 && size.Height >= 16 {
			assert.Contains(t, left, "Query  synthetic ribbons")
			assert.Contains(t, left, "0.875")
			assert.Contains(t, left, "› reference-16.txt")
			assert.Contains(t, right, "Source  reference-16.txt")
			assert.Contains(t, right, "file_16")
			assert.Contains(t, right, "Ribbon number 16.")
			assert.NotContains(t, right, "Last passage chunk.")
		} else {
			assert.Empty(t, right)
		}
	}
	modelKey(m, tea.KeyEnter)
	require.Equal(t, "passage", m.page)
	assert.Contains(t, m.View().Content, "Last passage chunk.")
	modelKey(m, tea.KeyEscape)
	left, right := searchPaneText(m.View().Content)
	assert.Contains(t, left, "› reference-16.txt")
	assert.Contains(t, right, "Ribbon number 16.")
}

func TestSearchArrowsFollowVisibleActions(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo", Name: "Demo"}
	m.enter("home")
	m.Update(tea.WindowSizeMsg{Width: 52, Height: 16})
	view := ansi.Strip(m.View().Content)
	assert.Contains(t, view, "Search your files")
	assert.Contains(t, view, "› Type a question")
	assert.NotContains(t, view, "› Search your files")
	for _, name := range []string{"Search", "Upload file", "Files", "Switch store", "Manage store"} {
		assert.Contains(t, view, name)
	}
	m.Update(tea.PasteMsg{Content: "copper kite"})
	m.results = []openai.VectorStoreSearchResponse{{FileID: "file_demo"}}
	m.note = "Navigation should keep the active action visible when a notice takes two rows. " + strings.Repeat("long ", 20)
	modelKey(m, tea.KeyUp)
	require.True(t, m.editing())
	for _, size := range []tea.WindowSizeMsg{{Width: 96, Height: 16}, {Width: 80, Height: 24}, {Width: 52, Height: 16}, {Width: 40, Height: 12}} {
		m.Update(size)
		for _, name := range []string{"Search", "Upload file", "Files", "Switch store", "Manage store"} {
			modelKey(m, tea.KeyDown)
			view := ansi.Strip(m.View().Content)
			assert.Contains(t, view, "› "+name, "%dx%d", size.Width, size.Height)
			assert.Contains(t, view, "copper kite")
			assert.False(t, m.busy)
		}
		assert.Contains(t, ansi.Strip(m.View().Content), "Tab last results")
		modelKey(m, tea.KeyDown)
		require.Equal(t, matchListFocus, m.focus)
		modelKey(m, tea.KeyEscape)
		assert.True(t, m.editing())
		modelKey(m, tea.KeyDown)
		modelKey(m, tea.KeyUp)
		assert.True(t, m.editing())
	}
	assert.Equal(t, "copper kite", m.editor.Value())
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	assert.Equal(t, matchListFocus, m.focus)
}

func TestSearchTabHintsLeadToActionsAndResults(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.results = []openai.VectorStoreSearchResponse{{FileID: "file_demo"}}
	m.enter("home")
	require.Contains(t, m.searchFooter(), "Tab actions")
	modelKey(m, tea.KeyTab)
	require.Contains(t, ansi.Strip(m.View().Content), "› Search")
	require.False(t, m.busy)
	for range 4 {
		modelKey(m, tea.KeyTab)
	}
	require.Contains(t, m.searchFooter(), "Tab last results")
	modelKey(m, tea.KeyTab)
	require.Equal(t, matchListFocus, m.focus)
	require.Contains(t, m.searchFooter(), "Shift+Tab actions")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Contains(t, ansi.Strip(m.View().Content), "› Manage store")
}

func searchPaneText(view string) (string, string) {
	var left, right []string
	for _, row := range strings.Split(ansi.Strip(view), "\n") {
		cells := strings.Split(row, "│")
		if len(cells) == 4 {
			left = append(left, cells[1])
			right = append(right, cells[2])
		}
	}
	return strings.Join(left, "\n"), strings.Join(right, "\n")
}
