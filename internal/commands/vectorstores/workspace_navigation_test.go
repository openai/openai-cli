package vectorstores

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreArrowsReachEveryVisibleAction(t *testing.T) {
	for _, more := range []bool{false, true} {
		m := modelForTest(t, modelService{})
		m.stores = []openai.VectorStore{{ID: "vs_one", Name: "One"}, {ID: "vs_two", Name: "Two"}}
		if more {
			m.storeNext = "vs_two"
		}
		modelKey(m, tea.KeyDown)
		assert.Equal(t, 1, m.selected)
		for _, focus := range m.storeChoices() {
			modelKey(m, tea.KeyDown)
			assert.Equal(t, focus, m.focus)
			modelKey(m, tea.KeyRight)
			assert.Equal(t, focus, m.focus, "vertical menus do not expose hidden horizontal actions")
			assert.Contains(t, ansi.Strip(m.View().Content), "›")
		}
		for range len(m.storeChoices()) {
			modelKey(m, tea.KeyUp)
		}
		assert.Zero(t, m.focus)
		assert.Equal(t, 1, m.selected)
	}
}

func TestSearchResultFooterMatchesKeys(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.enter("home")
	m.results = []openai.VectorStoreSearchResponse{{Filename: "first.txt"}, {Filename: "second.txt"}}
	m.searched, m.focus = true, 6
	view := m.View().Content
	assert.Contains(t, view, "↑↓ result")
	assert.Contains(t, view, "Enter read")
	modelKey(m, tea.KeyDown)
	assert.Equal(t, 1, m.resultSelected)
	modelKey(m, tea.KeyTab)
	assert.True(t, m.editing())
}

func TestEmptyStoresStartOnCreate(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.enter("stores")
	assert.Contains(t, m.View().Content, "› Create store")
	modelKey(m, tea.KeyEnter)
	assert.Equal(t, "create", m.page)
}

func TestSearchKeepsSelectedMatchAndDraft(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.query, m.submittedQuery = "copper kite", "copper kite"
	m.enter("home")
	m.results = []openai.VectorStoreSearchResponse{
		{Filename: "first.txt"},
		{FileID: "file_second", Content: []openai.VectorStoreSearchResponseContent{
			{Text: ""}, {Text: "Seven\nblue ribbons.\n"},
		}},
	}
	m.searched, m.focus, m.resultSelected = true, matchListFocus, 1
	assert.Contains(t, m.View().Content, "Seven blue ribbons.")
	modelKey(m, tea.KeyEnter)
	require.Equal(t, "passage", m.page)
	assert.Contains(t, m.View().Content, "file_second")
	modelKey(m, tea.KeyEscape)
	assert.Equal(t, matchListFocus, m.focus)
	assert.Equal(t, 1, m.resultSelected)
	modelKey(m, tea.KeyEscape)
	require.True(t, m.editing())
	m.Update(tea.PasteMsg{Content: " color"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, matchListFocus, m.focus)
	assert.Contains(t, m.View().Content, "Query  copper kite")
	assert.NotContains(t, m.View().Content, "color")
	modelKey(m, tea.KeyEscape)
	assert.Equal(t, "copper kite color", m.editor.Value())
}

func TestSearchEmptyPageWithMoreDoesNotClaimZeroMatches(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.query, m.submittedQuery = "kite", "kite"
	m.enter("home")
	m.focus = 1
	m.complete(workMessage{kind: "search", search: SearchPage{More: true}})
	require.True(t, m.editing())
	view := m.View().Content
	assert.Contains(t, view, "No matches on this page.")
	assert.Contains(t, view, "More matches exist")
	m.Update(tea.PasteMsg{Content: " draft"})
	assert.NotContains(t, m.View().Content, "No matches on this page.")
}

func TestLastStorePageKeepsVisibleSelection(t *testing.T) {
	m := modelForTest(t, modelService{listStores: func(context.Context, string) (StorePage, error) {
		return StorePage{Stores: []openai.VectorStore{{ID: "vs_last", Name: "Last"}}}, nil
	}})
	m.storeNext, m.focus = "vs_first", 3
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	assert.Empty(t, m.storeNext)
	assert.Zero(t, m.focus)
	assert.Contains(t, m.View().Content, "› Last")
	assert.NotContains(t, m.View().Content, "Load more stores")
}

func TestWorkspaceActionArrowsPreserveDraft(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo", Name: "Demo"}
	m.enter("home")
	m.Update(tea.PasteMsg{Content: "copper kite"})
	modelKey(m, tea.KeyDown)
	modelKey(m, tea.KeyRight)
	assert.Equal(t, 2, m.focus)
	assert.Contains(t, ansi.Strip(m.View().Content), "› Upload file")
	modelKey(m, tea.KeyEnter)
	assert.Equal(t, "upload", m.page)
	modelKey(m, tea.KeyEscape)
	assert.Equal(t, "copper kite", m.editor.Value())
	modelKey(m, tea.KeyLeft)
	assert.Zero(t, m.focus, "editing arrows retain text cursor control")
}

func TestMutationCooldownDoesNotSwallowDeliberateSearch(t *testing.T) {
	calls := 0
	m := modelForTest(t, modelService{search: func(context.Context, string, string) (SearchPage, error) {
		calls++
		return SearchPage{}, nil
	}})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.enter("home")
	m.lastMutation = time.Now()
	m.Update(tea.PasteMsg{Content: "copper kite"})
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	assert.Equal(t, 1, calls)
	m.lastMutation = time.Now()
	modelKey(m, tea.KeyDown)
	modelKey(m, tea.KeyRight)
	modelKey(m, tea.KeyEnter)
	assert.Equal(t, "upload", m.page)
}

func TestWorkspaceFeedbackAndFooterStayLocal(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.enter("home")
	m.note = "Enter a search question."
	modelKey(m, tea.KeyEscape)
	assert.Empty(t, m.note)
	m.enterWithNote("stores", "Store deleted. Source files remain in Files storage.")
	assert.Contains(t, m.View().Content, "Source files remain")
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	for i, line := range lines {
		if strings.Contains(line, "└") {
			require.Positive(t, i)
			assert.Contains(t, lines[i-1], "↑↓ choose", "footer sits immediately inside the bottom border")
			assert.Equal(t, len(lines)-1, i)
			return
		}
	}
	t.Fatal("missing bottom border")
}
