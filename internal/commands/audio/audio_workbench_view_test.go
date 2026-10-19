package audio

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioViewUsesPickerEditorAndAlignedSettings(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Read this text.")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	view := ansi.Strip(m.View().Content)
	require.True(t, strings.HasPrefix(view, "  Text to speech\n  \n  ╭ › Text "))
	require.Contains(t, view, "│ Read this text.")
	require.Contains(t, view, "    Settings\n")
	for _, row := range []string{"Model          gpt-4o-mini-tts", "Voice          Coral", "Format         WAV", "Speed          1.00 x"} {
		require.Contains(t, view, row)
	}
	require.Contains(t, view, "[ Generate speech ]")
	require.Contains(t, view, "Saves audio + inputs locally.")
	require.Contains(t, view, "More actions…")
	require.NotContains(t, view, "Preview voice")
	require.NotContains(t, view, "Command preview")
	require.NotContains(t, view, "Bash/zsh:")
	m.focus = 2
	m.setting = 1
	m.input.Blur()
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "╭ Text ", "primary editor must remain visible while choosing settings")
	require.Contains(t, view, "› Voice          Coral")
}

func TestAudioViewSelectedSettingAndPrimaryActionsStayVisible(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {100, 24}, {100, 32}} {
		m := audioReviewModel(t)
		m.input.SetValue("First line\nSecond line\nThird line")
		m.instructions.SetValue("Keep all three lines.")
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.focus, m.note = 2, "Audio action failed."
		labels := []string{"Model", "Voice", "Format", "Speed", "[ Generate speech ]", "More actions…"}
		for i, label := range labels {
			m.setting = i
			view := ansi.Strip(m.View().Content)
			require.Contains(t, view, "› "+label, "selected setting must remain visible")
			require.Contains(t, view, "Saves audio + inputs locally.")
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
	require.Contains(t, ansi.Strip(m.View().Content), "› Text")
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
