package audio

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func TestAudioHistorySearchFiltersDisplayedFieldsAndKeepsSelection(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	for _, size := range [][2]int{{40, 12}, {90, 32}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := audioReviewModel(t)
			m.width, m.height = size[0], size[1]
			var entries []audiohistory.Entry
			for i, fields := range [][3]string{{"Bell", "ash", "mp3"}, {"Train\n世界", "coral", "wav"}, {"Train lights", "sage", "wav"}} {
				entry := audioReviewResult(fmt.Sprint(i), false).entry
				entry.Settings.Input, entry.Settings.Voice, entry.Settings.Format = fields[0], fields[1], fields[2]
				entry.CreatedAt = now.AddDate(0, 0, -i)
				entries = append(entries, entry)
			}
			m.Update(audioWorkMessage{kind: "history", entries: entries})
			audioReviewKey(m, tea.KeyUp)
			require.NotNil(t, m.historyView(now).Cursor)
			audioReviewKey(m, tea.KeyDown)
			require.Nil(t, m.historyView(now).Cursor)
			audioReviewKey(m, tea.KeyDown)
			for _, key := range "TRAiN " {
				m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
			}
			view := m.historyView(now)
			require.NotNil(t, view.Cursor)
			require.Contains(t, view.Content, "History (2 of 3 recordings)")
			require.Contains(t, view.Content, "Yesterday")
			require.NotContains(t, view.Content, "Today")
			require.NotContains(t, view.Content, "Bell")
			require.NotContains(t, view.Content, "›")
			audioReviewKey(m, tea.KeyDown)
			require.Contains(t, m.historyView(now).Content, "› Train 世界")
			audioReviewKey(m, tea.KeyDown)
			require.Contains(t, m.historyView(now).Content, "› Train lights")

			for _, query := range []string{"SaGe", "WAV"} {
				m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
				m.Update(tea.PasteMsg{Content: query})
				audioReviewKey(m, tea.KeyDown)
				require.Contains(t, m.historyView(now).Content, "› Train lights")
			}
			audioReviewKey(m, tea.KeyEscape)
			require.Empty(t, m.modal)
			m.Update(audioWorkMessage{kind: "history", entries: entries})
			require.Contains(t, m.historyView(now).Content, "› Bell")
			require.Empty(t, m.historySearch.query)
		})
	}
}

func TestAudioHistorySearchSafeUnicodeAndNoMatches(t *testing.T) {
	m := audioReviewModel(t)
	m.width, m.height = 40, 12
	entry := audioReviewResult("private-id", false)
	entry.entry.Settings.Input = "A 世界 recording"
	entry.savedPaths = []string{"/exports/known.wav"}
	m.result = &entry
	m.Update(audioWorkMessage{kind: "history", entries: []audiohistory.Entry{entry.entry}})
	m.Update(tea.PasteMsg{Content: "世界語"})
	require.Contains(t, m.View().Content, "No matching recordings.")
	require.Contains(t, m.View().Content, "Ctrl+U clear")
	require.NotContains(t, m.View().Content, "known.wav")
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.Equal(t, "history", m.modal)
	audioReviewKey(m, tea.KeyBackspace)
	require.Equal(t, "世界", m.historySearch.query)
	require.Contains(t, m.View().Content, entry.entry.Settings.Input)
	require.Contains(t, m.View().Content, "known.wav")
	require.NotNil(t, m.View().Cursor)
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: "1️⃣"})
	require.Equal(t, 2+len("  Search  ")+2, m.View().Cursor.X)
	m.Update(tea.PasteMsg{Content: strings.Repeat("界", 40) + "\x1b[2J\n\t\u202eEND"})
	view := m.View()
	require.NotContains(t, view.Content, "\x1b")
	require.NotContains(t, view.Content, "\u202e")
	require.NotContains(t, view.Content, "\t")
	require.Contains(t, view.Content, "END")
	require.NotNil(t, view.Cursor)
	row := strings.Split(view.Content, "\n")[view.Cursor.Y]
	require.Equal(t, ansi.StringWidth(row), view.Cursor.X, "cursor should follow the visible query")
	require.Less(t, view.Cursor.X, m.width)
}

func TestAudioHistorySearchOpensTheMatchingStoredRecording(t *testing.T) {
	m := audioReviewModel(t)
	for _, input := range []string{"Morning clip", "Evening clip"} {
		settings := audioDefaultSettings()
		settings.Input = input
		wav := audioSeekWave(make([]byte, 128), 8000, 1)
		_, err := m.store.Capture(t.Context(), io.NopCloser(bytes.NewReader(wav)), settings)
		require.NoError(t, err)
	}
	m.Update(audioSeekWorker(t, m.history()))
	require.Len(t, m.entries, 2)
	wanted := m.entries[1]
	m.Update(tea.PasteMsg{Content: wanted.Settings.Input})
	message := audioSeekWorker(t, audioReviewKey(m, tea.KeyEnter))
	require.NoError(t, message.err)
	m.Update(message)
	require.Empty(t, m.modal)
	require.Equal(t, wanted.ID, m.result.entry.ID)
	require.Equal(t, wanted.Settings.Input, m.input.Value())
}
