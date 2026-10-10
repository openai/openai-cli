package custom

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func TestAudioWorkbenchFailurePreservesKnownResultWithoutInventingRetention(t *testing.T) {
	prior := audioReviewResult("retained", false)
	for _, result := range []*audioWorkResult{nil, &prior} {
		m := audioReviewModel(t)
		m.result = result
		m.input.SetValue("Keep the draft text.")
		m.instructions.SetValue("Keep these instructions.")
		settings := m.settings
		failure := errors.New("synthetic request failure")
		m.Update(audioWorkMessage{kind: "generate", err: failure})
		if result == nil {
			require.Nil(t, m.result)
		} else {
			require.Same(t, result, m.result)
		}
		require.ErrorIs(t, m.lastErr, failure)
		require.Equal(t, "Keep the draft text.", m.input.Value())
		require.Equal(t, "Keep these instructions.", m.instructions.Value())
		require.Equal(t, settings, m.settings)
		require.Contains(t, m.View().Content, "Audio action failed.")
		require.NotContains(t, m.View().Content, "retained audio remains available")
		require.False(t, m.busy)
	}
}

func TestAudioWorkbenchPartialHistoryFailureIsVisibleWithUsableEntries(t *testing.T) {
	for _, width := range []int{40, 90} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		entry := audioReviewResult("retained", false).entry
		failure := errors.New("synthetic corrupt neighbor")
		m.Update(audioWorkMessage{kind: "history", entries: []audiohistory.Entry{entry}, err: failure})
		require.Equal(t, "history", m.modal)
		require.Equal(t, []audiohistory.Entry{entry}, m.entries)
		require.ErrorIs(t, m.lastErr, failure)
		require.Contains(t, m.View().Content, "Some audio history could not be read.")
		_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.NotNil(t, command, "valid history selection was disabled by its corrupt neighbor")
	}
}
