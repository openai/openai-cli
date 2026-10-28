package audio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func TestAudioHistoryGroupsDatesAndShowsMetadataOnce(t *testing.T) {
	m := audioReviewModel(t)
	m.width, m.height = 90, 32
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.FixedZone("local", -7*3600))
	for i := range 4 {
		entry := audioReviewResult(fmt.Sprintf("private-%d", i), false).entry
		entry.Settings.Input = fmt.Sprintf("Recording %d", i)
		entry.CreatedAt = now.Add(-time.Duration(i) * 12 * time.Hour)
		m.entries = append(m.entries, entry)
	}
	view := ansi.Strip(m.historyView(now).Content)
	require.Equal(t, 1, strings.Count(view, "Today"))
	require.Equal(t, 1, strings.Count(view, "Yesterday"))
	require.Equal(t, 1, strings.Count(view, "Coral · WAV"))
	require.Contains(t, view, "10:00")
	require.Contains(t, view, "Enter open · Esc back")
	require.NotContains(t, view, "private-")
}

func TestAudioHistoryKeepsEverySelectionAndFooterVisible(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	for _, size := range [][2]int{{40, 12}, {60, 24}, {90, 32}} {
		m := audioReviewModel(t)
		m.width, m.height = size[0], size[1]
		for i := range 80 {
			entry := audioReviewResult(fmt.Sprintf("opaque-%d", i), false).entry
			entry.Settings.Input = fmt.Sprintf("Recording %02d", i)
			entry.CreatedAt = now.AddDate(0, 0, -i/3)
			m.entries = append(m.entries, entry)
		}
		for i := range m.entries {
			m.choice = i
			view := ansi.Strip(m.historyView(now).Content)
			require.Contains(t, view, fmt.Sprintf("› Recording %02d", i), "size %v, choice %d", size, i)
			require.Contains(t, view, "Enter open · Esc back")
			require.LessOrEqual(t, len(strings.Split(view, "\n")), size[1]-1)
			for _, line := range strings.Split(view, "\n") {
				require.LessOrEqual(t, ansi.StringWidth(line), size[0])
			}
		}
	}
}

func TestAudioHistorySanitizesAndTruncatesPromptBeforeTime(t *testing.T) {
	m := audioReviewModel(t)
	m.width, m.height = 60, 12
	entry := audioReviewResult("private-id", false).entry
	entry.Settings.Input = strings.Repeat("界", 60) + "\x1b[2J\nsecond line"
	entry.CreatedAt = time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	m.entries = []audiohistory.Entry{entry}
	view := m.historyView(entry.CreatedAt).Content
	require.NotContains(t, view, "\x1b[2J")
	require.Contains(t, ansi.Strip(view), "10:00")
	for _, line := range strings.Split(view, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), m.width)
	}
}

func TestAudioHistoryDateUsesLocalCalendarAcrossDST(t *testing.T) {
	zone, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	now := time.Date(2026, 3, 9, 0, 15, 0, 0, zone)
	for _, tc := range []struct {
		created time.Time
		label   string
	}{
		{now.UTC(), "Today"},
		{time.Date(2026, 3, 8, 0, 0, 0, 0, zone), "Yesterday"},
		{time.Date(2025, 12, 31, 0, 0, 0, 0, zone), "Dec 31, 2025"},
		{time.Date(2026, 3, 1, 0, 0, 0, 0, zone), "Mar 1"},
		{time.Time{}, "Earlier"},
	} {
		require.Equal(t, tc.label, audioHistoryDate(tc.created, now))
	}
}

func TestAudioMoreActionsPreservesEveryActionAtCompactSizes(t *testing.T) {
	for _, width := range []int{40, 60, 90} {
		for _, height := range []int{12, 13, 14, 24, 32} {
			for _, failed := range []bool{false, true} {
				m := audioReviewModel(t)
				m.width, m.height = width, height
				if failed {
					m.rememberFailure("speech", audioFailure("Synthetic failure.", nil))
				}
				for i, item := range m.moreItems() {
					m.moreChoice = i
					view := ansi.Strip(m.moreView().Content)
					require.Contains(t, view, "› "+item)
					require.NotContains(t, view, "Back to form")
					require.Contains(t, view, "[Esc] Back")
					require.LessOrEqual(t, len(strings.Split(view, "\n")), height-1)
				}
			}
		}
	}
}

func TestAudioMoreActionsKeepsDraftContextAndEscapePreservesResult(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Keep this draft while browsing actions.")
	r := audioReviewResult("retained", false)
	m.result = &r
	m.modal = "more"
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, m.input.Value())
	require.Contains(t, view, "Text to speech")
	require.Contains(t, view, "More actions")
	require.Contains(t, view, "Settings")
	require.Contains(t, view, m.settings.Model)
	require.Equal(t, 1, strings.Count(view, "›"), "only the menu may show a focus marker")
	require.NotContains(t, view, "Back to form")
	audioReviewKey(m, tea.KeyEscape)
	require.Empty(t, m.modal)
	require.Equal(t, "Keep this draft while browsing actions.", m.input.Value())
	require.Same(t, &r, m.result)
}
