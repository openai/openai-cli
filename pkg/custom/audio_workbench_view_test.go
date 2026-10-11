package custom

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
	require.True(t, strings.HasPrefix(view, "  Text to speech\n  ╭ › Text "))
	require.Contains(t, view, "│ Read this text.")
	require.Contains(t, view, "    Settings\n")
	for _, row := range []string{"Model          gpt-4o-mini-tts", "Voice          Coral", "Format         WAV", "Speed          1.00 x"} {
		require.Contains(t, view, row)
	}
	require.Contains(t, view, "    Preview voice  Generate and play a sample")
	require.NotContains(t, view, "Bash/zsh:")
	m.focus = 2
	m.setting = 1
	m.input.Blur()
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "╭ Text ", "primary editor must remain visible while choosing settings")
	require.Contains(t, view, "› Voice          Coral")
}

func TestAudioViewSelectedSettingAndFeedbackStayVisible(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {100, 24}, {100, 32}} {
		m := audioReviewModel(t)
		m.input.SetValue("First line\nSecond line\nThird line")
		m.instructions.SetValue("Keep all three lines.")
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.focus, m.note = 2, "Audio action failed."
		labels := []string{"Model", "Voice", "Format", "Speed", "Preview voice", "Generate speech", "History", "Clear form"}
		for i, label := range labels {
			m.setting = i
			view := ansi.Strip(m.View().Content)
			require.Contains(t, view, "› "+label, "selected setting must survive wrapped feedback")
			require.Contains(t, view, "Audio action failed.")
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
	m.focus = 3
	rows := m.previewRows(false)
	require.Greater(t, len(rows), 1)
	var prefix strings.Builder
	for i, row := range rows {
		marker := "  "
		if i == 0 {
			marker = "› "
		}
		text, found := strings.CutPrefix(ansi.Strip(row), marker)
		require.True(t, found)
		prefix.WriteString(text)
	}
	// The visible rows preserve the start of the already shell-tested command.
	require.True(t, strings.HasPrefix(m.commandPreview(), prefix.String()))
	m.focus = 0
	require.NotContains(t, strings.Join(m.previewRows(false), "\n"), "› ")
}
