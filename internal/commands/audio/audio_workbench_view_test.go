package audio

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func TestAudioViewFramesWorkspaceAndOnlyHighlightsSelectedControls(t *testing.T) {
	background := regexp.MustCompile(`\x1b\[(?:[0-9]+;)*48;2;[0-9]+;[0-9]+;[0-9]+(?:;[0-9]+)*m`)
	// A foreground RGB component ending in 48 is not an SGR background command.
	require.False(t, background.MatchString("\x1b[38;2;148;155;165m"))
	require.True(t, background.MatchString("\x1b[1;38;2;242;244;250;48;2;52;61;88m"))
	for _, dark := range []bool{false, true} {
		m := audioReviewModel(t)
		m.color, m.dark = true, dark
		m.input.SetValue("A short example.")
		for _, focus := range []int{0, 1, 2} {
			m.focusSection(focus)
			view := m.View().Content
			require.Equal(t, focus == 2, background.MatchString(view))
			require.Equal(t, 1, strings.Count(ansi.Strip(view), "╭"))
			require.Contains(t, ansi.Strip(view), "│")
		}
		m.modal, m.setting, m.choices = "choice", 0, audioModels
		require.True(t, background.MatchString(m.View().Content))
		r := audioReviewResult("result", false)
		m.modal, m.result = "", &r
		m.focusSection(4)
		view := m.View().Content
		require.Contains(t, ansi.Strip(view), "Generate speech")
		require.Contains(t, ansi.Strip(view), "Instructions")
		require.True(t, background.MatchString(view))
		m.openSave()
		view = m.View().Content
		require.Contains(t, ansi.Strip(view), "Filename")
		require.Contains(t, ansi.Strip(view), audioDefaultSaveFolder)
		require.Contains(t, ansi.Strip(view), "Full recording")
		require.False(t, background.MatchString(view), "the editable folder uses the native caret")
		audioReviewKey(m, tea.KeyTab)
		require.True(t, background.MatchString(m.View().Content))
	}
}

func TestAudioViewUsesPanelAndAlignedSettings(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Read this text.")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	view := ansi.Strip(m.View().Content)
	require.True(t, strings.HasPrefix(view, "╭"))
	require.Contains(t, strings.Split(view, "\n")[1], "Text to speech")
	require.NotContains(t, view, "Setting        Value")
	for _, row := range []string{"Model             gpt-4o-mini-tts", "Voice             Coral", "Format            WAV", "Speed             1.00 x"} {
		require.Contains(t, view, row)
	}
	require.Contains(t, view, "Generate speech")
	require.NotContains(t, view, "Saves audio + inputs locally.")
	require.Contains(t, view, "More actions…")
	require.NotContains(t, view, "Preview voice")
	require.NotContains(t, view, "View command")
	require.NotContains(t, view, "Bash/zsh:")
	m.focus = 2
	m.setting = 1
	m.input.Blur()
	view = ansi.Strip(m.View().Content)
	require.Contains(t, strings.Split(view, "\n")[2], "Read this text.", "primary editor must remain visible while choosing settings")
	require.Contains(t, view, "› Voice             Coral")
}

func TestAudioViewSelectedSettingAndPrimaryActionsStayVisible(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {100, 24}, {100, 32}} {
		m := audioReviewModel(t)
		m.input.SetValue("First line\nSecond line\nThird line")
		m.instructions.SetValue("Keep all three lines.")
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.focus, m.note = 2, "Audio action failed."
		labels := []string{"Model", "Voice", "Format", "Speed", "Generate speech", "History", "More actions…"}
		for i, label := range labels {
			m.setting = i
			view := ansi.Strip(m.View().Content)
			require.Contains(t, view, "› "+label, "selected setting must remain visible")
			require.NotContains(t, view, "Saves audio + inputs locally.")
			require.NotContains(t, view, "Audio action failed.", "old notes must not restore the removed footer")
		}
	}
}

func TestAudioViewEditorSizeTracksMultilineInput(t *testing.T) {
	m := audioReviewModel(t)
	require.Equal(t, 1, m.input.Height())
	m.Update(tea.PasteMsg{Content: "First\nSecond\nThird\nFourth"})
	require.Equal(t, 3, m.input.Height())
	require.Equal(t, "First\nSecond\nThird\nFourth", m.input.Value())
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	require.Equal(t, 1, m.input.Height())
	require.LessOrEqual(t, m.input.Width(), 32)
	require.NotContains(t, ansi.Strip(m.View().Content), "› Text")
}

func TestAudioComposerUsesFullWidthAndKeepsRoomAtHeightBoundary(t *testing.T) {
	for _, retained := range []bool{false, true} {
		m := audioReviewModel(t)
		m.input.SetValue(strings.Repeat("x", 45))
		if retained {
			r := audioReviewResult("composer-height", false)
			m.result = &r
		}
		for _, height := range []int{24, 27, 28, 29, 27, 24, 28} {
			m.Update(tea.WindowSizeMsg{Width: 60, Height: height})
			require.Equal(t, 1, m.input.Height())
			require.Equal(t, 1, m.instructions.Height(), "composer spacing must not enlarge Instructions")
			view := ansi.Strip(m.View().Content)
			rows := strings.Split(view, "\n")
			require.Contains(t, rows[1], "Text to speech")
			require.NotContains(t, rows[1], m.input.Value())
			require.Contains(t, rows[2], m.input.Value(), "the draft must use the width beside its former label")
			for _, label := range []string{"Instructions", "Model", "Generate speech", "Play", "Save audio"} {
				require.Contains(t, view, label)
			}
			for _, row := range rows {
				require.Less(t, ansi.StringWidth(row), 60)
			}
			require.LessOrEqual(t, len(rows), height)
		}
	}
}

func TestAudioViewCommandWrapPreservesTheExecutableText(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Short synthetic text")
	m.instructions.SetValue("Clear delivery")
	require.Nil(t, audioReviewMore(t, m, "View command"))
	require.Empty(t, m.modal)
	rows := m.commandDisplayRows()
	require.Equal(t, strings.ReplaceAll(m.commandPreview(), "\n", ""), ansi.Strip(strings.Join(rows, "")))
	require.Greater(t, len(rows), 1)
	for _, row := range rows {
		require.LessOrEqual(t, ansi.StringWidth(row), m.audioWidth())
	}
	require.Contains(t, audioFeedbackView(t, m), "printf")
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, "more", m.modal)
	require.Equal(t, 2, m.focus)
	require.NotContains(t, audioFeedbackView(t, m), "printf")
	audioReviewKey(m, tea.KeyEscape)
	require.Empty(t, m.modal)
	require.Equal(t, 2, m.focus)
	require.Equal(t, audioSettingMore, m.setting)
	require.NotContains(t, audioFeedbackView(t, m), "printf")
}

func TestAudioListenShortcutOpensRetainedAudioWithoutPlaying(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Draft remains editable")
	r := audioReviewResult("retained", false)
	m.result = &r
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	require.Equal(t, 4, m.focus)
	require.Same(t, &r, m.result)
	require.False(t, m.playing)
	require.Equal(t, "Draft remains editable", m.input.Value())
	m.openSave()
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	require.Equal(t, "save", m.modal, "Listen must not dismiss a Save dialog")
}

func TestAudioViewKeepsPanelStableAcrossFocus(t *testing.T) {
	for _, width := range []int{60, 100, 186} {
		m := audioReviewModel(t)
		m.input.SetValue("A clear short example.")
		m.Update(tea.WindowSizeMsg{Width: width, Height: 32})
		for _, focus := range []int{0, 1, 2} {
			m.focusSection(focus)
			view := ansi.Strip(m.View().Content)
			rows := strings.Split(view, "\n")
			require.Equal(t, 3, ansi.StringWidth(rows[1][:strings.Index(rows[1], "Text to speech")]))
			require.NotContains(t, rows[1], "A clear short example.")
			require.Contains(t, rows[2], "A clear short example.")
			require.Equal(t, 3, ansi.StringWidth(rows[2][:strings.Index(rows[2], "A clear short example.")]))
			require.Contains(t, rows[4], "Instructions")
			require.Equal(t, 1, strings.Count(view, "╭"), "only the workspace has an outline")
			require.NotContains(t, view, "Setting        Value")
			require.Contains(t, rows[6], "Model")
			require.Equal(t, 3, ansi.StringWidth(rows[6][:strings.Index(rows[6], "Model")]))
			require.Equal(t, 21, ansi.StringWidth(rows[6][:strings.Index(rows[6], m.settings.Model)]))
			require.NotContains(t, view, "› Text to speech")
			require.Equal(t, focus == 0 || focus == 1, m.View().Cursor != nil)
			if focus == 2 {
				require.NotContains(t, view, "Alt+Enter New line")
			} else {
				require.Contains(t, view, "Alt+Enter New line")
			}
			next := "Settings"
			if focus == 0 {
				next = "Instructions"
			}
			if focus == 2 {
				next = "Audio"
			}
			require.Contains(t, view, "Tab "+next)
		}
	}
}

func TestAudioComposerKeepsUnicodeContinuationAndLabeledSettingsAligned(t *testing.T) {
	for _, focus := range []int{0, 1} {
		m := audioReviewModel(t)
		m.input.SetValue("Text draft.")
		m.instructions.SetValue("Instruction draft.")
		m.focusSection(focus)
		editor := &m.input
		if focus == 1 {
			editor = &m.instructions
		}
		const draft = "界🙂 first line\ne\u0301 continuation\nFinal 世界."
		editor.SetValue(draft)
		m.Update(tea.WindowSizeMsg{Width: 101, Height: 32})
		editor.MoveToEnd()
		line, column := editor.Line(), editor.Column()
		for _, width := range []int{101, 60, 101} {
			m.Update(tea.WindowSizeMsg{Width: width, Height: 32})
			view := ansi.Strip(m.View().Content)
			require.Equal(t, draft, editor.Value())
			require.Equal(t, line, editor.Line())
			require.Equal(t, column, editor.Column(), "resize moved the Unicode caret")
			require.Equal(t, 1, strings.Count(view, "Text to speech"))
			for _, value := range append(strings.Split(draft, "\n"), m.settings.Model, "Coral", "WAV", "1.00 x") {
				found := false
				for _, row := range strings.Split(view, "\n") {
					if index := strings.Index(row, value); index >= 0 {
						column := 21
						if focus == 0 && strings.Contains(draft, value) {
							column = 3
						}
						require.Equal(t, column, ansi.StringWidth(row[:index]), "%q left its editor or value column", value)
						found = true
						break
					}
				}
				require.True(t, found, "%q disappeared after resize", value)
			}
		}
	}
}

func TestAudioSaveFieldsKeepTheirRowsAndValueColumns(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		m := audioReviewModel(t)
		r := audioReviewResult("retained", false)
		m.result = &r
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.openSave()
		m.path.SetValue("hello.wav")
		var originalRows [2]int
		for _, focus := range []int{2, 0, 1, 2} {
			m.focusSave(focus)
			m.resizeEditors()
			rows := strings.Split(ansi.Strip(m.View().Content), "\n")
			for index, value := range []string{"hello.wav", audioDefaultSaveFolder} {
				found := -1
				for row, text := range rows {
					if strings.Contains(text, value) {
						found = row
						column := 18
						if size[0] == 40 {
							column = 13
						}
						require.Equal(t, column, ansi.StringWidth(text[:strings.Index(text, value)]), "value moved below its label")
						break
					}
				}
				require.NotEqual(t, -1, found)
				if focus == 2 && originalRows[index] == 0 {
					originalRows[index] = found
				}
				require.Equal(t, originalRows[index], found, "changing focus moved a Save control")
			}
		}
	}
}

func TestAudioHistoryShowsPromptInsteadOfOpaqueID(t *testing.T) {
	m := audioReviewModel(t)
	entry := audioReviewResult("opaque-private-id", false).entry
	entry.Settings.Input = "A meaningful retained example"
	entry.Settings.Voice, entry.Settings.Format = "coral", "wav"
	m.modal, m.entries = "history", []audiohistory.Entry{entry}
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "A meaningful retained example")
		require.NotContains(t, view, entry.ID)
		if size[0] == 90 {
			require.Contains(t, view, "Enter open  Esc back")
		} else {
			require.Contains(t, view, "Enter open · Esc back")
		}
		if size[0] == 90 {
			require.Contains(t, view, "Coral · WAV")
		}
	}
	m.modal = ""
	m.result = &audioWorkResult{entry: entry}
	m.focusSection(0)
	require.NotContains(t, ansi.Strip(m.View().Content), "[Ctrl+O] Listen")
}

func TestAudioWidePanelKeepsHorizontalActions(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 186, Height: 34})
	m.input.SetValue("A short draft stays near its settings.")
	m.focusSection(2)
	m.setting = 4
	for index, key := range []rune{tea.KeyRight, tea.KeyRight, tea.KeyLeft, tea.KeyLeft} {
		require.Nil(t, audioReviewKey(m, key))
		require.Equal(t, []int{audioSettingHistory, audioSettingMore, audioSettingHistory, 4}[index], m.setting)
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, m.input.Value())
		require.Empty(t, m.modal)
		require.False(t, m.busy)
		for _, row := range strings.Split(view, "\n") {
			require.Equal(t, 185, ansi.StringWidth(row))
			if strings.Contains(row, "Generate speech") && !strings.Contains(row, "Generate speech to create") {
				require.Contains(t, row, "History")
				require.Contains(t, row, "More actions…")
			}
		}
	}
	require.Equal(t, 4, m.setting)
}

func TestAudioViewEditorGrowsWithWrappedText(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {90, 34}} {
		for _, unit := range []string{"word ", "界🙂e\u0301 "} {
			m := audioReviewModel(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.Update(tea.PasteMsg{Content: strings.Repeat(unit, 45)})
			require.Equal(t, 3, m.input.Height(), "wrapped text needs visible editing space")
			before := m.input.Value()
			row, col := m.input.Line(), m.input.Column()
			m.Update(tea.WindowSizeMsg{Width: size[0] + 5, Height: size[1]})
			require.Equal(t, before, m.input.Value())
			require.Equal(t, row, m.input.Line())
			require.Equal(t, col, m.input.Column(), "resize moved the editing cursor")
			view := ansi.Strip(m.View().Content)
			for _, label := range []string{"Model", "Instructions", "Generate speech", "History", "More actions…"} {
				require.Contains(t, view, label)
			}
			m.input.SetValue("Short text.")
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			require.Equal(t, 1, m.input.Height(), "one-line drafts occupy one editor row")
		}
	}
}

func TestAudioViewSizingPreservesWrappedCaretVisibility(t *testing.T) {
	for _, text := range []string{strings.Repeat("wrapped text ", 60), strings.Repeat("wrapped text ", 30) + "\nLast line"} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: 90, Height: 34})
		m.Update(tea.PasteMsg{Content: text})
		m.input.MoveToEnd()
		offset := m.input.ScrollYOffset()
		require.Positive(t, offset)
		before := m.input.View()
		m.Update(tea.WindowSizeMsg{Width: 90, Height: 34})
		require.Equal(t, offset, m.input.ScrollYOffset(), "sizing scrolled the active editor")
		require.Equal(t, before, m.input.View(), "sizing changed the visible text or caret")
		require.Equal(t, 3, m.input.Height())
	}
}

func TestAudioViewPanelResizeRetainsEditingPosition(t *testing.T) {
	for _, focus := range []int{0, 1} {
		m := audioReviewModel(t)
		m.focusSection(focus)
		text := strings.Repeat("界🙂e\u0301 wrapped text ", 25) + "\nEnd of draft."
		m.Update(tea.PasteMsg{Content: text})
		editor := &m.input
		if focus == 1 {
			editor = &m.instructions
		}
		value, line, column := editor.Value(), editor.Line(), editor.Column()
		for _, size := range [][2]int{{59, 24}, {60, 24}, {93, 24}, {92, 23}, {92, 24}, {59, 24}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			require.Equal(t, value, editor.Value())
			require.Equal(t, line, editor.Line())
			require.Equal(t, column, editor.Column())
			view := ansi.Strip(m.View().Content)
			require.Contains(t, view, "End of draft.", "resizing must keep the editing position visible")
			for _, action := range []string{"Generate speech", "History", "More actions…"} {
				require.Contains(t, view, action)
			}
			for _, row := range strings.Split(view, "\n") {
				require.Less(t, ansi.StringWidth(row), size[0])
			}
		}
	}
}

func TestAudioViewPanelRetainsInstructionsForUnsupportedModel(t *testing.T) {
	m := audioReviewModel(t)
	m.instructions.SetValue("Keep this delivery instruction.")
	m.settings.Model = "tts-1"
	for _, width := range []int{91, 92, 93} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "Not used by this model")
		require.Contains(t, view, "Keep this delivery instruction.")
		require.Contains(t, view, "Generate speech")
	}
	m.settings.Model = "gpt-4o-mini-tts"
	m.focusSection(1)
	require.Equal(t, "Keep this delivery instruction.", m.instructions.Value())
}

func TestAudioViewExpandingEditorShowsAllFittingLines(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 34})
	m.Update(tea.PasteMsg{Content: "First visible line.\nSecond visible line."})
	assertLines := func() {
		t.Helper()
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "First visible line.")
		require.Contains(t, view, "Second visible line.")
		require.Equal(t, 2, m.input.Height())
		require.Zero(t, m.input.ScrollYOffset())
	}
	assertLines()
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	assertLines()
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 34})
	assertLines()
	require.Equal(t, 1, m.focus)
	require.False(t, m.input.Focused())
	require.True(t, m.instructions.Focused())
	require.Equal(t, 1, m.input.Line())
	require.Equal(t, len("Second visible line."), m.input.Column())
	require.Equal(t, "First visible line.\nSecond visible line.", m.input.Value())
}

func TestAudioPanelPinsFooterBelowLongDraft(t *testing.T) {
	for _, width := range []int{60, 90, 186} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		for _, focus := range []int{0, 1, 2} {
			m.focusSection(focus)
			m.input.SetValue(strings.Repeat("Long draft.\n", 20))
			m.instructions.SetValue(strings.Repeat("Delivery instruction.\n", 20))
			m.resizeEditors()
			view := m.View()
			rows := strings.Split(ansi.Strip(view.Content), "\n")
			require.Len(t, rows, 24)
			for _, label := range []string{"Generate speech", "History", "More actions…", "Ctrl+C Exit", "Ctrl+G Generate"} {
				require.Contains(t, ansi.Strip(view.Content), label)
			}
			require.Contains(t, rows[len(rows)-1], "╰")
			require.Contains(t, rows[len(rows)-2], "│")
			require.NotEmpty(t, strings.Trim(rows[len(rows)-2], " │"), "footer must sit above bottom border")
		}
		m.quit = true
		require.Empty(t, m.View().Content)
	}
}
