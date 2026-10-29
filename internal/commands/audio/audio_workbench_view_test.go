package audio

import (
	"regexp"
	"slices"
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
	require.Contains(t, view, "Settings          ▸ gpt-4o-mini-tts  WAV  1.00×")
	require.NotContains(t, view, "Model             ")
	m.settingsExpanded = true
	view = ansi.Strip(m.View().Content)
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
		m.settingsExpanded = true
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
	require.Equal(t, 2, m.input.Height())
	m.Update(tea.PasteMsg{Content: "First\nSecond\nThird\nFourth"})
	require.Equal(t, 4, m.input.Height())
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
			require.Equal(t, 2, m.input.Height())
			require.Equal(t, 1, m.instructions.Height(), "composer spacing must not enlarge Instructions")
			view := ansi.Strip(m.View().Content)
			rows := strings.Split(view, "\n")
			require.Contains(t, rows[1], "Text to speech")
			require.NotContains(t, rows[1], m.input.Value())
			require.Contains(t, rows[2], m.input.Value(), "the draft must use the width beside its former label")
			labels := []string{"Instructions", "Settings", "Generate speech", m.settings.Model}
			if retained {
				labels = append(labels, "Play audio", "Save audio")
			} else {
				labels = append(labels, "Your recording will appear here.")
			}
			for _, label := range labels {
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
			require.Contains(t, rows[6], "Voice")
			require.Equal(t, 3, ansi.StringWidth(rows[6][:strings.Index(rows[6], "Voice")]))
			require.Equal(t, 21, ansi.StringWidth(rows[6][:strings.Index(rows[6], "Coral")]))
			require.Contains(t, rows[7], "Settings          ▸ "+m.settings.Model)
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
				if m.inlineActions() {
					next = "Actions"
				}
			}
			require.Contains(t, view, "Tab "+next)
		}
	}
}

func TestAudioComposerKeepsSpaceAfterAnIntentionalNewline(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.PasteMsg{Content: "A draft.\n"})
	view := m.View()
	require.NotNil(t, view.Cursor)
	rows := strings.Split(ansi.Strip(view.Content), "\n")
	require.Contains(t, rows[view.Cursor.Y-1], "A draft.")
	require.Equal(t, "", strings.TrimSpace(strings.Trim(rows[view.Cursor.Y+1], "│ ")))
	require.Contains(t, rows[view.Cursor.Y+2], "Instructions")
}

func TestAudioComposerKeepsUnicodeContinuationAndLabeledSettingsAligned(t *testing.T) {
	for _, focus := range []int{0, 1} {
		m := audioReviewModel(t)
		m.input.SetValue("Text draft.")
		m.instructions.SetValue("Instruction draft.")
		m.settingsExpanded = true
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
		require.Contains(t, view, "Enter open  Esc back")
		if size[0] == 90 {
			require.Contains(t, view, "Coral  WAV")
		}
	}
	m.modal = ""
	m.result = &audioWorkResult{entry: entry}
	m.focusSection(0)
	require.NotContains(t, ansi.Strip(m.View().Content), "[Ctrl+O] Listen")
}

func TestAudioWidePanelActionsStayBesideDraft(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 186, Height: 34})
	m.input.SetValue("A short draft stays near its settings.")
	m.focusSection(2)
	m.setting = 4
	audioReviewKey(m, tea.KeyRight)
	for _, label := range []string{"History", "Preview voice", "Clear form", "View command", "Help"} {
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, m.input.Value())
		require.Contains(t, view, "› "+label)
		require.NotContains(t, view, "More actions…")
		for _, row := range strings.Split(view, "\n") {
			require.Equal(t, 185, ansi.StringWidth(row))
		}
		audioReviewKey(m, tea.KeyDown)
	}
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, 4, m.setting)
	require.Empty(t, m.modal)
	audioReviewKey(m, tea.KeyTab)
	require.Equal(t, "more", m.modal)
	audioReviewKey(m, tea.KeyTab)
	require.Equal(t, 4, m.focus)
	require.Empty(t, m.modal)
	require.Empty(t, m.entries)
	require.Nil(t, m.result)
}

func TestAudioWideActionsAlignWithTheComposer(t *testing.T) {
	m := audioReviewModel(t)
	m.settingsExpanded = true
	m.failure.Summary = "Synthetic failure."
	r := audioReviewResult("layout", false)
	r.entry.Settings = m.effectiveSettings()
	m.result = &r
	column := func(row, label string) int {
		t.Helper()
		index := strings.Index(row, label)
		require.NotEqual(t, -1, index, "%q in %q", label, row)
		return ansi.StringWidth(row[:index])
	}
	for _, size := range [][2]int{{110, 24}, {137, 41}, {186, 34}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := ansi.Strip(m.View().Content)
		rows := strings.Split(view, "\n")
		heading := slices.IndexFunc(rows, func(row string) bool { return strings.Contains(row, "Text to speech") })
		require.GreaterOrEqual(t, heading, 0)
		require.Contains(t, rows[heading], "Actions")
		require.NotContains(t, view, "F2")
		start := column(rows[heading], "Actions")
		require.LessOrEqual(t, start, 87, "Actions must remain beside the draft on wide terminals")
		require.NotContains(t, rows[heading+1], "History", "leave one open row below the heading")
		for i, name := range m.moreItems() {
			row := rows[heading+i+2]
			require.Equal(t, start, column(row, name))
			require.Equal(t, start+16, column(row, audioActionDescriptions[name]))
		}
		require.Contains(t, view, "Play audio")
		require.Contains(t, view, "Save audio")
		require.Contains(t, view, "Ctrl+C Exit")
		for _, row := range rows {
			require.Equal(t, size[0]-1, ansi.StringWidth(row))
		}
	}
	for _, size := range [][2]int{{109, 24}, {137, 23}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := ansi.Strip(m.View().Content)
		require.NotContains(t, view, "Actions")
		require.Contains(t, view, "More actions…")
	}
}

func TestAudioWideComposerContainsFieldHighlightsAndValidation(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 24})
	m.focusSection(2)
	m.setting = 1
	message := strings.TrimSpace(strings.Repeat("Check the selected voice. ", 4))
	m.validation = &audioFormValidation{field: 1, message: message}
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "› Voice")
	for _, row := range strings.Split(view, "\n") {
		if strings.Contains(row, "› Voice") {
			require.NotContains(t, row, "…", "the focus fill must end at the composer edge")
		}
	}
	lines := m.fieldValidationRows(1)
	require.Greater(t, len(lines), 1)
	for i, line := range lines {
		require.Contains(t, view, line)
		lines[i] = strings.TrimSpace(line)
	}
	require.Equal(t, message, strings.Join(lines, " "))
}

func TestAudioViewEditorGrowsWithWrappedText(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {90, 34}} {
		for _, unit := range []string{"word ", "界🙂e\u0301 "} {
			m := audioReviewModel(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.Update(tea.PasteMsg{Content: strings.Repeat(unit, 45)})
			require.GreaterOrEqual(t, m.input.Height(), 3, "wrapped text needs visible editing space")
			before := m.input.Value()
			row, col := m.input.Line(), m.input.Column()
			m.Update(tea.WindowSizeMsg{Width: size[0] + 5, Height: size[1]})
			require.Equal(t, before, m.input.Value())
			require.Equal(t, row, m.input.Line())
			require.Equal(t, col, m.input.Column(), "resize moved the editing cursor")
			view := ansi.Strip(m.View().Content)
			for _, label := range []string{"Settings", "Instructions", "Generate speech", "History", "More actions…"} {
				require.Contains(t, view, label)
			}
			m.input.SetValue("Short text.")
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			require.Equal(t, 2, m.input.Height(), "short drafts retain room for another line")
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
		require.Equal(t, 4, m.input.Height())
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
			actions := "More actions…"
			if m.inlineActions() {
				actions = "View command"
			}
			for _, label := range []string{"Generate speech", "History", actions, "Ctrl+C Exit", "Generate speech  Ctrl+G"} {
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
