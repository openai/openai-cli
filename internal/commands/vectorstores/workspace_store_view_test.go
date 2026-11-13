package vectorstores

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorePickerInspectorFollowsSelection(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.stores = []openai.VectorStore{
		{ID: "vs_first", Name: "Reading list", Status: "completed"},
		{ID: "vs_second", Name: "Research", Status: "in_progress"},
	}
	m.stores[0].FileCounts.Total = 12
	m.stores[1].FileCounts.Total = 3
	for i, store := range m.stores {
		view := ansi.Strip(m.View().Content)
		assert.Contains(t, view, "Enter open store")
		assert.Contains(t, view, "Open this store to upload or view files.")
		assert.Contains(t, view, "Find store")
		assert.Less(t, strings.Index(view, "Store details"), strings.Index(view, "Actions"))
		assert.Contains(t, view, "› "+store.Name)
		assert.Equal(t, 1, strings.Count(view, store.ID))
		assert.Equal(t, 1, strings.Count(view, store.Name))
		assert.Contains(t, view, "Status  "+string(store.Status))
		assert.NotContains(t, view, m.stores[1-i].ID)
		if i == 0 {
			assert.Contains(t, view, "Files   12")
		} else {
			assert.Contains(t, view, "Files   3")
		}
		modelKey(m, tea.KeyDown)
	}
	view := ansi.Strip(m.View().Content)
	assert.Contains(t, view, "› Create store")
	assert.Contains(t, view, "Choose a name, then confirm to create it.")
	assert.Contains(t, view, "Enter choose name")
	assert.NotContains(t, view, "Store details")
}

func TestStorePickerFindShortcutAndEmptyFocus(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.stores = []openai.VectorStore{{ID: "vs_1", Name: "Quarterly/Review"}, {ID: "vs_2", Name: "Handbook"}}
	m.enter("stores")
	modelKey(m, tea.KeyDown)
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	require.Equal(t, "filter", m.page)
	require.True(t, m.editing())
	assert.Contains(t, m.View().Content, "Enter/Tab apply filter")
	assert.Contains(t, m.View().Content, "Esc cancel")
	require.Empty(t, m.editor.Value(), "the shortcut opens a clean filter")
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	require.Equal(t, "/", m.editor.Value(), "slashes remain ordinary text inside a filter")
	require.Len(t, m.filteredStores(), 1, "the list must update before pressing Enter")
	require.Empty(t, m.storeFilter, "typing must not commit the filter")
	require.False(t, m.busy)
	modelKey(m, tea.KeyEnter)
	require.Equal(t, "stores", m.page)
	require.Len(t, m.filteredStores(), 1)
	require.Zero(t, m.focus)
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	require.Equal(t, "/", m.editor.Value(), "reopening the filter restores the applied value")
	m.editor.SetValue("cancelled edit")
	modelKey(m, tea.KeyEscape)
	require.Equal(t, "/", m.storeFilter, "cancel keeps the previously applied filter")
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m.editor.SetValue("missing")
	modelKey(m, tea.KeyEnter)
	require.Equal(t, storeFinderFocus, m.focus, "an empty search stays accessible for editing")
	modelKey(m, tea.KeyTab)
	require.Equal(t, 1, m.focus, "skip the empty list on forward Tab")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, storeFinderFocus, m.focus, "skip the empty list on reverse Tab")
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m.editor.SetValue("")
	modelKey(m, tea.KeyEnter)
	require.Zero(t, m.focus)
	require.Empty(t, m.storeFilter)
	require.Len(t, m.filteredStores(), 2)
}

func TestHighlightedStoreFinderAcceptsFirstInput(t *testing.T) {
	for _, tc := range []struct {
		name, applied, want string
		input               tea.Msg
	}{
		{"q as text", "", "q", tea.KeyPressMsg{Code: 'q', Text: "q"}},
		{"unicode", "", "猫", tea.KeyPressMsg{Code: '猫', Text: "猫"}},
		{"pasted ID", "", "vs_notes", tea.PasteMsg{Content: "vs_notes"}},
		{"edit applied filter", "q", "qu", tea.KeyPressMsg{Code: 'u', Text: "u"}},
		{"backspace", "qu", "q", tea.KeyPressMsg{Code: tea.KeyBackspace}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := modelForTest(t, modelService{})
			m.stores = []openai.VectorStore{{ID: "vs_notes", Name: "Quarterly 猫 notes"}, {ID: "vs_other", Name: "Other"}}
			m.storeFilter = tc.applied
			m.enter("stores")
			modelKey(m, tea.KeyUp)
			require.Contains(t, m.View().Content, "Type to filter")
			m.Update(tc.input)
			require.False(t, m.quit)
			require.Equal(t, tc.want, m.editor.Value())
			require.Len(t, m.filteredStores(), 1)
			require.Equal(t, "vs_notes", m.filteredStores()[0].ID)
			modelKey(m, tea.KeyEscape)
			require.Equal(t, tc.applied, m.storeFilter)
			require.Equal(t, storeFinderFocus, m.focus)
		})
	}
}

func TestStorePickerTabHintsFollowFocus(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.stores = []openai.VectorStore{{ID: "vs_a", Name: "First"}, {ID: "vs_b", Name: "Second"}}
	m.storeNext = "vs_b"
	m.enter("stores")
	for _, step := range []struct{ hint, highlight string }{
		{"Tab actions", "Create store"},
		{"Tab next action", "Refresh stores"},
		{"Tab next action", "Load more stores"},
		{"Tab find store", "Find store"},
		{"Tab stores", "First"},
	} {
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, step.hint)
		require.LessOrEqual(t, len(m.footerLines(m.storeFooter())), 2)
		modelKey(m, tea.KeyTab)
		require.Contains(t, ansi.Strip(m.View().Content), "› "+step.highlight)
		require.False(t, m.busy)
	}
}

func TestStorePickerAdvertisesEveryActionAtEachWidth(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {96, 16}, {120, 32}} {
		m := modelForTest(t, modelService{})
		m.stores = []openai.VectorStore{{ID: "vs_one", Name: "One"}}
		m.storeNext = "next"
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, focus := range m.storeChoices() {
			modelKey(m, tea.KeyDown)
			view := ansi.Strip(m.View().Content)
			assert.Contains(t, view, "› "+storePickerActions[focus].label, "size %v", size)
			assert.Contains(t, view, "Enter "+storePickerActions[focus].enter, "size %v", size)
			assert.Contains(t, view, "Find store")
		}
	}
}

func TestStorePickerCancelFilterRestoresOriginalRow(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.stores = []openai.VectorStore{{ID: "vs_1", Name: "One"}, {ID: "vs_2", Name: "Two"}}
	m.enter("stores")
	modelKey(m, tea.KeyDown)
	for _, focus := range []int{0, 1} {
		if focus == 1 {
			modelKey(m, tea.KeyDown)
		}
		require.Equal(t, focus, m.focus)
		require.Equal(t, 1, m.selected)
		m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
		m.editor.SetValue("unsaved")
		modelKey(m, tea.KeyEscape)
		require.Equal(t, "stores", m.page)
		assert.Empty(t, m.storeFilter)
		assert.Equal(t, focus, m.focus)
		assert.Equal(t, 1, m.selected)
	}
}

func TestStoreFinderMatchesDisplayedUntitledName(t *testing.T) {
	for _, width := range []int{60, 120} {
		m := modelForTest(t, modelService{})
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m.stores = []openai.VectorStore{{ID: "vs_a", Name: "Notes"}, {ID: "vs_b"}}
		m.enter("stores")
		assert.Contains(t, m.View().Content, "Untitled store")
		m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
		m.Update(tea.PasteMsg{Content: "UNTITLED"})
		modelKey(m, tea.KeyEnter)
		require.Len(t, m.filteredStores(), 1)
		assert.Equal(t, "vs_b", m.filteredStores()[m.selected].ID)
		assert.Contains(t, m.View().Content, "› Untitled store")
	}
}

func TestStorePickerDistinguishesDuplicateNamesAtSplitWidth(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 16})
	prefix := "vs_" + strings.Repeat("a", 31)
	m.stores = []openai.VectorStore{
		{ID: prefix + "1", Name: strings.Repeat("参考", 30)},
		{ID: prefix + "2", Name: strings.Repeat("参考", 30)},
	}
	for _, store := range m.stores {
		view := ansi.Strip(m.View().Content)
		assert.Contains(t, view, store.ID, "the inspector keeps the complete ID")
		assert.Contains(t, view, "aaa1 ", "both ID endings remain visible")
		assert.Contains(t, view, "aaa2 ")
		assert.Contains(t, view, "参考")
		assert.NotContains(t, view, strings.Repeat("参考", 30))
		modelKey(m, tea.KeyDown)
	}
	m.storeFilter = "absent"
	m.enter("stores")
	view := ansi.Strip(m.View().Content)
	assert.Contains(t, view, "No matching stores.")
	assert.Contains(t, view, "0 matches")
	assert.Equal(t, storeFinderFocus, m.focus)
	assert.NotContains(t, view, "Store details")
}

func TestStoreFinderArrowsAndPagination(t *testing.T) {
	calls := 0
	m := modelForTest(t, modelService{listStores: func(_ context.Context, cursor string) (StorePage, error) {
		calls++
		assert.Equal(t, "vs_a", cursor)
		return StorePage{Stores: []openai.VectorStore{{ID: "vs_b", Name: "Two"}}}, nil
	}})
	m.stores = []openai.VectorStore{{ID: "vs_a", Name: "One"}}
	m.storeNext = "vs_a"
	m.enter("stores")
	modelKey(m, tea.KeyUp)
	require.Equal(t, storeFinderFocus, m.focus)
	modelKey(m, tea.KeyDown)
	require.Zero(t, m.focus)
	modelKey(m, tea.KeyUp)
	modelKey(m, tea.KeyEnter)
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.False(t, m.quit)
	require.Equal(t, "q", m.editor.Value())
	m.editor.SetValue("VS_B")
	modelKey(m, tea.KeyDown)
	require.Equal(t, "VS_B", m.storeFilter)
	require.Equal(t, storeFinderFocus, m.focus)
	for _, action := range []int{1, 2, 3} {
		modelKey(m, tea.KeyDown)
		require.Equal(t, action, m.focus)
	}
	require.Zero(t, calls)
	modelComplete(t, m, modelKey(m, tea.KeyEnter))
	require.Equal(t, 1, calls)
	require.Zero(t, m.focus)
	require.Len(t, m.filteredStores(), 1)
	require.Equal(t, "vs_b", m.filteredStores()[m.selected].ID)
}

func TestStoreInspectorShowsReportedZeroFileCount(t *testing.T) {
	m := modelForTest(t, modelService{})
	for _, tc := range []struct{ json, expected string }{
		{`{"id":"vs_demo","file_counts":{"total":0}}`, "Files   0"},
		{`{"id":"vs_demo"}`, "Files   Unknown"},
	} {
		var store openai.VectorStore
		require.NoError(t, json.Unmarshal([]byte(tc.json), &store))
		view := strings.Join(m.storeInspector(&store), "\n")
		assert.Contains(t, view, tc.expected)
	}
}

func TestStorePickerEmptyStatesAtEachSize(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {52, 16}, {80, 24}, {96, 16}} {
		m := modelForTest(t, modelService{})
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.enter("stores")
		assert.Contains(t, m.View().Content, "No stores loaded.", "size %v", size)
		m.stores = []openai.VectorStore{{ID: "vs_one", Name: "One"}}
		m.storeNext = "next"
		m.storeFilter = "missing"
		m.enter("stores")
		for _, focus := range m.storeChoices() {
			modelKey(m, tea.KeyDown)
			view := ansi.Strip(m.View().Content)
			assert.Contains(t, view, "No matching stores.", "size %v", size)
			assert.NotContains(t, view, "No items yet.")
			assert.Contains(t, view, "› "+storePickerActions[focus].label, "size %v", size)
		}
	}
}
