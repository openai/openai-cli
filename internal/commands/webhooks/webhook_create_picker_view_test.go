package webhooks

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestWebhookCreatePickerSearchRetainsSelectionsAndUnknownNames(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed", "new.family.event", "response.failed", "batch.completed", "new.family.event"})
	m.url.insert("https://example.invalid/webhook")
	m.stage = 2
	require.Len(t, m.events, 4)
	webhookCreatePaste(m, "responses")
	require.Equal(t, []string{"response.completed", "response.failed"}, m.filteredEvents())
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	require.True(t, m.selected["response.completed"])
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	require.True(t, m.selected["response.failed"])
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	webhookCreatePaste(m, "OTHER")
	require.Equal(t, []string{"new.family.event"}, m.filteredEvents())
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	require.Equal(t, []string{"response.completed", "response.failed", "new.family.event"}, m.settings().events)
	webhookCreatePaste(m, "missing")
	require.Empty(t, m.filteredEvents())
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	require.Len(t, m.settings().events, 3, "no matches must not alter selected events")
	require.Contains(t, m.View().Content, "No matches")
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 3, m.stage, "search must not hide the retained selections from confirmation")
	require.Contains(t, m.View().Content, "new.family.event")
}

func TestWebhookCreatePickerFocusesCategoryAndSearchAsSeparateFields(t *testing.T) {
	m := newWebhookCreatePicker([]string{"batch.completed", "response.completed"})
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	require.Contains(t, ansi.Strip(m.View().Content), "› Events")
	require.Nil(t, m.View().Cursor)
	webhookCreateKey(m, tea.KeyRight)
	require.Equal(t, []string{"batch.completed"}, m.filteredEvents())
	webhookCreateKey(m, tea.KeyDown)
	require.Contains(t, ansi.Strip(m.View().Content), "› / Find event")
	require.Contains(t, ansi.Strip(m.View().Content), "Event or group name")
	require.NotNil(t, m.View().Cursor, "an empty search must show where typing starts")
	webhookCreatePaste(m, "batch")
	category := m.category
	webhookCreateKey(m, tea.KeyLeft)
	require.Equal(t, 4, m.search.cursor)
	require.Equal(t, category, m.category, "search arrows edit text instead of changing categories")
	webhookCreateKey(m, tea.KeyRight)
	m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Equal(t, "batch ", string(m.search.value))
	require.Empty(t, m.settings().events, "a search space must not select an event")
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	require.Zero(t, m.search.cursor, "search retains ordinary Ctrl+A editing")
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	require.Empty(t, m.search.value)
	webhookCreateKey(m, tea.KeyDown)
	require.Contains(t, ansi.Strip(m.View().Content), "› [ ] batch.completed")
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	require.Equal(t, []string{"batch.completed"}, m.settings().events)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Contains(t, ansi.Strip(m.View().Content), "› / Find event")
	webhookCreateKey(m, tea.KeyUp)
	require.Contains(t, ansi.Strip(m.View().Content), "› Events")
	webhookCreateKey(m, tea.KeyUp)
	require.Equal(t, 1, m.stage)
	require.False(t, m.confirmed)
}

func TestWebhookEventFinderUsesSlashAndKeepsSubscriptions(t *testing.T) {
	m := newWebhookCreatePicker([]string{"batch.completed", "response.completed", "response.failed"})
	slash := tea.KeyPressMsg{Code: '/', Text: "/"}
	m.Update(slash)
	require.Equal(t, "/", string(m.name.value))
	webhookCreateKey(m, tea.KeyTab)
	m.Update(slash)
	require.Equal(t, "/", string(m.url.value))
	webhookCreateKey(m, tea.KeyTab)
	m.Update(slash)
	require.Equal(t, webhookCreateSearch, m.eventFocus)
	require.Empty(t, m.search.value)

	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	for _, line := range lines {
		switch {
		case strings.Contains(line, "Events"), strings.Contains(line, "/ Find event"):
			require.Equal(t, 2, strings.Count(line, "│"), "only the outer frame borders the event rows")
		case strings.Contains(line, "Name"), strings.Contains(line, "URL"):
			require.Equal(t, 3, strings.Count(line, "│"))
		}
	}
	webhookCreatePaste(m, "completed")
	require.Equal(t, []string{"batch.completed", "response.completed"}, m.filteredEvents())
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	m.Update(slash)
	webhookCreateKey(m, tea.KeyLeft)
	m.Update(slash)
	require.Equal(t, "complete/d", string(m.search.value))
	require.Empty(t, m.filteredEvents())
	webhookCreateKey(m, tea.KeyEscape)
	require.False(t, m.canceled)
	require.Empty(t, m.search.value)
	require.True(t, m.selected["response.completed"])
	webhookCreateKey(m, tea.KeyEscape)
	require.True(t, m.canceled)
}

func TestWebhookExpandKeepsDraftAndRevealsMoreMatches(t *testing.T) {
	var events []string
	for i := range 40 {
		events = append(events, fmt.Sprintf("batch.event_%02d", i))
	}
	m := newWebhookCreatePicker(events)
	m.width, m.height, m.stage = 100, 32, 2
	webhookCreatePaste(m, "batch.event")
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	settings, current := m.settings(), m.current
	compact := m.View()
	require.False(t, compact.AltScreen)
	require.Contains(t, compact.Content, "Ctrl+G expand")
	toggle := tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
	m.Update(toggle)
	full := m.View()
	require.False(t, full.AltScreen, "expanding keeps shell history in the primary buffer")
	lines := strings.Split(full.Content, "\n")
	require.Len(t, lines, m.height)
	require.Contains(t, lines[len(lines)-1], "Ctrl+G compact")
	require.Greater(t, strings.Count(full.Content, "[ ]"), strings.Count(compact.Content, "[ ]"))
	require.Equal(t, current, m.current)
	m.Update(toggle)
	require.Equal(t, compact.Content, m.View().Content)
	require.Equal(t, settings, m.settings())
	m.Update(toggle)
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.True(t, m.canceled)
	require.False(t, m.View().AltScreen)
}

func TestWebhookExpandedEventListPinsActionsAndFooter(t *testing.T) {
	m := newWebhookCreatePicker([]string{"batch.completed", "batch.failed"})
	m.width, m.height, m.stage, m.fullScreen = 122, 32, 2, true
	m.browser = &webhookEndpointBrowser{}
	check := func(height int) {
		t.Helper()
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		require.Len(t, lines, height)
		require.Contains(t, lines[2], "Name")
		require.Contains(t, lines[3], "URL")
		require.Contains(t, lines[6], "/ Find event")
		require.Empty(t, strings.Trim(lines[height-5], " │"), "the spare row belongs to Events")
		require.True(t, strings.HasPrefix(lines[height-4], "├"))
		require.Contains(t, lines[height-3], "[ Review ]")
		require.True(t, strings.HasPrefix(lines[height-2], "╰"))
		require.Contains(t, lines[height-1], "Ctrl+G compact")
	}
	check(32)
	webhookCreatePaste(m, "missing event")
	check(32)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	check(24)
	m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	require.Less(t, len(strings.Split(m.View().Content, "\n")), m.height)
}

func TestWebhookCreatePickerSelectAllOnlyChangesMatchingEvents(t *testing.T) {
	m := newWebhookCreatePicker([]string{"batch.completed", "response.completed", "response.failed"})
	m.stage = 2
	m.selected["batch.completed"] = true
	webhookCreatePaste(m, "response")
	webhookCreateKey(m, tea.KeyDown)
	selectAll := tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
	m.Update(selectAll)
	require.Len(t, m.settings().events, 3)
	require.False(t, m.confirmed)
	require.False(t, m.confirmYes)
	m.Update(selectAll)
	require.Equal(t, []string{"batch.completed"}, m.settings().events, "hidden selections must survive clearing matches")
	webhookCreatePaste(m, "-no-matches")
	webhookCreateKey(m, tea.KeyDown)
	m.Update(selectAll)
	require.Equal(t, []string{"batch.completed"}, m.settings().events)
	m.stage = 0
	m.name.insert("Original")
	m.Update(selectAll)
	require.Equal(t, 0, m.name.cursor, "Ctrl+A in a text field must retain its editing meaning")
	require.Equal(t, "Original", string(m.name.value))
}
