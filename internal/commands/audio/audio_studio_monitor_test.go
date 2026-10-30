package audio

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioStudioMonitorAnchorsRecordingAndTransport(t *testing.T) {
	for _, size := range [][2]int{{110, 28}, {133, 39}} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		width := m.studioMonitorWidth()
		const height = 18
		render := func(light, verb string) [3][2]int {
			rows, cursor := m.studioMonitorRows(width, height)
			require.Len(t, rows, height)
			require.Nil(t, cursor)
			var positions [3][2]int
			for i, label := range []string{light, verb, " / "} {
				found := false
				for y, row := range rows {
					require.LessOrEqual(t, ansi.StringWidth(row), width)
					before, _, ok := strings.Cut(ansi.Strip(row), label)
					if ok {
						positions[i] = [2]int{ansi.StringWidth(before), y}
						found = true
						break
					}
				}
				require.True(t, found, "missing %q", label)
			}
			return positions
		}
		initial := render("○ Idle", "Play audio")
		m.busy, m.note = true, "Working: generate"
		require.Equal(t, initial, render("◉ Generating", "Play audio"))
		m.busy, m.note = false, ""
		r := audioReviewResult("take", false)
		m.result, m.focus = &r, 4
		require.Equal(t, initial, render("○ Ready", "Play audio"))
		m.result.resumable, m.playing = true, true
		require.Equal(t, initial, render("◉ Playing", "Pause audio"))
		m.playing = false
		require.Equal(t, initial, render("◎ Paused", "Resume audio"))
		m.result.finished = true
		require.Equal(t, initial, render("● Completed", "Replay audio"))

		m.result.finished = false
		m.waveformFocused = true
		m.seekResult("right")
		rows, _ := m.studioMonitorRows(width, height)
		view := strings.Join(rows, "\n")
		require.Contains(t, view, "› ")
		require.Contains(t, view, "00:00")
		require.Contains(t, view, "00:01")
		require.Equal(t, 1, m.selectedBoundary())
		require.Equal(t, time.Second/time.Duration(audioWaveformBars(width-4)), m.result.position)
	}
}

func TestAudioStudioMonitorKeepsRetainedClipAndSeekCaret(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
	r := audioReviewResult("original", false)
	r.savedPaths = []string{"/tmp/" + strings.Repeat("界", 100) + "/saved.wav"}
	r.entry.Settings.Input = strings.Repeat("original 世界", 80)
	m.result, m.focus = &r, 4
	m.input.SetValue("New script")
	m.retainedGenerationFailure = true
	m.failure.Summary = strings.Repeat("error 世界", 30)
	const height = 18
	width := m.studioMonitorWidth()
	rows, _ := m.studioMonitorRows(width, height)
	text := strings.Join(rows, "\n")
	for _, label := range []string{"F1 details", "Draft changed. Showing previous audio.", "Recording: original", "Saved: ", "/saved.wav"} {
		require.Contains(t, text, label)
	}
	for _, row := range rows {
		require.LessOrEqual(t, ansi.StringWidth(row), width)
	}

	m.openSeek("0.5")
	rows, caret := m.studioMonitorRows(width, height)
	require.NotNil(t, caret)
	label := "Go to time  0.5"
	row := ansi.Strip(rows[caret.Y])
	end := strings.Index(row, label) + len(label)
	require.Contains(t, row, label)
	require.Equal(t, ansi.StringWidth(row[:end]), caret.X)
	require.NotContains(t, strings.Join(rows, "\n"), "Saved: ")
}

func TestAudioStudioRailStaysInPlaceAcrossRecordings(t *testing.T) {
	for _, size := range [][2]int{{110, 28}, {133, 39}} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		width := m.studioMonitorWidth()
		render := func() []string {
			rows, _ := m.studioMonitorRows(width, 18)
			for i := range rows {
				rows[i] = ansi.Strip(rows[i])
			}
			return rows
		}
		idle := render()
		rail := -1
		for i, row := range idle {
			if strings.Contains(row, "╶──") {
				rail = i
				break
			}
		}
		require.Positive(t, rail)
		require.Equal(t, []string{"00:00", "00:00"}, strings.Fields(idle[rail+1]))
		r := audioReviewResult("studio-rail", false)
		r.position = time.Second / 2
		m.result = &r
		measured := render()
		require.Equal(t, []string{"00:00", "00:01"}, strings.Fields(measured[rail+1]))
		column := audioRailMarkerColumn(t, measured[rail])
		require.Equal(t, 2+audioWaveformMarker(width-4, m.selectedBoundary()), column)
		wave := strings.Join(measured[:rail], "\n")
		require.Contains(t, wave, "┃")
		require.False(t, strings.ContainsAny(wave, "╂┠┨"))

		r.analysis.Peaks = nil
		durationOnly := render()
		require.Equal(t, measured[rail:rail+2], durationOnly[rail:rail+2])
		require.NotContains(t, strings.Join(durationOnly, "\n"), "┃")
		r.measured = false
		unknown := render()
		require.Equal(t, idle[rail], unknown[rail])
		require.Empty(t, strings.TrimSpace(unknown[rail+1]))
	}
}
