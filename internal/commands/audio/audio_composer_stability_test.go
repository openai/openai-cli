package audio

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestAudioComposerKeepsItsRowsThroughGeneration(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {100, 29}, {110, 32}, {133, 39}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("First line.\nSecond line.\nThird line.")
			m.instructions.SetValue("First direction.\nSecond direction.")
			m.settingsExpanded = true
			m.focusSection(1)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			compose := func() string {
				rows, _, _ := m.formRows(false)
				return strings.ReplaceAll(strings.Join(rows, "\n"), "› ", "  ")
			}
			start, actions := compose(), m.formActionRows()
			require.Contains(t, start, "Second direction.")

			_, cmd := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
			require.NotNil(t, cmd)
			require.True(t, m.busy)
			require.Equal(t, start, compose())
			require.Equal(t, actions, m.formActionRows())
			require.Contains(t, strings.Join(m.workspaceFooter(), "\n"), "Ctrl+C cancels")

			r := audioReviewResult("composer-stability", false)
			r.entry.Settings = m.effectiveSettings()
			m.Update(audioWorkMessage{kind: "generate", result: r})
			require.False(t, m.busy)
			require.Equal(t, 4, m.focus)
			require.Equal(t, start, compose())
			require.Equal(t, actions, m.formActionRows())
		})
	}
}

func TestAudioGenerationShowsProgressOnce(t *testing.T) {
	for _, size := range [][2]int{{55, 22}, {60, 24}, {133, 39}} {
		m := audioReviewModel(t)
		m.input.SetValue("Synthetic draft.")
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		require.Equal(t, 1, strings.Count(m.View().Content, "Creating speech"))
	}
}
