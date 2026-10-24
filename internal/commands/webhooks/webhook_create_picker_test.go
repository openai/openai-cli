package webhooks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
)

func webhookCreateKey(m *webhookCreatePicker, code rune) {
	m.Update(tea.KeyPressMsg{Code: code})
}

func webhookCreatePaste(m *webhookCreatePicker, text string) {
	m.Update(tea.PasteMsg{Content: text})
}

func TestWebhookCreatePickerRepaintsPromptMarkKeys(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter}, {Code: tea.KeyTab}, {Code: tea.KeyTab, Mod: tea.ModShift},
		{Code: 'd', Mod: tea.ModCtrl},
	} {
		t.Run(key.String(), func(t *testing.T) {
			m := newWebhookCreatePicker([]string{"response.completed"})
			m.clearPromptMarks = true
			_, command := m.Update(key)
			require.NotNil(t, command)
			require.IsType(t, tea.ClearScreen(), command())
		})
	}
	for _, clear := range []bool{false, true} {
		m := newWebhookCreatePicker([]string{"response.completed"})
		m.clearPromptMarks = clear
		_, command := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
		require.Nil(t, command, "ordinary typing retains incremental rendering")
		require.Equal(t, "x", m.settings().name)
	}
}

func TestWebhookCreatePickerTextCursorsStayBelowEraseOrigin(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 16}, {36, 10}} {
		for _, stage := range []int{0, 1, 2} {
			m := newWebhookCreatePicker([]string{"response.completed"})
			m.width, m.height, m.stage = size[0], size[1], stage
			m.eventFocus = webhookCreateSearch
			view := m.View()
			require.NotNil(t, view.Cursor)
			require.Greater(t, view.Cursor.Y, 0, "ED0 preserves marks on its first row")
			require.False(t, view.AltScreen, "the form stays inline")
		}
	}
}

func webhookCreateReview(t *testing.T, m *webhookCreatePicker) {
	t.Helper()
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 1, m.stage)
	webhookCreatePaste(m, "https://example.invalid/webhook")
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 2, m.stage)
	webhookCreateKey(m, tea.KeyDown) // Search.
	webhookCreateKey(m, tea.KeyDown) // Event choices.
	webhookCreateKey(m, ' ')
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 3, m.stage)
}

func TestWebhookCreatePickerConfirmationDefaultsToNo(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		m := newWebhookCreatePicker([]string{"response.completed"})
		webhookCreateReview(t, m)
		require.False(t, m.confirmYes)
		require.Contains(t, m.View().Content, "[No]")
		webhookCreatePaste(m, "yes\n")
		require.False(t, m.confirmYes, "pasted text must not consent")
		if confirm {
			webhookCreateKey(m, tea.KeyRight)
		}
		webhookCreateKey(m, tea.KeyEnter)
		require.Equal(t, confirm, m.confirmed)
		require.Equal(t, !confirm, m.canceled)
		require.NotContains(t, m.View().Content, "Create endpoint now?")
		before := m.settings()
		webhookCreateKey(m, tea.KeyEnter)
		webhookCreatePaste(m, "queued text")
		require.Equal(t, before, m.settings())
		require.Equal(t, confirm, m.confirmed)
	}
}

func TestWebhookCreatePickerRequiresFieldsAndEvents(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed"})
	require.Contains(t, m.View().Content, "Webhook")
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, "Webhook", m.settings().name)
	for _, invalid := range []string{"", "example.invalid/hook", "http://example.invalid/hook", "https://", "https://user:password@example.invalid/hook", "https://example.invalid/#fragment"} {
		m.url = webhookCreateInput{}
		webhookCreatePaste(m, invalid)
		webhookCreateKey(m, tea.KeyEnter)
		require.Equal(t, 1, m.stage, invalid)
		require.NotEmpty(t, m.note)
	}
	m.url = webhookCreateInput{}
	webhookCreatePaste(m, "https://example.invalid/hook")
	webhookCreateKey(m, tea.KeyEnter)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 2, m.stage)
	require.Contains(t, m.note, "Select at least one")
	webhookCreateKey(m, ' ')
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 3, m.stage)
	webhookCreateKey(m, tea.KeyRight)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, 2, m.stage)
	webhookCreateKey(m, tea.KeyEnter)
	require.False(t, m.confirmYes, "returning to review resets confirmation")
}

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

func TestWebhookCreatePickerCancellationAndTinyTerminal(t *testing.T) {
	for _, stage := range []int{0, 1, 2, 3} {
		for _, msg := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'c', Mod: tea.ModCtrl}} {
			m := newWebhookCreatePicker([]string{"response.completed"})
			m.stage = stage
			m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
			require.Contains(t, m.View().Content, "Resize")
			webhookCreateKey(m, tea.KeyEnter)
			require.Equal(t, stage, m.stage)
			m.Update(msg)
			require.True(t, m.canceled)
			require.False(t, m.confirmed)
		}
	}
	m := newWebhookCreatePicker([]string{"response.completed"})
	webhookCreateReview(t, m)
	webhookCreateKey(m, tea.KeyRight)
	webhookCreateKey(m, tea.KeyEnter)
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.False(t, m.confirmed, "cancellation can still stop a queued submission")
}

func TestWebhookCreatePickerInputAndControlSafety(t *testing.T) {
	m := newWebhookCreatePicker([]string{"future.\x1b[31mred", "new.\u202eevil", "new.\nline"})
	webhookCreatePaste(m, "猫")
	webhookCreateKey(m, tea.KeyLeft)
	webhookCreatePaste(m, "@")
	require.Equal(t, "@猫", m.settings().name)
	for _, text := range []string{"\x1b[31m", "two\nlines", "a\tb", "\x00", "\xff"} {
		before := m.settings().name
		webhookCreatePaste(m, text)
		require.Equal(t, before, m.settings().name)
		require.Contains(t, m.note, "one line")
	}
	m.stage = 2
	content := m.View().Content
	require.NotContains(t, content, "\x1b")
	require.NotContains(t, content, "\u202e")
	require.Contains(t, content, `\u001b`)
	require.Contains(t, content, `\u202e`)
	require.Contains(t, content, `\n`)
	for _, event := range m.events {
		m.selected[event] = true
	}
	require.ElementsMatch(t, []string{"future.\x1b[31mred", "new.\u202eevil", "new.\nline"}, m.settings().events)
}

func TestWebhookCreatePickerResizeKeepsAllChoicesAccessible(t *testing.T) {
	events := []string{"agent.session.created", "batch.completed", "response.completed", "eval.run.succeeded", "fine_tuning.job.succeeded", "realtime.call.incoming", "video.completed", "safety.alert.created", "future.event"}
	m := newWebhookCreatePicker(events)
	m.stage = 2
	m.eventFocus = webhookCreateChoices
	for _, size := range [][2]int{{80, 24}, {40, 12}, {36, 10}, {100, 30}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for range len(events) {
			content := ansi.Strip(m.View().Content)
			require.Contains(t, content, "› [ ] "+m.filteredEvents()[m.current], "the focused event must stay visible after scrolling")
			lines := strings.Split(content, "\n")
			require.LessOrEqual(t, len(lines), size[1]-1)
			for _, line := range lines {
				require.LessOrEqual(t, ansi.StringWidth(line), size[0]-2)
			}
			webhookCreateKey(m, ' ')
			webhookCreateKey(m, tea.KeyDown)
		}
		m.current = 0
		m.reviewFocused = false
		m.selected = map[string]bool{}
	}
}

func TestWebhookCreatePickerReviewCanScrollCompleteURLAndSelections(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed", "future.event"})
	m.stage, m.width, m.height = 3, 40, 12
	url := "https://example.invalid/" + strings.Repeat("path", 30) + "/end-marker"
	m.url.insert(url)
	for _, event := range m.events {
		m.selected[event] = true
	}
	// Reassemble every displayed review row by its scroll position. A complete
	// URL can cross physical lines, including the final identifying marker.
	rows := map[int]string{}
	for range 20 {
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "│ ") {
				lines[i] = strings.TrimRight(strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │"), " ")
			}
		}
		scrollHint := -1
		for i := 2; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], "↑↓ settings ") {
				scrollHint = i
				break
			}
			index := m.reviewOffset + i - 2
			if previous, exists := rows[index]; exists {
				require.Equal(t, previous, lines[i], "review rows must remain stable while scrolling")
			}
			rows[index] = lines[i]
		}
		require.Greater(t, scrollHint, 1, "the review must show content and its scroll range")
		webhookCreateKey(m, tea.KeyDown)
	}
	var complete strings.Builder
	for i := range len(rows) {
		row, exists := rows[i]
		require.True(t, exists, "every review row must remain accessible")
		complete.WriteString(row)
	}
	require.Equal(t, "Name: WebhookURL: "+url+"Events:  response.completed  future.event", complete.String())
	require.False(t, m.confirmYes)
}

func TestWebhookCreatePickerCanceledBeforeTerminalSetup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, confirmed, err := runWebhookCreatePicker(ctx, nil, nil, []string{"response.completed"})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, confirmed)
}

func TestWebhookCreateURLValidation(t *testing.T) {
	for _, valid := range []string{"https://example.invalid", "https://example.invalid/path?x=1", "https://[2001:db8::1]:8443/webhook"} {
		require.NoError(t, validateWebhookCreateURL(valid))
	}
	for _, invalid := range []string{"http://example.invalid", "HTTPS://example.invalid/hook", "Https://example.invalid/hook", "https:///path", "https:opaque", "https://bad host/", "https://host:invalid/", "https://user@host/", "https://host/#fragment", "https://host/" + strings.Repeat("x", 2048)} {
		require.Error(t, validateWebhookCreateURL(invalid), invalid)
	}
}

func TestWebhookCreateInputViewportKeepsCursorVisible(t *testing.T) {
	for _, value := range []string{
		"https://example.invalid/" + strings.Repeat("long-path/", 20) + "tail-marker",
		strings.Repeat("猫🙂", 30) + "終点",
		strings.Repeat("a\u0301", 30) + "ending",
	} {
		input := webhookCreateInput{}
		require.True(t, input.insert(value))
		for _, width := range []int{1, 2, 3, 8, 16, 32, 60} {
			for cursor := 0; cursor <= len(input.value); cursor++ {
				input.cursor = cursor
				view, column := input.viewport(width, "")
				require.GreaterOrEqual(t, column, 0)
				require.Less(t, column, width)
				require.NotContains(t, view, "▏")
				require.LessOrEqual(t, ansi.StringWidth(view), width, "view=%q", view)
			}
		}
		require.Equal(t, value, string(input.value), "display must not alter the submitted value")
	}
}

func TestWebhookCreateLongURLCanBeEditedAtVisibleTail(t *testing.T) {
	input := webhookCreateInput{}
	value := "https://example.invalid/" + strings.Repeat("long/", 30) + "tail"
	require.True(t, input.insert(value))
	check := func(suffix string, cursorBefore int) {
		view, column := input.viewport(32, "")
		require.True(t, strings.HasSuffix(view, suffix+" "), view)
		require.Equal(t, ansi.StringWidth(view)-1-cursorBefore, column)
	}
	check("tail", 0)
	require.True(t, input.edit(tea.KeyPressMsg{Code: tea.KeyLeft}))
	check("tail", 1)
	require.True(t, input.edit(tea.KeyPressMsg{Code: tea.KeyBackspace}))
	check("tal", 1)
	require.True(t, input.insert("NEW"))
	check("taNEWl", 1)
	require.Equal(t, strings.TrimSuffix(value, "tail")+"taNEWl", string(input.value))
	require.True(t, input.edit(tea.KeyPressMsg{Code: tea.KeyHome}))
	view, column := input.viewport(32, "")
	require.True(t, strings.HasPrefix(view, "https://"))
	require.Zero(t, column)
	require.True(t, input.edit(tea.KeyPressMsg{Code: tea.KeyEnd}))
	check("taNEWl", 0)
}

func TestWebhookCreateUppercaseSchemeDoesNotReachReview(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed"})
	webhookCreateKey(m, tea.KeyEnter)
	webhookCreatePaste(m, "HTTPS://example.invalid/webhook")
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 1, m.stage)
	require.Contains(t, m.note, "lowercase https://")
	require.Equal(t, "HTTPS://example.invalid/webhook", m.settings().url)
}

func TestWebhookCreatePickerCompactFrameAndCleanup(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {80, 60}, {40, 12}, {36, 10}} {
		m := newWebhookCreatePicker([]string{"agent.session.created", "batch.completed", "response.completed",
			"response.failed", "eval.run.succeeded", "video.completed", "safety.alert.created", "future.event"})
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		initial := ansi.Strip(m.View().Content)
		require.LessOrEqual(t, strings.Count(initial, "\n")+1, min(18, size[1]-1), "the combined form must stay compact")
		for _, field := range []string{"Name", "URL", "Events"} {
			require.Contains(t, initial, field, "every editing field must be visible from the start")
		}
		require.NotContains(t, initial, "1/4")
		previousHeight := 0
		for _, stage := range []int{0, 1, 2, 3, 2, 0} {
			m.stage = stage
			for _, query := range []string{"", "response", "response.completed", "no-matches"} {
				m.search = webhookCreateInput{}
				m.search.insert(query)
				for _, note := range []string{"", "Select at least one event with Space."} {
					m.note = note
					lines := strings.Split(ansi.Strip(m.View().Content), "\n")
					require.GreaterOrEqual(t, len(lines), previousHeight, "erase all previously owned rows")
					previousHeight = len(lines)
					require.LessOrEqual(t, len(lines), min(18, size[1]-1))
					for _, line := range lines {
						require.LessOrEqual(t, ansi.StringWidth(line), min(76, size[0]-2))
					}
				}
			}
		}
	}
}

func TestWebhookCreatePickerTallTerminalDoesNotExpandPanel(t *testing.T) {
	for stage := range 4 {
		m := newWebhookCreatePicker([]string{"response.completed", "response.failed", "batch.completed"})
		m.stage = stage
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		normal := m.View().Content
		m.Update(tea.WindowSizeMsg{Width: 160, Height: 60})
		require.Equal(t, normal, m.View().Content, "unused terminal space must not stretch the panel")
	}
}

func TestWebhookCreatePickerThemeAndPlainSelection(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed"})
	for stage := range 3 {
		m.stage, m.color = stage, false
		plain := m.View().Content
		require.Contains(t, plain, "› ")
		require.NotContains(t, plain, "\x1b")
		for _, dark := range []bool{true, false} {
			m.color, m.dark = true, dark
			colored := m.View().Content
			marker := "\x1b[1;38;2;49;89;188m›"
			if dark {
				marker = "\x1b[1;38;2;138;168;255m›"
			}
			require.Contains(t, colored, marker, "focus marker must use the theme's blue accent")
			color := "138;168;255"
			if !dark {
				color = "49;89;188"
			}
			for _, label := range []string{"Name", "URL", "Events"} {
				require.Regexp(t, "\\x1b\\[[^m]*38;2;"+color+"[^m]*m[› ]*"+label, colored, "field labels keep their accent when focus changes")
			}
			require.Equal(t, plain, ansi.Strip(colored), "theme must not change content or alignment")
		}
	}
}

func TestWebhookCreatePickerArrowNavigationIncludesReview(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed", "response.failed"})
	m.name.insert("My webhook")
	m.url.insert("https://example.invalid/webhook")
	webhookCreateKey(m, tea.KeyDown)
	require.Equal(t, 1, m.stage)
	webhookCreateKey(m, tea.KeyUp)
	require.Equal(t, 0, m.stage)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	require.Equal(t, 2, m.stage)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	require.True(t, m.reviewFocused)
	require.Contains(t, ansi.Strip(m.View().Content), "› [ Continue to confirmation ]")
	foot := ansi.Strip(m.View().Content)
	for _, inactive := range []string{"←→ category", "Space select", "Ctrl+A all"} {
		require.NotContains(t, foot, inactive, "the action must not advertise event controls")
	}
	require.Contains(t, foot, "Enter continue")
	require.Contains(t, foot, "↑ back")
	before := m.settings()
	webhookCreatePaste(m, "hidden text")
	webhookCreateKey(m, ' ')
	require.Equal(t, before, m.settings(), "review focus must not edit hidden fields or toggle an event")
	webhookCreateKey(m, tea.KeyUp)
	require.False(t, m.reviewFocused)
	webhookCreateKey(m, tea.KeyUp)
	webhookCreateKey(m, tea.KeyUp)
	webhookCreateKey(m, tea.KeyUp)
	webhookCreateKey(m, tea.KeyUp)
	require.Equal(t, 1, m.stage)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	require.True(t, m.reviewFocused)
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 3, m.stage)
	require.False(t, m.confirmYes)
	webhookCreateKey(m, tea.KeyEnter)
	require.True(t, m.canceled)
	require.False(t, m.confirmed)
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
	require.Contains(t, ansi.Strip(m.View().Content), "› Search")
	require.Contains(t, ansi.Strip(m.View().Content), "Type an event or group name")
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
	require.Contains(t, ansi.Strip(m.View().Content), "› Search")
	webhookCreateKey(m, tea.KeyUp)
	require.Contains(t, ansi.Strip(m.View().Content), "› Events")
	webhookCreateKey(m, tea.KeyUp)
	require.Equal(t, 1, m.stage)
	require.False(t, m.confirmed)
}

func TestWebhookCreatePickerTabStopsDoNotSubmitTheDraft(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed"})
	for _, marker := range []string{"› Name", "› URL", "› Events", "› Search", "› [ ] response.completed", "› [ Continue to confirmation ]"} {
		require.Contains(t, ansi.Strip(m.View().Content), marker)
		require.False(t, m.confirmed)
		webhookCreateKey(m, tea.KeyTab)
	}
	// The action validates the missing URL without consenting or hiding the error.
	require.Equal(t, 1, m.stage)
	require.Contains(t, m.note, "https://")
	require.False(t, m.confirmYes)
}

func TestWebhookCreatePickerKeepsOnlyContextualControls(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {36, 10}} {
		for stage := range 4 {
			m := newWebhookCreatePicker([]string{"response.completed"})
			m.width, m.height, m.stage = size[0], size[1], stage
			view := ansi.Strip(m.View().Content)
			for _, removed := range []string{" selected ·", "matching events", "Tab fields", "Enter next · Shift+Tab", "Shift+Tab"} {
				require.NotContains(t, view, removed)
			}
			if stage < 3 {
				require.Contains(t, view, "[ Continue to confirmation ]")
			}
		}
	}
}

func TestWebhookCreatePickerKeepsEditingFieldsTogether(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed", "response.failed"})
	for stage := range 3 {
		m.stage = stage
		view := ansi.Strip(m.View().Content)
		require.Contains(t, strings.Split(view, "\n")[1], "Create webhook endpoint", "keep the heading inside the form")
		for _, value := range []string{"Name", "Webhook", "URL", "https://example.com/webhook", "Events", "response.completed", "response.failed"} {
			require.Contains(t, view, value, "changing focus must not replace the other fields")
		}
		require.Equal(t, 1, strings.Count(view, "Create webhook endpoint"))
	}
}

func TestWebhookCreatePickerCanSelectEventsBeforeCompletingURL(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed"})
	webhookCreateKey(m, tea.KeyTab)
	webhookCreateKey(m, tea.KeyTab)
	require.Equal(t, 2, m.stage, "Tab must allow editing visible fields in any order")
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 1, m.stage, "review must return focus to the missing URL")
	require.Contains(t, m.note, "https://")
	require.False(t, m.confirmed)
	require.True(t, m.selected["response.completed"])
	webhookCreatePaste(m, "https://example.invalid/webhook")
	webhookCreateKey(m, tea.KeyEnter)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 3, m.stage)
	require.False(t, m.confirmYes, "choosing fields must never consent to creation")
	webhookCreateKey(m, tea.KeyEnter)
	require.True(t, m.canceled)
	require.False(t, m.confirmed)
}

func TestWebhookCreatePickerReviewValidatesSkippedName(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed"})
	webhookCreatePaste(m, strings.Repeat("n", 257))
	webhookCreateKey(m, tea.KeyTab)
	webhookCreatePaste(m, "https://example.invalid/webhook")
	webhookCreateKey(m, tea.KeyTab)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 0, m.stage)
	require.Contains(t, m.note, "256")
	require.False(t, m.confirmed)
	require.True(t, m.selected["response.completed"])
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

func TestWebhookCreatePickerCompactBudgetKeepsControls(t *testing.T) {
	for _, size := range [][2]int{{36, 10}, {40, 12}, {80, 24}, {80, 60}} {
		for stage := range 4 {
			m := newWebhookCreatePicker([]string{"response." + strings.Repeat("long_event", 30)})
			m.width, m.height, m.stage = size[0], size[1], stage
			m.url.insert("https://example.invalid/" + strings.Repeat("long/path/", 100))
			m.selected[m.events[0]] = true
			m.note = "Check this value before continuing."
			view := ansi.Strip(m.View().Content)
			require.NotContains(t, view, "Tab fields", "size=%v stage=%d", size, stage)
			if stage < 3 {
				require.Contains(t, view, "[ Continue to confirmation ]")
			}
			if stage == 3 {
				require.Contains(t, view, "Create endpoint now?")
				require.Contains(t, view, "[No]   Yes, create")
				require.Contains(t, view, "↑↓ settings")
			}
		}
	}
}

func TestWebhookCreateControlsRemainVisibleAtFrameThreshold(t *testing.T) {
	for width := 36; width <= 80; width++ {
		for height := 15; height <= 19; height++ {
			m := newWebhookCreatePicker([]string{"response.completed"})
			m.width, m.height, m.stage, m.eventFocus = width, height, 2, webhookCreateChoices
			m.browser = &webhookEndpointBrowser{}
			for _, note := range []string{"", "Choose at least one event."} {
				m.note = note
				view := ansi.Strip(m.View().Content)
				controls := []string{"Space select", "Esc cancel"}
				if note == "" {
					controls = append(controls, "Ctrl+A all", "Enter next")
				}
				for _, control := range controls {
					require.Contains(t, view, control, "size=%dx%d note=%q", width, height, note)
				}
			}
			m.browser = &webhookEndpointBrowser{
				active: true, items: []webhookEndpoint{{name: "Retained", id: "whe_partial"}},
				note: "Could not load all webhooks. Check your connection, project, and webhook read access, then Refresh.",
			}
			view := ansi.Strip(m.View().Content)
			for _, content := range []string{"Retained", "Could not load all webhooks.", "[ Refresh ]", "Tab actions", "Esc back"} {
				require.Contains(t, view, content, "size=%dx%d", width, height)
			}
		}
	}
}

func TestWebhookCreatePickerTinyFrameBlocksHiddenEditingAndConfirmation(t *testing.T) {
	for stage := range 4 {
		m := newWebhookCreatePicker([]string{"response.completed"})
		m.stage, m.confirmYes = stage, true
		m.name.insert("Original name")
		m.url.insert("https://example.invalid/original")
		m.search.insert("response")
		m.selected["response.completed"] = true
		before := m.settings()
		m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
		require.Contains(t, m.View().Content, "Resize")
		require.NotContains(t, m.View().Content, "Create endpoint now")
		webhookCreatePaste(m, "hidden edit")
		for _, key := range []tea.KeyPressMsg{
			{Code: 'x', Text: "x"}, {Code: tea.KeyBackspace}, {Code: tea.KeyTab},
			{Code: tea.KeyTab, Mod: tea.ModShift}, {Code: tea.KeyLeft}, {Code: tea.KeyDown},
			{Code: ' ', Text: " "}, {Code: tea.KeyEnter},
		} {
			m.Update(key)
		}
		require.Equal(t, before, m.settings())
		require.Equal(t, "response", string(m.search.value))
		require.Equal(t, stage, m.stage)
		require.True(t, m.confirmYes, "hidden keys must not change the selected confirmation")
		require.False(t, m.confirmed, "Enter must not submit while review is hidden")
		require.False(t, m.canceled)
		webhookCreateKey(m, tea.KeyEscape)
		require.True(t, m.canceled, "cancellation remains available")
	}
}

func TestWebhookCreatePickerExitFrameReplacesCompleteReview(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}, {36, 10}} {
		for _, confirm := range []bool{false, true} {
			m := newWebhookCreatePicker([]string{"response.completed"})
			m.name.insert("Synthetic review name")
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			webhookCreateReview(t, m)
			before := strings.Split(m.View().Content, "\n")
			require.Contains(t, strings.Join(before, "\n"), "Create endpoint now?")
			if confirm {
				webhookCreateKey(m, tea.KeyRight)
				webhookCreateKey(m, tea.KeyEnter)
			} else {
				webhookCreateKey(m, tea.KeyEscape)
			}
			after := m.View().Content
			require.Len(t, strings.Split(after, "\n"), len(before), "the final frame must replace all prior owned rows")
			for _, previous := range []string{"Create webhook endpoint ·", "Create endpoint now?", "Synthetic review name", "https://example.invalid", "response.completed", "[No]", "[Yes, create]"} {
				require.NotContains(t, after, previous)
			}
			if confirm {
				require.Equal(t, "Creating webhook endpoint...", strings.TrimSpace(after))
			} else {
				require.Equal(t, "Webhook creation canceled.", strings.TrimSpace(after))
			}
		}
	}
}

func TestWebhookCreatePickerParentCancelMessageFinishesFrame(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {20, 5}} {
		m := newWebhookCreatePicker([]string{"response.completed"})
		m.stage, m.confirmed, m.confirmYes = 3, true, true
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		// A submitted model ignores window messages; set the current window to
		// model a signal racing with submission in an already-small terminal.
		m.width, m.height = size[0], size[1]
		_, quit := m.Update(webhookCreateCancelMsg{})
		require.NotNil(t, quit)
		require.True(t, m.canceled)
		require.False(t, m.confirmed)
		require.NotContains(t, m.View().Content, "Create endpoint now?")
		require.LessOrEqual(t, len(strings.Split(m.View().Content, "\n")), min(18, size[1]-1))
	}
}

type webhookCreateLifecycleModel struct {
	*webhookCreatePicker
	initialize tea.Cmd
}

func (m *webhookCreateLifecycleModel) Init() tea.Cmd { return m.initialize }

func TestWebhookCreateCancellationWatcherLifecycle(t *testing.T) {
	for _, mode := range []string{"parent cancellation", "normal quit", "writer failure", "initialization failure"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancelParent := context.WithCancel(t.Context())
			defer cancelParent()
			private, stop := context.WithCancel(context.WithoutCancel(parent))
			defer stop()
			model := &webhookCreateLifecycleModel{webhookCreatePicker: newWebhookCreatePicker([]string{"response.completed"})}
			model.initialize = func() tea.Msg {
				switch mode {
				case "parent cancellation":
					cancelParent()
					return nil
				case "writer failure":
					stop()
					return nil
				default:
					return tea.KeyPressMsg{Code: tea.KeyEscape}
				}
			}
			program := tea.NewProgram(model, tea.WithContext(private), tea.WithInput(strings.NewReader("")),
				tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler(), tea.WithWindowSize(80, 24))
			finish := watchWebhookCreateCancellation(parent, program, stop)
			finished := false
			defer func() {
				if !finished {
					finish()
				}
			}()
			if mode == "initialization failure" {
				// Exercise a canceled Send before the event loop ever starts.
				cancelParent()
				finish()
				finished = true
				require.ErrorIs(t, private.Err(), context.Canceled)
				return
			}
			_, err := program.Run()
			if mode == "writer failure" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.True(t, model.canceled)
				require.False(t, model.confirmed)
			}
			if mode == "parent cancellation" {
				require.NoError(t, private.Err(), "parent cancellation must render through the model before stopping Tea")
				require.ErrorIs(t, parent.Err(), context.Canceled)
			}
			finish()
			finished = true
			require.ErrorIs(t, private.Err(), context.Canceled)
		})
	}
}

func TestWebhookPickerOutputPreservesFirstFailure(t *testing.T) {
	first := errors.New("synthetic first output failure")
	second := errors.New("synthetic cleanup failure")
	stops := 0
	out := &webhookPickerOutput{TerminalWriter: clioutput.TerminalWriter{
		Writer: clioutput.Writer{Context: t.Context(), Sink: webhookFailedWriter{first}},
	}, stop: func() { stops++ }}
	_, err := out.Write([]byte("frame"))
	require.ErrorIs(t, err, first)
	out.Writer.Sink = webhookFailedWriter{second}
	_, err = out.WriteString("cleanup")
	require.ErrorIs(t, err, second)
	require.ErrorIs(t, out.Err(), first)
	require.NotErrorIs(t, out.Err(), second)
	require.Equal(t, 2, stops)
}

func TestWebhookPickerCleanupCancellationKeepsRealErrors(t *testing.T) {
	failure := errors.New("synthetic terminal output failure")
	for _, err := range []error{context.Canceled, tea.ErrProgramKilled, errors.Join(context.Canceled, tea.ErrProgramKilled)} {
		require.NoError(t, webhookWithoutCleanupCancellation(err))
	}
	for _, err := range []error{failure, errors.Join(failure, context.Canceled),
		fmt.Errorf("cleanup: %w", errors.Join(tea.ErrProgramKilled, failure, context.Canceled))} {
		got := webhookWithoutCleanupCancellation(err)
		require.ErrorIs(t, got, failure)
		require.NotErrorIs(t, got, context.Canceled)
		require.NotErrorIs(t, got, tea.ErrProgramKilled)
	}
}

func TestWebhookCreatePickerBlinkingTextCursorAndCompactSpacing(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 24}, {36, 10}, {40, 12}} {
		m := newWebhookCreatePicker([]string{"response.completed", "response.failed"})
		m.width, m.height, m.color = size[0], size[1], true
		for stage := range 3 {
			m.stage = stage
			view := m.View()
			require.NotContains(t, view.Content, "▏")
			require.NotRegexp(t, `\x1b\[[^m]*48;`, view.Content, "focus must not paint a highlight bar")
			if stage == 2 {
				require.Nil(t, view.Cursor, "category browsing does not show a text cursor")
				require.Contains(t, ansi.Strip(view.Content), "› Events")
				m.eventFocus = webhookCreateSearch
			}
			input := m.input()
			input.insert(strings.Repeat("猫🙂", 30))
			for _, position := range []int{0, len(input.value) / 2, len(input.value)} {
				input.cursor = position
				inputWidth, column := min(76, size[0]-2)-9, 9
				if size[1] >= 15 {
					inputWidth -= 6 // Border padding and the fixed column divider.
					column += 4
				}
				_, offset := input.viewport(inputWidth, "")
				painted := m.View()
				require.NotNil(t, painted.Cursor)
				require.Equal(t, tea.CursorBar, painted.Cursor.Shape, "text editing uses a thin cursor")
				require.True(t, painted.Cursor.Blink)
				require.Equal(t, column+offset, painted.Cursor.X)
				require.Less(t, painted.Cursor.X, size[0]-2)
				lines := strings.Split(ansi.Strip(painted.Content), "\n")
				require.Less(t, painted.Cursor.Y, len(lines))
				require.Contains(t, lines[painted.Cursor.Y], []string{"Name", "URL", "Search"}[stage])
			}
		}
		m.reviewFocused = true
		require.Nil(t, m.View().Cursor)
		m.canceled = true
		require.Nil(t, m.View().Cursor)
	}
}

func TestWebhookCreatePickerCategorySelectionPreservesSubscriptions(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed", "batch.completed", "response.failed", "future.event"})
	m.stage = 2
	require.Equal(t, []string{"All event types", "Batches", "Background responses", "Other"}, m.categories())
	webhookCreateKey(m, tea.KeyRight)
	require.Equal(t, []string{"batch.completed"}, m.filteredEvents())
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	webhookCreatePaste(m, "no matches")
	require.Empty(t, m.filteredEvents())
	webhookCreateKey(m, tea.KeyUp)
	webhookCreateKey(m, tea.KeyRight)
	require.Empty(t, m.search.value, "switching categories clears the local search")
	require.Zero(t, m.current)
	require.Zero(t, m.offset)
	require.Equal(t, []string{"response.completed", "response.failed"}, m.filteredEvents())
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, tea.KeyDown)
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	require.ElementsMatch(t, []string{"batch.completed", "response.completed", "response.failed"}, m.settings().events)
	webhookCreateKey(m, tea.KeyRight)
	require.Equal(t, []string{"future.event"}, m.filteredEvents())
	webhookCreateKey(m, ' ')
	webhookCreateKey(m, tea.KeyRight)
	require.Zero(t, m.category, "category browsing wraps to all events")
	require.Len(t, m.filteredEvents(), 4)
	webhookCreateKey(m, tea.KeyLeft)
	require.Equal(t, 3, m.category)
	require.Len(t, m.settings().events, 4, "category changes must preserve hidden selections")
	m.stage = 0
	m.name.insert("Name")
	webhookCreateKey(m, tea.KeyLeft)
	require.Equal(t, 3, m.name.cursor, "text fields retain ordinary cursor keys")
	require.Equal(t, 3, m.category)
}

func TestWebhookCreatePickerDoesNotEndWithEmptyGroupHeading(t *testing.T) {
	m := newWebhookCreatePicker([]string{"agent.session.action_required", "agent.session.created", "agent.session.failed", "agent.session.idle", "agent.session.in_progress", "batch.completed"})
	m.stage = 2
	m.eventFocus = webhookCreateChoices
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Batches") {
		require.Contains(t, view, "batch.completed", "a group heading needs space for its first choice")
	}
	for range 5 {
		webhookCreateKey(m, tea.KeyDown)
	}
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "Batches")
	require.Contains(t, view, "› [ ] batch.completed")
}
