package custom

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func webhookCreateKey(m *webhookCreatePicker, code rune) {
	m.Update(tea.KeyPressMsg{Code: code})
}

func webhookCreatePaste(m *webhookCreatePicker, text string) {
	m.Update(tea.PasteMsg{Content: text})
}

func webhookCreateReview(t *testing.T, m *webhookCreatePicker) {
	t.Helper()
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 1, m.stage)
	webhookCreatePaste(m, "https://example.invalid/webhook")
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 2, m.stage)
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
	require.Contains(t, m.View().Content, "default: Webhook")
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
	m.stage = 2
	require.Len(t, m.events, 4)
	webhookCreatePaste(m, "responses")
	require.Equal(t, []string{"response.completed", "response.failed"}, m.filteredEvents())
	webhookCreateKey(m, ' ')
	require.True(t, m.selected["response.completed"])
	webhookCreateKey(m, tea.KeyDown)
	webhookCreateKey(m, ' ')
	require.True(t, m.selected["response.failed"])
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	webhookCreatePaste(m, "OTHER")
	require.Equal(t, []string{"new.family.event"}, m.filteredEvents())
	webhookCreateKey(m, ' ')
	require.Equal(t, []string{"response.completed", "response.failed", "new.family.event"}, m.settings().events)
	webhookCreatePaste(m, "missing")
	require.Empty(t, m.filteredEvents())
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
	for _, size := range [][2]int{{80, 24}, {40, 12}, {36, 10}, {100, 30}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for range len(events) {
			content := ansi.Strip(m.View().Content)
			lines := strings.Split(content, "\n")
			require.LessOrEqual(t, len(lines), size[1]-1)
			for _, line := range lines {
				require.LessOrEqual(t, ansi.StringWidth(line), size[0]-2)
			}
			webhookCreateKey(m, ' ')
			webhookCreateKey(m, tea.KeyDown)
		}
		m.current = 0
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
		lines := strings.Split(m.View().Content, "\n")
		scrollHint := -1
		for i := 2; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], "↑↓ review ") {
				scrollHint = i
				break
			}
			index := m.reviewOffset + i - 2
			if previous, exists := rows[index]; exists {
				require.Equal(t, previous, lines[i], "review rows must remain stable while scrolling")
			}
			rows[index] = lines[i]
		}
		require.Greater(t, scrollHint, 2, "the review must show content and its scroll range")
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
				view := input.display(width, "")
				require.Contains(t, view, "▏", "width=%d cursor=%d", width, cursor)
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
	require.Contains(t, input.display(32, ""), "tail▏")
	require.True(t, input.edit(tea.KeyPressMsg{Code: tea.KeyLeft}))
	require.Contains(t, input.display(32, ""), "tai▏l")
	require.True(t, input.edit(tea.KeyPressMsg{Code: tea.KeyBackspace}))
	require.Contains(t, input.display(32, ""), "ta▏l")
	require.True(t, input.insert("NEW"))
	require.Contains(t, input.display(32, ""), "taNEW▏l")
	require.Equal(t, strings.TrimSuffix(value, "tail")+"taNEWl", string(input.value))
	require.True(t, input.edit(tea.KeyPressMsg{Code: tea.KeyHome}))
	require.True(t, strings.HasPrefix(input.display(32, ""), "▏https://"))
	require.True(t, input.edit(tea.KeyPressMsg{Code: tea.KeyEnd}))
	require.Contains(t, input.display(32, ""), "taNEWl▏")
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

func TestWebhookCreatePickerFrameHeightStaysStable(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}, {36, 10}} {
		m := newWebhookCreatePicker([]string{"agent.session.created", "batch.completed", "response.completed",
			"response.failed", "eval.run.succeeded", "video.completed", "safety.alert.created", "future.event"})
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for stage := range 4 {
			m.stage = stage
			for _, query := range []string{"", "response", "response.completed", "no-matches"} {
				m.search = webhookCreateInput{}
				m.search.insert(query)
				for _, note := range []string{"", "Select at least one event with Space."} {
					m.note = note
					lines := strings.Split(m.View().Content, "\n")
					require.Len(t, lines, size[1]-1, "stage=%d query=%q note=%q", stage, query, note)
					require.NotEmpty(t, lines[len(lines)-1], "keep keyboard guidance on the final row")
					for _, line := range lines {
						require.LessOrEqual(t, ansi.StringWidth(line), size[0]-2)
					}
				}
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
		require.Len(t, strings.Split(m.View().Content, "\n"), size[1]-1)
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
