package audio

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/stretchr/testify/require"
)

func TestTerminalTranslationPanelFitsAcrossFrameThreshold(t *testing.T) {
	for _, width := range []int{40, 59, 60, 80, 81, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := &terminalTranslationModel{width: width, height: 16,
				options: terminalTranslationOptions{Config: audiolive.Config{Language: "fr"}}}
			m.focusSetupAction()
			check := func(content string) string {
				t.Helper()
				require.NotContains(t, content, "\x1b")
				rows := strings.Split(content, "\n")
				require.LessOrEqual(t, len(rows), m.height)
				for _, row := range rows {
					require.Less(t, ansi.StringWidth(row), width)
				}
				if width >= 60 {
					require.True(t, strings.HasPrefix(rows[0], "╭"))
					require.True(t, strings.HasSuffix(rows[len(rows)-2], "╯"))
				}
				return rows[len(rows)-1]
			}
			setup := m.View().Content
			require.Contains(t, setup, "[Microphone]")
			require.Contains(t, setup, "French")
			require.Contains(t, setup, "› Start microphone")
			require.Contains(t, check(setup), "Esc exit")

			m.started, m.finished = true, true
			m.result = terminalTranslationResult{stage: &terminalTranslationStage{}, Complete: true}
			m.state = terminalTranslationUpdate{Original: "How are you?", Translation: "Comment allez-vous ?"}
			result := m.View().Content
			require.Contains(t, result, "Completed")
			require.Contains(t, result, "How are you?")
			require.Contains(t, result, "Comment allez-vous ?")
			require.Contains(t, check(result), "T read · S save")
			for _, row := range strings.Split(result, "\n") {
				if strings.Contains(row, "Original") {
					require.Equal(t, width >= 81, strings.Contains(row, "Translation"))
				}
			}
		})
	}
}

func TestTerminalTranslationEditorCaretFollowsFrameOnResize(t *testing.T) {
	m := &terminalTranslationModel{width: 59, height: 16, form: terminalTranslationForm{source: translationText}}
	m.focusSetupAction()
	require.Contains(t, m.View().Content, "Enter edit")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.PasteMsg{Content: "世界"})
	for _, width := range []int{59, 60, 40, 100} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 16})
		view := m.View()
		require.NotNil(t, view.Cursor)
		require.Equal(t, tea.CursorBar, view.Cursor.Shape)
		require.True(t, view.Cursor.Blink)
		line := strings.Split(view.Content, "\n")[view.Cursor.Y]
		prefix := "  世界"
		if width >= 60 {
			prefix = "│ 世界"
		}
		require.True(t, strings.HasPrefix(line, prefix))
		require.Equal(t, ansi.StringWidth(prefix), view.Cursor.X)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "世界", m.form.text)
}
