package audio

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/stretchr/testify/require"
)

func TestTranslationWaveformKeepsLiveAmplitudeScale(t *testing.T) {
	m := &terminalTranslationModel{state: terminalTranslationUpdate{Peaks: []float64{0, .18, .09, 0, 0, .35}}}
	layout := translationLayout{width: 8}
	before := m.translationWaveformRows(layout, 3)
	require.Len(t, before, 3)
	require.NotContains(t, strings.Join(before, ""), "┃")
	require.Contains(t, before[1], "·")
	m.state.Peaks[5] = 1
	after := m.translationWaveformRows(layout, 3)
	for i, row := range after {
		require.Equal(t, []rune(before[i])[:6], []rune(row)[:6])
	}
	require.NotEqual(t, before, after)
	require.Empty(t, m.translationWaveformRows(layout, 2))
	layout.expanded = true
	require.Len(t, m.translationWaveformRows(layout, 8), 7)
}

func TestTranslationWaveformUsesLiveSnapshotDuringResultHandoff(t *testing.T) {
	m := &terminalTranslationModel{state: terminalTranslationUpdate{Peaks: []float64{0, .7, .4}}}
	published := make(chan struct{})
	go func() {
		m.result = terminalTranslationResult{AudioBytes: 48000, SampleRate: 24000, Channels: 1}
		close(published)
	}()
	rows := m.translationWaveformRows(translationLayout{width: 12}, 3)
	<-published
	require.Len(t, rows, 3)
	require.NotContains(t, strings.Join(rows, ""), "┃")
}

func TestTranslationWaveformMarksReplayStartAtBothEndpoints(t *testing.T) {
	m := &terminalTranslationModel{
		width: 122, height: 32, started: true, finished: true,
		state:  terminalTranslationUpdate{Peaks: []float64{0, .7, 0, .4, 0}},
		result: terminalTranslationResult{AudioBytes: 48000, SampleRate: 24000, Channels: 1},
	}
	markerAt := func(column int) {
		t.Helper()
		wave := m.translationWaveformRows(m.translationLayout(), 8)
		require.NotEmpty(t, wave)
		for _, row := range wave {
			require.Equal(t, '┃', []rune(row)[column])
			require.Equal(t, 1, strings.Count(row, "┃"))
		}
	}
	markerAt(0)
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	markerAt(36)
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	markerAt(71)
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Equal(t, 500*time.Millisecond, m.seekPosition)
	m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	markerAt(57)
	m.replayDone = make(chan struct{})
	require.NotContains(t, strings.Join(m.translationWaveformRows(m.translationLayout(), 8), ""), "┃")
	require.Contains(t, m.translationAudioHeading(m.translationLayout()), "From 00:00.500 / 00:01.000")
}

func TestTranslationWaveformLeavesRoomForCaptionsAndWarnings(t *testing.T) {
	m := &terminalTranslationModel{
		width: 40, height: 16, started: true, finished: true,
		options: terminalTranslationOptions{Config: audiolive.Config{Language: "en"}},
		state: terminalTranslationUpdate{
			Original: "Original words", Translation: "Translated words", Peaks: []float64{.25, .4, 0},
			Warning: "Playback stopped.",
		},
		result: terminalTranslationResult{AudioBytes: 48000, SampleRate: 24000, Channels: 1, Complete: true},
	}
	view := m.View().Content
	for _, text := range []string{"Original words", "Translated words", "Seek 00:00.000 / 00:01.000", "Playback stopped.", "Esc exit"} {
		require.Contains(t, view, text)
	}
	for _, row := range strings.Split(view, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(row), 40)
	}
	m.finished = false
	m.state.AudioBytes, m.state.SampleRate, m.state.Channels = 48000, 24000, 1
	require.Contains(t, m.translationAudioHeading(m.translationLayout()), "00:01.000 received")
	m.state = terminalTranslationUpdate{Translation: "Typed translation"}
	require.Empty(t, m.translationAudioHeading(m.translationLayout()))
	require.Empty(t, m.translationWaveformRows(m.translationLayout(), 8))
}
