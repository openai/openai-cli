package audio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioTransportClockSelectsAndEditsTime(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {133, 39}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := audioResultNavigationModel(t, size[0], size[1])
			clock := func() (int, int) {
				t.Helper()
				for y, row := range strings.Split(ansi.Strip(m.View().Content), "\n") {
					if prefix, _, ok := strings.Cut(row, "00:03.000 / "); ok {
						return ansi.StringWidth(prefix), y
					}
				}
				t.Fatal("elapsed and total time are missing")
				return 0, 0
			}
			x, y := clock()
			audioReviewKey(m, tea.KeyRight)
			audioReviewKey(m, tea.KeyRight)
			require.Equal(t, audioTimeAction, m.resultAction)
			require.Contains(t, ansi.Strip(m.View().Content), "› 00:03.000 / ")
			require.Contains(t, ansi.Strip(m.View().Content), "Enter set time")
			selectedX, selectedY := clock()
			require.Equal(t, [2]int{x, y}, [2]int{selectedX, selectedY})
			audioReviewKey(m, tea.KeyEnter)
			require.Equal(t, "seek", m.modal)
			m.Update(tea.PasteMsg{Content: "999999"})
			audioReviewKey(m, tea.KeyEnter)
			require.Equal(t, "Time exceeds the recording.", m.seekError)
			require.Equal(t, "00:03.000", m.seekInput.Value())
			require.Equal(t, 3*time.Second, m.result.position)
			m.Update(tea.PasteMsg{Content: "1.234"})
			audioReviewKey(m, tea.KeyEnter)
			require.Empty(t, m.modal)
			require.Equal(t, 1234*time.Millisecond, m.result.position)
			require.Equal(t, audioTimeAction, m.resultAction)

			audioReviewKey(m, tea.KeyUp)
			require.True(t, m.waveformFocused)
			audioReviewKey(m, 'g')
			require.Equal(t, "seek", m.modal)
			audioReviewKey(m, tea.KeyEscape)
			audioReviewKey(m, tea.KeyDown)
			require.False(t, m.waveformFocused)
			require.Equal(t, audioTimeAction, m.resultAction)
			audioReviewKey(m, 'g')
			require.Equal(t, "seek", m.modal)
			audioReviewKey(m, tea.KeyEscape)
			require.Equal(t, audioTimeAction, m.resultAction)
		})
	}
}

func TestAudioTransportClockRequiresKnownDuration(t *testing.T) {
	m := audioResultNavigationModel(t, 90, 34)
	// A measured duration still supports seeking when waveform peaks are absent.
	m.result.analysis.Peaks = nil
	audioReviewKey(m, tea.KeyRight)
	audioReviewKey(m, tea.KeyRight)
	require.Contains(t, ansi.Strip(m.View().Content), "› 00:03.000 / ")
	audioReviewKey(m, tea.KeyEnter)
	require.Equal(t, "seek", m.modal)
	audioReviewKey(m, tea.KeyEscape)

	for _, empty := range []bool{false, true} {
		if empty {
			m.result = nil
		} else {
			m.result.measured = false
		}
		m.focusSection(0)
		m.focusSection(4)
		require.LessOrEqual(t, m.resultAction, audioSaveAction)
		audioReviewKey(m, tea.KeyRight)
		audioReviewKey(m, tea.KeyRight)
		require.Equal(t, audioSaveAction, m.resultAction)
		require.NotContains(t, ansi.Strip(m.View().Content), "Enter set time")
		audioReviewKey(m, 'g')
		require.Empty(t, m.modal)
	}
}
