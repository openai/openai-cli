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
			require.Contains(t, view.Content, "2 of 3 recordings")
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

func TestAudioHistorySearchByLocalCalendarDate(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	now := time.Date(2026, 10, 30, 0, 15, 0, 0, zone)
	m := audioReviewModel(t)
	for i, stamp := range []string{"2026-10-30T04:10:00Z", "2026-10-30T03:55:00Z", "2026-10-29T03:00:00Z", "2025-10-28T10:00:00Z", "2026-10-02T10:00:00Z"} {
		entry := audioReviewResult(fmt.Sprint(i), false).entry
		entry.Settings.Input, entry.Settings.Voice, entry.Settings.Format = "Train at the station", "coral", "wav"
		entry.CreatedAt, err = time.Parse(time.RFC3339, stamp)
		require.NoError(t, err)
		m.entries = append(m.entries, entry)
	}
	m.entries[3].Settings.Voice = "ash"
	unknown := audioReviewResult("unknown-date", false).entry
	unknown.CreatedAt = time.Time{}
	m.entries = append(m.entries, unknown)
	for query, want := range map[string][]string{
		"today": {"0"}, "YESTERDAY": {"1"}, "2026-10-28": {"2"},
		"oct 28": {"2", "3"}, "Oct 28, 2025": {"3"}, "October 28 2026": {"2"},
		"ash October 28 WAV train": {"3"}, "oct 2": {"4"}, "yesterday ash": {},
		"2025": {"3"}, "2025-10": {"3"}, "November": {}, "Oct 28, 2024": {},
	} {
		t.Run(query, func(t *testing.T) {
			m.filterHistory(query, now)
			var ids []string
			for _, entry := range m.visibleHistory(now) {
				ids = append(ids, entry.ID)
			}
			require.ElementsMatch(t, want, ids)
		})
	}
}

func TestAudioHistoryRelativeSearchRefreshesAcrossDSTAndMidnight(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	now := time.Date(2026, 11, 1, 23, 30, 0, 0, zone)
	m := audioReviewModel(t)
	for i, stamp := range []string{"2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z", "2026-10-31T04:10:00Z"} {
		entry := audioReviewResult(fmt.Sprint(i), false).entry
		entry.CreatedAt, err = time.Parse(time.RFC3339, stamp)
		require.NoError(t, err)
		m.entries = append(m.entries, entry)
	}
	m.filterHistory("yesterday", now)
	require.Equal(t, m.entries[2:], m.visibleHistory(now))
	// Both occurrences of 01:30 fall on the previous calendar day after midnight.
	require.Equal(t, m.entries[:2], m.visibleHistory(now.Add(time.Hour)))
}

func TestAudioHistoryYesterdayAfterSkippedMidnight(t *testing.T) {
	zone, err := time.LoadLocation("America/Santiago")
	require.NoError(t, err)
	now := time.Date(2026, 9, 7, 0, 30, 0, 0, zone)
	m := audioReviewModel(t)
	for _, day := range []int{6, 5} {
		entry := audioReviewResult(fmt.Sprint(day), false).entry
		entry.CreatedAt = time.Date(2026, 9, day, 12, 0, 0, 0, zone)
		m.entries = append(m.entries, entry)
	}
	m.filterHistory("yesterday", now)
	require.Equal(t, m.entries[:1], m.visibleHistory(now))
	require.Equal(t, "Yesterday", audioHistoryDate(m.entries[0].CreatedAt, now))
	require.Equal(t, "Sep 5", audioHistoryDate(m.entries[1].CreatedAt, now))
}

func TestAudioHistoryMonthAndYearSearchWithText(t *testing.T) {
	m := audioReviewModel(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for i, fields := range [][3]string{
		{"2026-09-10", "coral", "Sample"}, {"2025-09-12", "coral", "Sample"},
		{"2026-09-10", "ash", "Sample"}, {"2026-10-12", "ash", "May I speak?"},
	} {
		entry := audioReviewResult(fmt.Sprint(i), false).entry
		created, err := time.Parse(time.DateOnly, fields[0])
		require.NoError(t, err)
		entry.CreatedAt = created
		entry.Settings.Voice, entry.Settings.Input = fields[1], fields[2]
		m.entries = append(m.entries, entry)
	}
	for query, want := range map[string][]string{
		"September coral": {"0", "1"}, "2026-09 coral": {"0"}, "Sep 2026 coral": {"0"},
		"coral 2025": {"1"}, "May I speak": {"3"}, "Sep 2027 coral": {},
	} {
		t.Run(query, func(t *testing.T) {
			m.filterHistory(query, now)
			var ids []string
			for _, entry := range m.visibleHistory(now) {
				ids = append(ids, entry.ID)
			}
			require.ElementsMatch(t, want, ids)
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
	query := wanted.CreatedAt.Local().Format(time.DateOnly) + " " + wanted.Settings.Input
	m.Update(tea.PasteMsg{Content: query})
	message := audioSeekWorker(t, audioReviewKey(m, tea.KeyEnter))
	require.NoError(t, message.err)
	m.Update(message)
	require.Empty(t, m.modal)
	require.Equal(t, wanted.ID, m.result.entry.ID)
	require.Equal(t, wanted.Settings.Input, m.input.Value())
}
