package audio

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/stretchr/testify/require"
)

func TestTerminalTranslationPanelFitsAcrossFrameThreshold(t *testing.T) {
	for _, width := range []int{40, 59, 60, 80, 81, 120} {
		for _, expanded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/expanded=%t", width, expanded), func(t *testing.T) {
				m := &terminalTranslationModel{width: width, height: 16, expanded: expanded,
					options: terminalTranslationOptions{Config: audiolive.Config{Language: "fr"}}}
				m.focusSetupAction()
				check := func(content string) {
					t.Helper()
					require.NotContains(t, content, "\x1b")
					rows := strings.Split(content, "\n")
					require.LessOrEqual(t, len(rows), m.height)
					for _, row := range rows {
						require.Less(t, ansi.StringWidth(row), width)
					}
					if width >= 60 {
						require.True(t, strings.HasPrefix(rows[0], "╭"))
						require.Contains(t, content, "╯\n")
					}
				}
				setup := m.View().Content
				require.Contains(t, setup, "[Microphone]")
				require.Contains(t, setup, "French")
				require.Contains(t, setup, "› Start microphone")
				require.Contains(t, setup, "Esc exit")
				check(setup)

				m.started, m.finished = true, true
				m.result = terminalTranslationResult{stage: &terminalTranslationStage{}, Complete: true}
				m.state = terminalTranslationUpdate{Original: "How are you?", Translation: "Comment allez-vous ?"}
				result := m.View().Content
				require.Contains(t, result, "Completed")
				require.Contains(t, result, "How are you?")
				require.Contains(t, result, "Comment allez-vous ?")
				require.Contains(t, result, "T read · S save")
				check(result)
				for _, row := range strings.Split(result, "\n") {
					if strings.Contains(row, "Original") {
						require.Equal(t, expanded && width >= 81, strings.Contains(row, "Translation"))
					}
				}
			})
		}
	}
}

func TestTerminalTranslationHighlightsSourceAcrossFocusAndLayoutChanges(t *testing.T) {
	m := &terminalTranslationModel{width: 122, height: 32, color: true, dark: true,
		options: terminalTranslationOptions{Config: audiolive.Config{Language: "fr"}}}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, translationFile, m.form.source)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	row := m.form.row
	highlight := audioSelectionStyle(true, true).Background(lipgloss.Color("#3159BC")).Render("[Audio file]")
	compact := m.View().Content
	require.Contains(t, compact, highlight)
	require.Contains(t, ansi.Strip(compact), "Ctrl+L expand")

	toggle := tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}
	m.Update(toggle)
	expanded := m.View().Content
	require.Contains(t, expanded, highlight)
	require.Contains(t, ansi.Strip(expanded), "Ctrl+L compact")
	require.Greater(t, ansi.StringWidth(strings.Split(expanded, "\n")[0]), ansi.StringWidth(strings.Split(compact, "\n")[0]))
	require.Equal(t, row, m.form.row)
	require.Equal(t, translationFile, m.form.source)
	require.False(t, m.started)

	m.Update(tea.WindowSizeMsg{Width: 40, Height: 16})
	require.Contains(t, m.View().Content, highlight)
	m.colorProfile = colorprofile.ANSI256
	highlight = audioSelectionStyle(true, true).Background(lipgloss.Color("25")).Render("[Audio file]")
	require.Contains(t, m.View().Content, highlight)
	m.color = false
	narrow := m.View().Content
	for _, source := range []string{"Microphone", "[Audio file]", "Typed text"} {
		require.Contains(t, narrow, source)
	}
	require.NotContains(t, narrow, "\x1b")
	m.Update(toggle)
	require.False(t, m.expanded)
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
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	require.Equal(t, 12, m.form.editor.Height())
	require.Contains(t, strings.Split(m.View().Content, "\n")[m.View().Cursor.Y], "世界")
	m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	require.Equal(t, 6, m.form.editor.Height())
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "世界", m.form.text)
}
