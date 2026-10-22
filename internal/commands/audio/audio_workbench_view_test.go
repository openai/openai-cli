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

func TestAudioViewUsesOneEditorAndNoBackgroundFill(t *testing.T) {
	background := regexp.MustCompile(`\x1b\[[0-9;:]*48[;:]`)
	for _, dark := range []bool{false, true} {
		m := audioReviewModel(t)
		m.color, m.dark = true, dark
		m.input.SetValue("A short example.")
		for _, focus := range []int{0, 1, 2} {
			m.focusSection(focus)
			view := m.View().Content
			require.False(t, background.MatchString(view))
			require.Equal(t, 1, strings.Count(ansi.Strip(view), "╭"))
			require.Contains(t, ansi.Strip(view), "│")
		}
		m.modal, m.setting, m.choices = "choice", 0, audioModels
		require.False(t, background.MatchString(m.View().Content))
		r := audioReviewResult("result", false)
		m.modal, m.result = "", &r
		m.focusSection(4)
		view := m.View().Content
		require.NotContains(t, ansi.Strip(view), "Generate speech")
		require.NotContains(t, ansi.Strip(view), "Instructions")
		require.False(t, background.MatchString(view))
		m.openSave()
		view = m.View().Content
		require.Contains(t, ansi.Strip(view), "Filename")
		require.Contains(t, ansi.Strip(view), audioDefaultSaveFolder)
		require.Contains(t, ansi.Strip(view), "Full recording")
		require.False(t, background.MatchString(view))
	}
}

func TestAudioViewUsesPlainEditorAndAlignedSettings(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Read this text.")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	view := ansi.Strip(m.View().Content)
	require.True(t, strings.HasPrefix(view, "    Text to speech\n  ╭─"))
	require.Contains(t, view, "    Settings\n")
	for _, row := range []string{"Model          gpt-4o-mini-tts", "Voice          Coral", "Format         WAV", "Speed          1.00 x"} {
		require.Contains(t, view, row)
	}
	require.Contains(t, view, "Generate speech")
	require.NotContains(t, view, "Saves audio + inputs locally.")
	require.Contains(t, view, "More actions…")
	require.NotContains(t, view, "Preview voice")
	require.NotContains(t, view, "Command preview")
	require.NotContains(t, view, "Bash/zsh:")
	m.focus = 2
	m.setting = 1
	m.input.Blur()
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "╭─", "primary editor must remain visible while choosing settings")
	require.Contains(t, view, "› Voice          Coral")
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

func TestAudioViewCommandWrapPreservesTheExecutableText(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Short synthetic text")
	m.instructions.SetValue("Clear delivery")
	require.Nil(t, audioReviewMore(t, m, "Show command"))
	require.Equal(t, m.commandPreview(), m.infoText)
	rows := m.infoRows()
	require.Greater(t, len(rows), 1)
	for _, row := range rows {
		require.LessOrEqual(t, ansi.StringWidth(row), m.audioWidth())
	}
	require.Contains(t, audioFeedbackView(t, m), "printf")
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	require.Equal(t, "more", m.modal)
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
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

func TestAudioViewKeepsImageStyleFormStableAcrossFocus(t *testing.T) {
	for _, width := range []int{60, 100, 186} {
		m := audioReviewModel(t)
		m.input.SetValue("A clear short example.")
		m.Update(tea.WindowSizeMsg{Width: width, Height: 32})
		for _, focus := range []int{0, 1, 2} {
			m.focusSection(focus)
			view := ansi.Strip(m.View().Content)
			rows := strings.Split(view, "\n")
			require.Equal(t, 4, strings.Index(rows[0], "Text to speech"))
			require.Equal(t, min(96, width-4), ansi.StringWidth(rows[1])-2, "wide terminals must retain a bounded reading column")
			require.Contains(t, rows[2], "A clear short example.")
			require.Contains(t, rows[3], "╯")
			require.Equal(t, "    Settings", rows[4], "settings must follow the composer")
			require.Contains(t, rows[5], "Instructions", "delivery instructions belong inside Settings")
			require.Contains(t, rows[6], "Model")
			require.Equal(t, 1, strings.Count(view, "╭"), "Instructions must not replace the primary input")
			require.NotContains(t, view, "› Text")
			if focus == 2 {
				require.NotContains(t, view, "[Alt+Enter] New line")
			} else if width >= 100 {
				require.Contains(t, view, "[Alt+Enter] New line")
			}
			next := "Settings"
			if focus == 0 {
				next = "Instructions"
			}
			if focus == 2 {
				next = "Text"
			}
			require.Contains(t, view, "[Tab] "+next)
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
						column := 19
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
		require.Contains(t, view, "Enter open · Esc back")
		if size[0] == 90 {
			require.Contains(t, view, "Coral · WAV")
		}
	}
	m.modal = ""
	m.result = &audioWorkResult{entry: entry}
	m.focusSection(0)
	require.NotContains(t, ansi.Strip(m.View().Content), "[Ctrl+O] Listen")
}

func TestAudioWideFormKeepsActionRowAndEditorWithinReadingColumn(t *testing.T) {
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
			require.LessOrEqual(t, ansi.StringWidth(row), 98)
			if strings.Contains(row, "Generate speech") {
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
			for _, label := range []string{"Settings", "Instructions", "Generate speech", "History", "More actions…"} {
				require.Contains(t, view, label)
			}
			m.input.SetValue("Short text.")
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			require.Equal(t, 1, m.input.Height(), "short text should not retain empty rows")
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
