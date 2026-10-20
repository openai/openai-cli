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
	require.Zero(t, m.setting)
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
			order := []int{0, 1, 2}
			if withResult {
				result := audioReviewResult("retained", false)
				m.result = &result
				order = []int{0, 1, 2, 4}
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

func TestAudioNavigationSettingsArrowsDoNotWrapOrActivate(t *testing.T) {
	for _, withResult := range []bool{false, true} {
		t.Run(fmt.Sprintf("result_%t", withResult), func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Clear must remain an explicit action")
			m.focus, m.setting = 2, 0
			if withResult {
				result := audioReviewResult("retained", false)
				m.result = &result
			}
			for setting := 1; setting <= 5; setting++ {
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
				require.Equal(t, 2, m.focus)
				require.Equal(t, setting, m.setting)
			}
			for _, key := range []rune{tea.KeyEnd, tea.KeyHome, tea.KeyHome, tea.KeyEnd} {
				audioNavigationKey(t, m, tea.KeyPressMsg{Code: key})
				expected := 0
				if key == tea.KeyEnd {
					expected = 5
				}
				require.Equal(t, expected, m.setting)
				require.Equal(t, 2, m.focus)
			}
			audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			if withResult {
				require.Equal(t, 4, m.focus)
			} else {
				require.Equal(t, 2, m.focus)
				require.Equal(t, 5, m.setting)
			}
			m.focus, m.setting = 2, 5
			for setting := 4; setting >= 0; setting-- {
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

func TestAudioNavigationCommandScrollingStaysBounded(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue(strings.Repeat("Preview 世界 ", 200))
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	require.Nil(t, audioReviewMore(t, m, "Show command"))
	require.Equal(t, "info", m.modal)
	limit := func() int {
		return max(0, len(m.infoRows())-m.informationHeight())
	}
	require.Positive(t, limit())
	for range 100 {
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
		require.LessOrEqual(t, m.infoOffset, limit())
	}
	require.Equal(t, limit(), m.infoOffset)
	for _, key := range []rune{tea.KeyDown, tea.KeyPgDown, tea.KeyRight, tea.KeyEnd} {
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: key})
		require.Equal(t, limit(), m.infoOffset)
	}
	for _, width := range []int{160, 40, 80} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
		require.GreaterOrEqual(t, m.infoOffset, 0)
		require.LessOrEqual(t, m.infoOffset, limit())
		for _, line := range strings.Split(m.View().Content, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), width)
		}
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
		require.Equal(t, limit(), m.infoOffset)
	}
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	for _, key := range []rune{tea.KeyHome, tea.KeyPgUp, tea.KeyLeft} {
		audioNavigationKey(t, m, tea.KeyPressMsg{Code: key})
		require.Zero(t, m.infoOffset)
		require.Equal(t, "info", m.modal)
	}
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	require.Positive(t, m.infoOffset)
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	require.Zero(t, m.infoOffset)
	require.Equal(t, "info", m.modal)
	audioNavigationKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, "info", m.modal)
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	require.Equal(t, "more", m.modal)
	require.Equal(t, 2, m.focus)
	require.Equal(t, 5, m.setting)
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
					require.Equal(t, []int{0, 1, 0, 2, 2, 2}[index], m.recoveryChoice)
				}
				if (modal == "choice" || modal == "history") && index < 2 {
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
		{tea.KeyUp, 2, 0},
		{tea.KeyDown, 4, 0},
		{tea.KeyDown, 4, 1},
		{tea.KeyDown, 4, 1},
		{tea.KeyUp, 4, 0},
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
