package audio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
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
	require.Equal(t, 1, strings.Count(view, "Coral  WAV"))
	require.Contains(t, view, "10:00")
	require.Contains(t, view, "Enter open")
	require.Contains(t, view, "Esc back")
	require.NotContains(t, view, "private-")
}

func TestAudioHistoryKeepsEverySelectionAndFooterVisible(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	for _, size := range [][2]int{{40, 12}, {60, 24}, {90, 32}, {101, 29}} {
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
			require.Contains(t, view, "Enter open")
			require.Contains(t, view, "Esc back")
			require.LessOrEqual(t, len(strings.Split(view, "\n")), size[1])
			for _, line := range strings.Split(view, "\n") {
				require.LessOrEqual(t, ansi.StringWidth(line), size[0])
			}
		}
	}
}

func TestAudioHistoryTableHeadersCountAndRightInset(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := audioReviewModel(t)
			m.width, m.height = size[0], size[1]
			entry := audioReviewResult("opaque-id", false).entry
			entry.Settings.Input = "Short clip"
			entry.CreatedAt = time.Date(2026, 10, 27, 10, 30, 0, 0, time.UTC)
			m.entries = []audiohistory.Entry{entry}
			view := ansi.Strip(m.historyView(entry.CreatedAt).Content)
			require.Contains(t, view, "History (1 recording)")
			require.Contains(t, view, "Clip")
			require.Contains(t, view, "› Short clip")
			require.NotContains(t, view, "opaque-id")
			if size[0] < 60 {
				require.NotContains(t, view, "Created time")
				return
			}
			for _, heading := range []string{"Voice", "Format", "Created time"} {
				require.Contains(t, view, heading)
			}
			for _, line := range strings.Split(view, "\n") {
				if strings.Contains(line, "10:30") || strings.Contains(line, "Created time") {
					require.True(t, strings.HasSuffix(line, "  │"), "time touches frame: %q", line)
				}
			}
		})
	}
}

func TestAudioHistoryNeverLeavesOrphanDateHeading(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		m := audioReviewModel(t)
		m.width, m.height = size[0], size[1]
		m.store.Directory = "/synthetic/history"
		dates := map[string]bool{}
		for i := range 80 {
			entry := audioReviewResult(fmt.Sprintf("private-%d", i), false).entry
			entry.Settings.Input = fmt.Sprintf("Recording %02d", i)
			entry.CreatedAt = now.AddDate(0, 0, -i/2)
			dates[audioHistoryDate(entry.CreatedAt, now)] = true
			m.entries = append(m.entries, entry)
		}
		for selected := range m.entries {
			m.choice = selected
			r := audioWorkResult{entry: m.entries[selected], savedPaths: []string{"/exports/known.wav"}}
			m.result = &r
			view := ansi.Strip(m.historyView(now).Content)
			rows := strings.Split(view, "\n")
			for index, line := range rows {
				label := strings.Trim(line, " │")
				if dates[label] {
					require.Less(t, index+1, len(rows))
					next := strings.Trim(rows[index+1], " │")
					require.True(t, strings.HasPrefix(next, "Recording ") || strings.HasPrefix(next, "› Recording "), "size %v, selection %d, orphan %q followed by %q", size, selected, label, next)
				}
			}
			require.Contains(t, view, fmt.Sprintf("› Recording %02d", selected))
			require.Contains(t, view, "History: /synthetic/history")
			require.Contains(t, view, "Saved: /exports/known.wav")
			require.Contains(t, view, "Esc back")
		}
	}
}

func TestAudioHistoryLocationsUseOnlyMatchingSessionEvidence(t *testing.T) {
	m := audioReviewModel(t)
	m.width, m.height = 101, 29
	m.store.Directory = "/synthetic/history"
	selected := audioReviewResult("selected", false)
	selected.entry.Settings.Input = "Selected recording"
	m.entries = []audiohistory.Entry{selected.entry}
	other := audioReviewResult("other", false)
	other.savedPaths = []string{"/exports/unrelated.wav"}
	m.result = &other
	view := ansi.Strip(m.historyView(time.Now()).Content)
	require.Contains(t, view, "History: /synthetic/history")
	require.NotContains(t, view, "Saved:")
	require.NotContains(t, view, "unrelated.wav")
	require.NotContains(t, strings.ToLower(view), "unsaved")
	selected.savedPaths = []string{"/exports/previous.wav", "/exports/confirmed.wav"}
	m.priorResult = &selected
	view = ansi.Strip(m.historyView(time.Now()).Content)
	require.Contains(t, view, "Saved: /exports/confirmed.wav")
	require.NotContains(t, view, "previous.wav")
	require.NotContains(t, view, "unrelated.wav")
	m.result, m.priorResult = &selected, nil
	view = ansi.Strip(m.historyView(time.Now()).Content)
	require.Contains(t, view, "Saved: /exports/confirmed.wav")
	selected.savedPaths = nil
	view = ansi.Strip(m.historyView(time.Now()).Content)
	require.NotContains(t, view, "Saved:")
	require.Equal(t, "/synthetic/history", m.store.Directory)
	require.Equal(t, []audiohistory.Entry{selected.entry}, m.entries)
}

func TestAudioHistoryPathsAndMetadataStaySafeAndBounded(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		for _, color := range []bool{false, true} {
			m := audioReviewModel(t)
			m.width, m.height, m.color = size[0], size[1], color
			m.store.Directory = "/history/" + strings.Repeat("界", 80) + "\n\t\u202e\x1b[2J"
			entry := audioReviewResult("opaque-id", false)
			entry.entry.Settings.Input = "Visible clip " + strings.Repeat("界", 60)
			entry.entry.Settings.Voice = "coral\n\t\x1b[2J"
			entry.entry.Settings.Format = "wav\n\t\u202e"
			entry.savedPaths = []string{"/exports/" + strings.Repeat("界", 80) + "\n\t\u202e\x1b[2J/clip.wav"}
			m.entries, m.result = []audiohistory.Entry{entry.entry}, &entry
			view := m.historyView(time.Now()).Content
			plain := ansi.Strip(view)
			require.NotContains(t, view, "\x1b[2J")
			require.NotContains(t, view, "\u202e")
			require.NotContains(t, view, "\t")
			require.Contains(t, plain, "› Visible clip")
			require.Contains(t, plain, "History:")
			require.Contains(t, plain, "Saved:")
			require.Contains(t, plain, "clip.wav")
			require.Contains(t, plain, "Esc back")
			require.LessOrEqual(t, len(strings.Split(view, "\n")), size[1])
			for _, line := range strings.Split(view, "\n") {
				require.LessOrEqual(t, ansi.StringWidth(line), size[0])
			}
			if !color {
				require.Equal(t, plain, view)
			} else if m.framed() {
				require.Contains(t, view, "48;2;52;61;88", "selected row must use the existing dark palette fill")
			}
		}
	}
}

func TestAudioHistoryEmptyViewShowsActualFolder(t *testing.T) {
	m := audioReviewModel(t)
	m.width, m.height = 40, 12
	m.store.Directory = "/synthetic/history"
	view := ansi.Strip(m.historyView(time.Now()).Content)
	require.Contains(t, view, "History (0 recordings)")
	require.Contains(t, view, "No saved audio yet.")
	require.Contains(t, view, "History: /synthetic/history")
	require.NotContains(t, view, "Saved:")
	require.Contains(t, view, "Esc back")
}

func TestAudioHistoryPathSuffixKeepsInsetAcrossWideCharacters(t *testing.T) {
	m := audioReviewModel(t)
	m.width, m.height = 40, 12
	for _, path := range []string{"/clip.wav", strings.Repeat("界", 30) + "/clip.wav", strings.Repeat("e\u0301", 50) + "/clip.wav", strings.Repeat("👨‍👩‍👧‍👦", 30) + "/clip.wav"} {
		row := ansi.Strip(m.historyPathRow("History", path))
		require.LessOrEqual(t, ansi.StringWidth(row), m.audioWidth()-2)
		require.True(t, strings.HasSuffix(row, "/clip.wav"))
		require.Contains(t, cliterminal.InlineFrame(m.historyPathRow("History", path), m.width), "/clip.wav")
		if path == "/clip.wav" {
			require.Equal(t, "  History: /clip.wav", row)
		} else {
			require.Contains(t, row, "History: …")
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
					require.Contains(t, view, audioActionDescriptions[item])
					require.NotContains(t, view, "Back to form")
					require.Contains(t, view, "Esc back")
					require.LessOrEqual(t, len(strings.Split(view, "\n")), height)
				}
			}
		}
	}
}

func TestAudioMoreActionsSeparatesActionsAndEscapePreservesDraftAndResult(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Keep this draft while browsing actions.")
	r := audioReviewResult("retained", false)
	m.result = &r
	m.modal = "more"
	view := ansi.Strip(m.View().Content)
	require.NotContains(t, view, m.input.Value())
	require.NotContains(t, view, "Text to speech")
	require.Contains(t, view, "More actions")
	require.NotContains(t, view, "Settings")
	require.NotContains(t, view, m.settings.Model)
	require.Equal(t, 1, strings.Count(view, "›"), "only the menu may show a focus marker")
	require.NotContains(t, view, "Back to form")
	audioReviewKey(m, tea.KeyEscape)
	require.Empty(t, m.modal)
	require.Equal(t, "Keep this draft while browsing actions.", m.input.Value())
	require.Same(t, &r, m.result)
}
