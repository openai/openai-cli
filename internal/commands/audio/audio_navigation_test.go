package audio

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func audioNavigationKey(t *testing.T, m *audioWorkbench, key tea.KeyPressMsg) {
	t.Helper()
	input, instructions, settings := m.input.Value(), m.instructions.Value(), m.settings
	started, note := m.started, m.note
	_, command := m.Update(key)
	require.False(t, m.busy, "navigation scheduled audio work: %s", key.String())
	require.Equal(t, started, m.started, "navigation changed the work start time")
	require.Equal(t, note, m.note, "navigation attempted an action or changed feedback")
	require.False(t, m.quit)
	require.Equal(t, input, m.input.Value())
	require.Equal(t, instructions, m.instructions.Value())
	require.Equal(t, settings, m.settings)
	// Textarea updates and focus can return caret commands. Other navigation
	// must return no command; all work paths also set busy and started.
	if m.focus >= 2 && m.modal != "save" && m.modal != "speed" {
		require.Nil(t, command, "non-editor navigation returned work: %s", key.String())
	}
}

func audioNavigationEditor(t *testing.T, focus int, text string) (*audioWorkbench, *textarea.Model) {
	t.Helper()
	m := audioReviewModel(t)
	m.input.SetValue("Synthetic text remains available for explicit generation")
	m.instructions.SetValue("Keep these instructions")
	m.input.Blur()
	m.instructions.Blur()
	m.focus = focus
	editor := &m.input
	if focus == 1 {
		editor = &m.instructions
	}
	editor.SetValue(text)
	editor.Focus()
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	return m, editor
}

func TestAudioNavigationEmptyEditors(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("")
	m.instructions.SetValue("")
	for _, step := range []struct {
		key   rune
		focus int
	}{
		{tea.KeyUp, 0},
		{tea.KeyDown, 1},
		{tea.KeyDown, 2},
		{tea.KeyUp, 1},
		{tea.KeyUp, 0},
		{tea.KeyUp, 0},
	} {
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: step.key})
		require.Equal(t, step.focus, m.focus, "empty editor focus after %s", tea.KeyPressMsg{Code: step.key}.String())
		require.Equal(t, step.focus == 0, m.input.Focused())
		require.Equal(t, step.focus == 1, m.instructions.Focused())
	}
	require.Equal(t, 1, m.setting)
	require.Nil(t, m.result)
}

func TestAudioNavigationSingleLineBoundaries(t *testing.T) {
	for _, focus := range []int{0, 1} {
		for _, direction := range []rune{tea.KeyUp, tea.KeyDown} {
			t.Run(fmt.Sprintf("focus_%d/key_%d", focus, direction), func(t *testing.T) {
				m, editor := audioNavigationEditor(t, focus, "café 世界")
				editor.SetCursorColumn(2)
				require.Equal(t, 1, editor.LineInfo().Height)
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
				expected := max(0, focus-1)
				if direction == tea.KeyDown {
					expected = focus + 1
				}
				require.Equal(t, expected, m.focus)
			})
		}
	}
}

func TestAudioNavigationMultilineUnicodeMovesCaretBeforeLeaving(t *testing.T) {
	for _, focus := range []int{0, 1} {
		for _, direction := range []rune{tea.KeyUp, tea.KeyDown} {
			t.Run(fmt.Sprintf("focus_%d/key_%d", focus, direction), func(t *testing.T) {
				m, editor := audioNavigationEditor(t, focus, "First 世界\nSecond café\nThird 🙂")
				require.Equal(t, 3, editor.LineCount())
				if direction == tea.KeyDown {
					for range 3 {
						editor.CursorUp()
					}
					editor.CursorStart()
					require.Zero(t, editor.Line())
					for _, line := range []int{1, 2} {
						audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
						require.Equal(t, focus, m.focus)
						require.Equal(t, line, editor.Line())
					}
					audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
					require.Equal(t, focus+1, m.focus)
				} else {
					require.Equal(t, 2, editor.Line())
					for _, line := range []int{1, 0} {
						audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
						require.Equal(t, focus, m.focus)
						require.Equal(t, line, editor.Line())
					}
					audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
					require.Equal(t, max(0, focus-1), m.focus)
				}
			})
		}
	}
}

func TestAudioNavigationWrappedUnicodeUsesVisualRows(t *testing.T) {
	for _, focus := range []int{0, 1} {
		for _, direction := range []rune{tea.KeyUp, tea.KeyDown} {
			t.Run(fmt.Sprintf("focus_%d/key_%d", focus, direction), func(t *testing.T) {
				m, editor := audioNavigationEditor(t, focus, strings.Repeat("界🙂", 24))
				require.Equal(t, 1, editor.LineCount(), "fixture must wrap without logical newlines")
				rows := editor.LineInfo().Height
				require.Greater(t, rows, 2)
				if direction == tea.KeyDown {
					editor.CursorStart()
					require.Zero(t, editor.LineInfo().RowOffset)
					for row := 1; row < rows; row++ {
						audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
						require.Equal(t, focus, m.focus, "left before the last visual row")
						require.Equal(t, row, editor.LineInfo().RowOffset)
					}
					audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
					require.Equal(t, focus+1, m.focus)
				} else {
					require.Equal(t, rows-1, editor.LineInfo().RowOffset)
					for row := rows - 2; row >= 0; row-- {
						audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
						require.Equal(t, focus, m.focus, "left before the first visual row")
						require.Equal(t, row, editor.LineInfo().RowOffset)
					}
					audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
					require.Equal(t, max(0, focus-1), m.focus)
				}
			})
		}
	}
}

func TestAudioNavigationWrappedPaddingBeforeNextLogicalLine(t *testing.T) {
	for _, focus := range []int{0, 1} {
		t.Run(fmt.Sprintf("focus_%d", focus), func(t *testing.T) {
			m, editor := audioNavigationEditor(t, focus, "Initial text")
			require.Zero(t, editor.Width()%4, "fixture needs complete wide-character pairs")
			firstLine := strings.Repeat("界🙂", 3*editor.Width()/4)
			const nextLine = "Next logical row"
			editor.SetValue(firstLine + "\n" + nextLine)
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
			editor.MoveToBegin()
			require.Equal(t, 2, editor.LineCount())
			require.Equal(t, 4, editor.LineInfo().Height, "exact-width text must retain its cursor-only padding row")
			for _, row := range []int{1, 2, 3} {
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
				require.Equal(t, focus, m.focus, "padding navigation left the editor")
				require.Zero(t, editor.Line(), "padding row was skipped for the next logical line")
				require.Equal(t, row, editor.LineInfo().RowOffset)
			}
			require.Equal(t, len([]rune(firstLine)), editor.Column())
			require.Equal(t, editor.Column(), editor.LineInfo().StartColumn)
			require.Positive(t, editor.ScrollYOffset(), "padding caret did not scroll into the viewport")
			visibleCaretRow := editor.LineInfo().RowOffset - editor.ScrollYOffset()
			require.GreaterOrEqual(t, visibleCaretRow, 0)
			require.Less(t, visibleCaretRow, editor.Height())

			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			require.Equal(t, focus, m.focus)
			require.Equal(t, 1, editor.Line(), "Down did not reach the next logical line")
			require.Zero(t, editor.LineInfo().RowOffset)
			require.Contains(t, ansi.Strip(editor.View()), nextLine, "next logical line is outside the visible viewport")
			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			require.Equal(t, focus+1, m.focus, "Down did not leave after the final logical line")
		})
	}
}

func TestAudioNavigationAdjacentWrapWidths(t *testing.T) {
	for _, delta := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("width_delta_%d", delta), func(t *testing.T) {
			m, editor := audioNavigationEditor(t, 0, "Initial text")
			text := strings.Repeat("x", editor.Width()+delta)
			editor.SetValue(text)
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
			editor.MoveToBegin()
			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			if delta < 0 {
				require.Equal(t, 1, m.focus, "unwrapped text did not reach Instructions")
				return
			}
			require.Zero(t, m.focus, "wrapped text left before its final visual row")
			require.Equal(t, 1, editor.LineInfo().RowOffset)
			require.Equal(t, editor.Width(), editor.Column(), "Down skipped the last visual row's first cell")
			if delta == 0 {
				require.Equal(t, len(text), editor.Column(), "exact-width padding caret is missing")
			} else {
				require.Less(t, editor.Column(), len(text), "ordinary wrapped text incorrectly used the padding fallback")
			}
			require.Equal(t, 0, editor.LineInfo().RowOffset-editor.ScrollYOffset(), "final visual row is outside the one-row editor")
			require.Equal(t, 1, editor.Height())
			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			require.Equal(t, 1, m.focus)
		})
	}
}

func TestAudioNavigationPreservesSelectionAndModifiedArrows(t *testing.T) {
	for _, focus := range []int{0, 1} {
		for _, direction := range []rune{tea.KeyUp, tea.KeyDown} {
			t.Run(fmt.Sprintf("selection/focus_%d/key_%d", focus, direction), func(t *testing.T) {
				m, editor := audioNavigationEditor(t, focus, "Selected 世界")
				editor.SelectAll()
				require.True(t, editor.HasSelection())
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction})
				require.Equal(t, focus, m.focus, "selection navigation escaped the editor")
			})
			for _, modifier := range []tea.KeyMod{tea.ModShift, tea.ModAlt, tea.ModCtrl, tea.ModCtrl | tea.ModShift} {
				t.Run(fmt.Sprintf("modifier_%d/focus_%d/key_%d", modifier, focus, direction), func(t *testing.T) {
					m, _ := audioNavigationEditor(t, focus, "Keep modified arrows in this editor")
					audioNavigationKey(t, m, tea.KeyPressMsg{Code: direction, Mod: modifier})
					require.Equal(t, focus, m.focus)
				})
			}
		}
	}
}

func TestAudioNavigationTabUsesVisibleSectionOrder(t *testing.T) {
	for _, withResult := range []bool{false, true} {
		t.Run(fmt.Sprintf("result_%t", withResult), func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Do not generate while traversing")
			order := []int{0, 1, 2, 4}
			if withResult {
				result := audioReviewResult("retained", false)
				m.result = &result
			}
			for range 2 {
				for step := 1; step <= len(order); step++ {
					audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
					require.Equal(t, order[step%len(order)], m.focus)
				}
				for step := len(order) - 1; step >= 0; step-- {
					audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
					require.Equal(t, order[step], m.focus)
				}
			}
		})
	}
}

func TestAudioActionsKeyboardRouteBeforeGeneration(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 137, Height: 41}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			m := audioReviewModel(t)
			m.Update(size)
			m.input.SetValue("Keep this draft.")
			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
			require.Zero(t, m.focus)
			require.Empty(t, m.modal)

			tab := tea.KeyPressMsg{Code: tea.KeyTab}
			audioNavigationKey(t, m, tab) // Instructions.
			audioNavigationKey(t, m, tab) // Settings.
			if size.Width >= 110 {
				require.Contains(t, ansi.Strip(m.View().Content), "Tab Actions")
				audioNavigationKey(t, m, tab)
			} else {
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
				view := ansi.Strip(m.View().Content)
				require.Contains(t, view, "› More actions…")
				require.Contains(t, view, "Enter open")
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			require.Equal(t, "more", m.modal)
			require.Contains(t, ansi.Strip(m.View().Content), "› History")
			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Equal(t, "info", m.modal)
			require.Contains(t, ansi.Strip(m.View().Content), "Help")
			require.Nil(t, m.result)
		})
	}
}

func TestAudioNavigationSettingsArrowsDoNotWrapOrActivate(t *testing.T) {
	for _, withResult := range []bool{false, true} {
		t.Run(fmt.Sprintf("result_%t", withResult), func(t *testing.T) {
			m := audioReviewModel(t)
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 12}) // Actions occupy separate rows.
			m.input.SetValue("Clear must remain an explicit action")
			m.focus, m.setting = 2, 1
			if withResult {
				result := audioReviewResult("retained", false)
				m.result = &result
			}
			for _, setting := range []int{audioSettingDisclosure, 4, audioSettingHistory, audioSettingMore} {
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
				require.Equal(t, 2, m.focus)
				require.Equal(t, setting, m.setting)
			}
			for _, key := range []rune{tea.KeyEnd, tea.KeyHome, tea.KeyHome, tea.KeyEnd} {
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: key})
				expected := 1
				if key == tea.KeyEnd {
					expected = audioSettingMore
				}
				require.Equal(t, expected, m.setting)
				require.Equal(t, 2, m.focus)
			}
			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			require.Equal(t, 4, m.focus, "visible player controls must remain reachable before generation")
			require.Empty(t, m.modal)
			m.focus, m.setting = 2, audioSettingMore
			for _, setting := range []int{audioSettingHistory, 4, audioSettingDisclosure, 1} {
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
				require.Equal(t, 2, m.focus)
				require.Equal(t, setting, m.setting)
			}
			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
			require.Equal(t, 1, m.focus, "top setting wrapped to the last setting")
			require.True(t, m.instructions.Focused())
		})
	}
}

func TestAudioNavigationWideActionsToWaveform(t *testing.T) {
	for _, lastError := range []bool{false, true} {
		t.Run(fmt.Sprintf("last_error_%t", lastError), func(t *testing.T) {
			m := audioResultNavigationModel(t, 120, 32)
			m.input.SetValue("Keep this draft.")
			if lastError {
				m.failure.Summary = "A previous action failed."
				m.failureNote = m.note
			}
			key := func(code rune) { audioNavigationKey(t, m, tea.KeyPressMsg{Code: code}) }
			m.setting = audioSettingDisclosure
			m.focusSection(2)
			key(tea.KeyRight)
			require.Equal(t, "more", m.modal)
			key(tea.KeyLeft)
			require.Empty(t, m.modal)
			require.Equal(t, 4, m.setting)
			key(tea.KeyRight)
			for i := range m.moreItems() {
				if i > 0 {
					key(tea.KeyRight)
				}
				require.Equal(t, i, m.moreChoice)
				key(tea.KeyDown)
				require.Empty(t, m.modal)
				require.Equal(t, 4, m.focus)
				require.True(t, m.waveformFocused)
				require.Contains(t, m.View().Content, "↑ Actions")
				key(tea.KeyUp)
				require.Equal(t, "more", m.modal)
				require.Equal(t, i, m.moreChoice)
			}
			key(tea.KeyRight)
			require.Equal(t, len(m.moreItems())-1, m.moreChoice)
			key(tea.KeyLeft)
			require.Equal(t, len(m.moreItems())-2, m.moreChoice)
			key(tea.KeyHome)
			require.Equal(t, 4, m.setting)
			key(tea.KeyEnd)
			require.Equal(t, len(m.moreItems())-1, m.moreChoice)
			key(tea.KeyUp)
			require.Empty(t, m.modal)
			require.Equal(t, audioSettingDisclosure, m.setting)
			key(tea.KeyDown)
			require.Equal(t, 4, m.setting)
			key(tea.KeyDown)
			require.Equal(t, 4, m.focus)
			require.True(t, m.waveformFocused)
			require.Contains(t, m.View().Content, "↑ Generate")
			key(tea.KeyUp)
			require.Empty(t, m.modal)
			require.Equal(t, 2, m.focus)
			require.Equal(t, 4, m.setting)
			require.NotNil(t, m.result)
			require.Equal(t, "Keep this draft.", m.input.Value())
		})
	}
}

func TestAudioWideActionsRetainSelectionAcrossAudioAndRecovery(t *testing.T) {
	m := audioResultNavigationModel(t, 110, 24)
	m.settingsExpanded = true
	m.failure.Summary = "Synthetic failure."
	m.failureNote = m.note
	m.openActions()
	audioReviewKey(m, tea.KeyEnd)
	require.Equal(t, "Last error", m.moreItems()[m.moreChoice])
	audioReviewKey(m, tea.KeyUp)
	require.Equal(t, 3, m.setting, "Up returns to Speed when Settings is expanded")
	m.openActions()
	audioReviewKey(m, tea.KeyEnd)
	m.note = "Playback completed."
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 24})
	require.Equal(t, "Help", m.moreItems()[m.moreChoice])
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	require.Equal(t, 4, m.focus)
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, "more", m.modal)
	require.Equal(t, "Help", m.moreItems()[m.moreChoice])
	m.Update(tea.WindowSizeMsg{Width: 109, Height: 24})
	require.Contains(t, ansi.Strip(m.View().Content), "More actions")
	audioReviewKey(m, tea.KeyUp)
	require.Equal(t, "View command", m.moreItems()[m.moreChoice])
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 24})
	require.Contains(t, ansi.Strip(m.View().Content), "› View command")
}

func TestAudioNavigationHorizontalFormActions(t *testing.T) {
	m := audioResultNavigationModel(t, 101, 29)
	key := func(code rune) { audioNavigationKey(t, m, tea.KeyPressMsg{Code: code}) }
	key(tea.KeyRight) // Select Save before returning to the form.
	m.focusSection(2)
	m.setting = 4
	for _, action := range []int{4, audioSettingHistory, audioSettingMore} {
		require.Equal(t, action, m.setting)
		key(tea.KeyDown)
		require.Equal(t, 4, m.focus)
		require.True(t, m.waveformFocused)
		key(tea.KeyDown)
		require.False(t, m.waveformFocused)
		require.Equal(t, 1, m.resultAction)
		key(tea.KeyUp)
		key(tea.KeyUp)
		require.Equal(t, 2, m.focus)
		require.Equal(t, action, m.setting)
		key(tea.KeyRight)
	}
	key(tea.KeyUp)
	require.Equal(t, audioSettingDisclosure, m.setting)
}

func TestAudioNavigationCommandScrollingStaysBounded(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue(strings.Repeat("Preview 世界 ", 200))
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	require.Nil(t, audioReviewMore(t, m, "View command"))
	require.Empty(t, m.modal)
	limit := func() int {
		return max(0, len(m.commandDisplayRows())-m.commandViewportHeight())
	}
	require.Positive(t, limit())
	for range 100 {
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
		require.LessOrEqual(t, m.commandOffset, limit())
	}
	require.Equal(t, limit(), m.commandOffset)
	for _, key := range []rune{tea.KeyDown, tea.KeyPgDown, tea.KeyRight, tea.KeyEnd} {
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: key})
		require.Equal(t, limit(), m.commandOffset)
	}
	for _, width := range []int{160, 40, 80} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
		require.GreaterOrEqual(t, m.commandOffset, 0)
		require.LessOrEqual(t, m.commandOffset, limit())
		for _, line := range strings.Split(m.View().Content, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), width)
		}
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
		require.Equal(t, limit(), m.commandOffset)
	}
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	for _, key := range []rune{tea.KeyHome, tea.KeyPgUp, tea.KeyLeft} {
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: key})
		require.Zero(t, m.commandOffset)
		require.Empty(t, m.modal)
	}
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	require.Positive(t, m.commandOffset)
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	require.Zero(t, m.commandOffset)
	require.Empty(t, m.modal)
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	require.Empty(t, m.modal)
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, "more", m.modal)
	require.Equal(t, "View command", m.moreItems()[m.moreChoice])
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, 2, m.focus)
	require.Equal(t, audioSettingMore, m.setting)
}

func TestAudioNavigationRespectsBusyTinyAndModalGuards(t *testing.T) {
	keys := []tea.KeyPressMsg{{Code: tea.KeyUp}, {Code: tea.KeyDown}, {Code: tea.KeyHome}, {Code: tea.KeyEnd},
		{Code: tea.KeyTab}, {Code: tea.KeyTab, Mod: tea.ModShift}}
	for _, guard := range []string{"busy", "tiny"} {
		t.Run(guard, func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Keep guarded draft")
			m.focus, m.setting, m.infoOffset = 2, 3, 20
			if guard == "busy" {
				m.busy = true
			} else {
				m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
			}
			for _, key := range keys {
				_, command := m.Update(key)
				require.Nil(t, command)
				require.Equal(t, 2, m.focus)
				require.Equal(t, 3, m.setting)
				require.Equal(t, 20, m.infoOffset)
				require.Equal(t, guard == "busy", m.busy)
				require.Equal(t, "Keep guarded draft", m.input.Value())
			}
		})
	}
	for _, modal := range []string{"choice", "history", "save", "speed", "more", "info", "recovery"} {
		t.Run(modal, func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Keep modal draft")
			m.focus, m.setting, m.modal = 2, 1, modal
			if modal == "recovery" {
				m.modal = ""
				_, command := m.Update(audioWorkMessage{kind: "generate", err: audioUnauthorizedFailure()})
				require.Nil(t, command)
				require.True(t, m.activeFailure())
			}
			m.choices = []string{"coral", "ash"}
			m.entries = []audiohistory.Entry{audioReviewResult("first", false).entry, audioReviewResult("second", false).entry}
			m.path.Focus()
			m.speedInput.Focus()
			for index, key := range keys {
				audioNavigationKey(t, m, key)
				require.Equal(t, 2, m.focus)
				require.Equal(t, 1, m.setting)
				require.Equal(t, modal, m.modal)
				if modal == "recovery" {
					require.True(t, m.activeFailure())
					require.Zero(t, m.failureOffset, "short error details need no scrolling")
				}
				if modal == "choice" && index < 2 {
					require.Equal(t, 1-index, m.choice, "modal must retain arrow navigation")
				}
			}
		})
	}
}

func TestAudioNavigationResultArrowsPreservePlayback(t *testing.T) {
	m := audioReviewModel(t)
	result := audioReviewResult("playing-result", false)
	m.result, m.focus, m.playing, m.playID = &result, 4, true, 42
	m.note = audioPlayingNote
	playContext, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.playCancel = cancel
	for _, step := range []struct {
		key           rune
		focus, action int
	}{
		{tea.KeyUp, 4, 0},
		{tea.KeyUp, 2, 0},
		{tea.KeyDown, 4, 0},
		{tea.KeyDown, 4, 0},
		{tea.KeyDown, 4, 1},
		{tea.KeyDown, 4, 1},
		{tea.KeyLeft, 4, 0},
	} {
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: step.key})
		require.Equal(t, step.focus, m.focus)
		require.Equal(t, step.action, m.resultAction)
		require.True(t, m.playing)
		require.Equal(t, uint64(42), m.playID)
		require.Same(t, &result, m.result)
		require.NoError(t, playContext.Err(), "navigation stopped playback")
	}
}

func TestAudioNavigationSubmissionRemainsExplicit(t *testing.T) {
	for _, focus := range []int{0, 1} {
		t.Run(fmt.Sprintf("focus_%d", focus), func(t *testing.T) {
			m, editor := audioNavigationEditor(t, focus, "Synthetic words")
			_, newline := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
			require.Nil(t, newline)
			require.False(t, m.busy)
			require.Equal(t, "Synthetic words\n", editor.Value())
			for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: 'g', Mod: tea.ModCtrl}} {
				m.lastFinished = time.Now()
				_, command := m.Update(key)
				require.Nil(t, command, "repeat guard allowed another submission")
				require.False(t, m.busy)
				m.lastFinished = time.Time{}
				_, command = m.Update(key)
				require.NotNil(t, command, "explicit submission did not schedule generation")
				require.True(t, m.busy)
				require.Equal(t, "Working: generate", m.note)
				// Do not execute the returned generation command in this state test.
				m.busy = false
			}
		})
	}
}

func TestAudioNavigationTextareaPaddingControl(t *testing.T) {
	// Control for the pinned dependency: bypass audio Update and its workaround.
	editor := textarea.New()
	editor.ShowLineNumbers = false
	editor.Prompt = ""
	editor.SetWidth(32)
	editor.SetHeight(1)
	editor.SetValue(strings.Repeat("界🙂", 24))
	editor.Focus()
	require.Equal(t, 3, editor.LineInfo().RowOffset)
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, 2, editor.LineInfo().RowOffset)
	for range 2 {
		editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		require.Equal(t, 2, editor.LineInfo().RowOffset, "dependency control must reproduce the unreachable padding row")
	}
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	require.Equal(t, 3, editor.LineInfo().RowOffset)
	require.Equal(t, 0, editor.LineInfo().RowOffset-editor.ScrollYOffset())
}
