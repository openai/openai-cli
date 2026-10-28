package audio

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioWaveformDetailRespectsWindowBudgetAndEmptyGeometry(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Short 世界 draft.")
	m.instructions.SetValue("Speak clearly.")
	r := audioReviewResult("detail-budget", false)
	r.entry.Settings = m.effectiveSettings()
	m.input.MoveToEnd()
	line, column := m.input.Line(), m.input.Column()
	for _, tc := range []struct{ width, height, wave, gap int }{
		{60, 24, 3, 0}, {80, 24, 5, 0}, {101, 27, 7, 0}, {101, 28, 9, 0}, {101, 29, 9, 0},
		{101, 30, 9, 1}, {101, 31, 9, 1}, {101, 40, 9, 1}, {101, 29, 9, 0}, {60, 24, 3, 0},
	} {
		for _, recorded := range []bool{false, true} {
			m.result = nil
			if recorded {
				m.result = &r
			}
			m.Update(tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
			view := ansi.Strip(m.View().Content)
			positions := audioWaveformDetailPositions(t, view)
			toolbarOffset := 1 + tc.gap
			if tc.width == 60 {
				toolbarOffset++ // Exact timing gets its own row when fixed controls would overlap.
			}
			if recorded {
				require.Equal(t, tc.wave, positions[1]-positions[0], "unexpected waveform height at %dx%d", tc.width, tc.height)
				require.Equal(t, positions[1]+toolbarOffset, positions[2], "only spare rows may separate the waveform and toolbar")
				require.Contains(t, view, "Coral • WAV")
				require.Contains(t, view, "00:00.000 / 00:01.000")
			} else {
				require.Equal(t, -1, positions[0])
				require.Equal(t, -1, positions[1])
				require.Contains(t, view, "No recording yet")
				require.NotContains(t, view, "┃")
			}
			require.Contains(t, view, m.input.Value())
			require.Contains(t, view, "Ctrl+C Exit")
			require.Equal(t, line, m.input.Line())
			require.Equal(t, column, m.input.Column())
			assertAudioWorkspaceBounds(t, view, [2]int{tc.width, tc.height})
		}
	}

	// Moving focus alone must not shrink a nine-row waveform or move its controls.
	m.Update(tea.WindowSizeMsg{Width: 101, Height: 31})
	initial := audioWaveformDetailPositions(t, ansi.Strip(m.View().Content))
	for _, focus := range []int{0, 1, 2, 4} {
		require.Equal(t, focus, m.focus)
		require.Equal(t, initial, audioWaveformDetailPositions(t, ansi.Strip(m.View().Content)))
		if focus < 4 {
			require.Nil(t, audioReviewKey(m, tea.KeyTab))
		}
	}
	require.Nil(t, audioReviewKey(m, tea.KeyUp))
	require.True(t, m.waveformFocused)
	require.Equal(t, initial, audioWaveformDetailPositions(t, ansi.Strip(m.View().Content)))
}

func TestAudioWaveformDetailReservesRecordingIdentityAndLongTiming(t *testing.T) {
	m := audioReviewModel(t)
	r := audioReviewResult("detail-retained", false)
	r.entry.Settings.Input = "Original recording."
	r.entry.Settings.Voice, r.entry.Settings.Format = "alloy", "mp3"
	r.entry.Bytes = 123456
	r.analysis.Duration = 9000000000
	r.position = 8999999999 * time.Second
	r.savedPaths = []string{"/exports/" + strings.Repeat("界", 50) + "/retained.mp3"}
	m.result = &r
	m.input.SetValue(strings.Repeat("Draft 世界 ", 30) + "\nDRAFT_TAIL")
	m.instructions.SetValue(strings.Repeat("Speak clearly 世界. ", 20) + "\nINSTRUCTION_TAILZ")
	m.focusSection(1)
	m.input.MoveToEnd()
	m.instructions.MoveToEnd()
	require.Nil(t, audioReviewKey(m, tea.KeyLeft))
	text, instructions := m.input.Value(), m.instructions.Value()
	line, column := m.instructions.Line(), m.instructions.Column()
	for _, size := range [][2]int{{101, 31}, {60, 24}, {101, 29}, {101, 31}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := ansi.Strip(m.View().Content)
		positions := audioWaveformDetailPositions(t, view)
		require.Less(t, positions[1]-positions[0], 9, "extra content must take priority over waveform detail")
		for _, text := range []string{
			"DRAFT_TAIL", "INSTRUCTION_TAIL", "Draft changed · Showing previous audio.",
			"Alloy", "MP3", "120.6 KiB", "Recording: Original recording.",
			"149999999:59.000 / 150000000:00.000", "Saved: …", "retained.mp3", "Ctrl+C Exit",
		} {
			require.Contains(t, view, text)
		}
		require.Equal(t, text, m.input.Value())
		require.Equal(t, instructions, m.instructions.Value())
		require.Equal(t, line, m.instructions.Line())
		require.Equal(t, column, m.instructions.Column())
		require.Same(t, &r, m.result)
		assertAudioNativeCursorBefore(t, m.View(), "Z")
		assertAudioWorkspaceBounds(t, view, size)
	}
}

func audioWaveformDetailPositions(t *testing.T, view string) [4]int {
	t.Helper()
	positions := [4]int{-1, -1, -1, -1}
	rows := strings.Split(view, "\n")
	for index, row := range rows {
		if positions[0] < 0 && strings.ContainsAny(row, "┃┠┨╂") {
			positions[0] = index
		}
		if strings.Contains(row, "┷") {
			require.Equal(t, -1, positions[1], "duplicate timeline rail")
			positions[1] = index
		}
		for item, label := range []string{"Play audio", "Save audio…"} {
			if strings.Contains(row, label) {
				positions[item+2] = index
			}
		}
	}
	require.Positive(t, positions[2], "Play must remain visible")
	require.Equal(t, positions[2], positions[3], "the player actions must share one row")
	return positions
}
